package s3

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bin/internal/checksum"
	"bin/internal/httprange"
	"bin/internal/s3/s3err"
	"bin/internal/server"
	"bin/internal/storage"
)

const (
	// maxPutSize is the largest single PUT or copy S3 allows (5 GiB).
	maxPutSize = 5 << 30
	// maxUserMeta bounds the total size of x-amz-meta-* names and values.
	maxUserMeta = 2 << 10
	// defaultContentType is what S3 reports for objects stored without one.
	defaultContentType = "binary/octet-stream"
)

// storedHeaders are the standard headers S3 stores with an object and returns
// on GET and HEAD.
var storedHeaders = []string{"Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "Expires"}

// unsupportedPutHeaders name features bin does not implement; accepting them
// silently would mislead the client into thinking they took effect.
var unsupportedPutHeaders = []string{
	"X-Amz-Server-Side-Encryption", "X-Amz-Server-Side-Encryption-Customer-Algorithm",
	"X-Amz-Tagging", "X-Amz-Object-Lock-Mode", "X-Amz-Object-Lock-Retain-Until-Date",
	"X-Amz-Object-Lock-Legal-Hold", "X-Amz-Website-Redirect-Location",
}

func quoteETag(etag string) string { return `"` + etag + `"` }

// objectMetadata extracts what PutObject and CopyObject (REPLACE) store from
// the request headers.
func objectMetadata(r *http.Request) (contentType string, headers, meta map[string]string, err error) {
	contentType = r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = defaultContentType
	}
	headers = map[string]string{}
	for _, h := range storedHeaders {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	meta = map[string]string{}
	total := 0
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-meta-") {
			continue
		}
		key := strings.TrimPrefix(lower, "x-amz-meta-")
		value := strings.Join(values, ",")
		meta[key] = value
		total += len(key) + len(value)
	}
	if total > maxUserMeta {
		return "", nil, nil, s3err.ErrMetadataTooLarge
	}
	return contentType, headers, meta, nil
}

// checksumRequest is what the client asked to have verified.
type checksumRequest struct {
	algs     []checksum.Algorithm
	expected map[checksum.Algorithm]string // values known from headers
	md5      []byte                        // Content-MD5, if sent
}

// requestedChecksums reads the checksum the client announced, by header
// (x-amz-checksum-<alg>), by algorithm (x-amz-sdk-checksum-algorithm,
// x-amz-checksum-algorithm) or as a trailer (x-amz-trailer).
func requestedChecksums(r *http.Request) (checksumRequest, error) {
	req := checksumRequest{expected: map[checksum.Algorithm]string{}}
	add := func(a checksum.Algorithm) {
		for _, x := range req.algs {
			if x == a {
				return
			}
		}
		req.algs = append(req.algs, a)
	}
	for _, a := range checksum.All {
		v := r.Header.Get(a.Header())
		if v == "" {
			continue
		}
		if !a.Valid(v) {
			return req, s3err.ErrInvalidRequest.WithMessage("Value for " + a.Header() + " header is invalid.")
		}
		req.expected[a] = v
		add(a)
	}
	if len(req.expected) > 1 {
		return req, s3err.ErrInvalidRequest.WithMessage("Expecting a single x-amz-checksum- header. Multiple checksum Types are not allowed.")
	}
	for _, h := range []string{"X-Amz-Sdk-Checksum-Algorithm", "X-Amz-Checksum-Algorithm"} {
		if v := r.Header.Get(h); v != "" {
			a, ok := checksum.Parse(v)
			if !ok {
				return req, s3err.ErrInvalidRequest.WithMessage("Checksum algorithm provided is unsupported. Please try again with any of the valid types: [CRC32, CRC32C, CRC64NVME, SHA1, SHA256]")
			}
			add(a)
		}
	}
	for _, name := range strings.Split(r.Header.Get("X-Amz-Trailer"), ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if a, ok := checksum.Parse(strings.TrimPrefix(name, "x-amz-checksum-")); ok && strings.HasPrefix(name, "x-amz-checksum-") {
			add(a)
		}
	}
	if v := r.Header.Get("Content-Md5"); v != "" {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(b) != md5.Size {
			return req, s3err.ErrInvalidDigest
		}
		req.md5 = b
	}
	return req, nil
}

// verify compares the digests of the stored data with the client's. Trailer
// values are read from r.Trailer, which the body reader fills at EOF.
func (c checksumRequest) verify(r *http.Request, sums storage.Sums) error {
	if c.md5 != nil && string(c.md5) != string(sums.MD5) {
		return s3err.ErrBadDigest
	}
	for _, a := range c.algs {
		want := c.expected[a]
		if want == "" && r.Trailer != nil {
			want = r.Trailer.Get(a.Header())
		}
		if want == "" {
			continue // algorithm requested for storage only
		}
		if !a.Valid(want) {
			return s3err.ErrInvalidRequest.WithMessage("Value for " + a.Header() + " trailer is invalid.")
		}
		if want != checksum.Encode(sums.Checksums[a]) {
			return s3err.ErrBadDigest.WithMessage("The " + string(a) + " you specified did not match the calculated checksum.")
		}
	}
	return nil
}

func (h *Handler) putObject(w http.ResponseWriter, r *http.Request, req Request) error {
	for _, name := range unsupportedPutHeaders {
		if r.Header.Get(name) != "" {
			return s3err.ErrNotImplemented.WithMessage(name + " is not supported.")
		}
	}
	if r.ContentLength < 0 {
		return s3err.ErrMissingContentLength
	}
	if r.ContentLength > maxPutSize {
		return s3err.ErrEntityTooLarge
	}
	contentType, headers, meta, err := objectMetadata(r)
	if err != nil {
		return err
	}
	sums, err := requestedChecksums(r)
	if err != nil {
		return err
	}
	info, err := h.opts.Store.PutObject(r.Context(), req.Bucket, req.Key, r.Body, r.ContentLength, storage.PutOptions{
		ContentType: contentType,
		Headers:     headers,
		UserMeta:    meta,
		Checksums:   sums.algs,
		Verify:      func(s storage.Sums) error { return sums.verify(r, s) },
		IfMatch:     r.Header.Get("If-Match"),
		IfNoneMatch: r.Header.Get("If-None-Match"),
	})
	if err != nil {
		return storageError(err)
	}
	w.Header().Set("ETag", quoteETag(info.ETag))
	setVersionHeader(w, info.VersionID, false)
	setChecksumHeaders(w, info)
	w.WriteHeader(http.StatusOK)
	return nil
}

// setVersionHeader reports the version a request read or wrote. Objects of
// buckets that were never versioned are all "null", which S3 leaves out
// unless the client asked for a version.
func setVersionHeader(w http.ResponseWriter, versionID string, requested bool) {
	if versionID != "" && (versionID != storage.NullVersion || requested) {
		w.Header().Set("X-Amz-Version-Id", versionID)
	}
}

// requestedVersion reads the versionId query parameter ("" if absent).
func requestedVersion(r *http.Request) (string, error) {
	v := r.URL.Query().Get("versionId")
	if r.URL.Query().Has("versionId") && !storage.ValidVersionID(v) {
		return "", s3err.ErrInvalidArgument.WithMessage("Invalid version id specified")
	}
	return v, nil
}

// deleteMarkerError answers a read that found a delete marker: 404 when it
// is the latest version of the key, 405 when the client named it.
func deleteMarkerError(w http.ResponseWriter, marker storage.ObjectInfo, requested bool) error {
	w.Header().Set("X-Amz-Delete-Marker", "true")
	setVersionHeader(w, marker.VersionID, true)
	if requested {
		return s3err.ErrMethodNotAllowed
	}
	return s3err.ErrNoSuchKey
}

func setChecksumHeaders(w http.ResponseWriter, info storage.ObjectInfo) {
	for a, v := range info.Checksums {
		w.Header().Set(a.Header(), v)
	}
	if info.ChecksumType != "" {
		w.Header().Set("X-Amz-Checksum-Type", info.ChecksumType)
	}
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, req Request) error {
	versionID, err := requestedVersion(r)
	if err != nil {
		return err
	}
	info, err := h.opts.Store.StatVersion(r.Context(), req.Bucket, req.Key, versionID)
	if errors.Is(err, storage.ErrDeleteMarker) {
		return deleteMarkerError(w, info, versionID != "")
	}
	if err != nil {
		return storageError(err)
	}
	return h.respondObject(w, r, info, nil)
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, req Request) error {
	versionID, err := requestedVersion(r)
	if err != nil {
		return err
	}
	info, data, err := h.opts.Store.OpenVersion(r.Context(), req.Bucket, req.Key, versionID)
	if errors.Is(err, storage.ErrDeleteMarker) {
		return deleteMarkerError(w, info, versionID != "")
	}
	if err != nil {
		return storageError(err)
	}
	defer data.Close()
	return h.respondObject(w, r, info, data)
}

// respondObject writes the response for GET (data != nil) or HEAD. Whole
// objects are streamed through the SHA-256 verifier (per part for multipart
// objects); ranges and single parts are not verified.
func (h *Handler) respondObject(w http.ResponseWriter, r *http.Request, info storage.ObjectInfo, data *storage.ObjectData) error {
	if status := evaluateConditions(r, info); status == http.StatusPreconditionFailed {
		return s3err.ErrPreconditionFailed
	} else if status == http.StatusNotModified {
		w.Header().Set("ETag", quoteETag(info.ETag))
		w.Header().Set("Last-Modified", info.ModTime.Format(http.TimeFormat))
		w.WriteHeader(http.StatusNotModified)
		return nil
	}

	start, length, partial := int64(0), info.Size, false
	if pn := r.URL.Query().Get("partNumber"); pn != "" {
		n, err := strconv.Atoi(pn)
		if err != nil || n < 1 || n > 10000 {
			return s3err.ErrInvalidArgument.WithMessage("Part number must be an integer between 1 and 10000, inclusive")
		}
		off, size, ok := info.Part(n)
		if !ok {
			return s3err.ErrInvalidPartNumber
		}
		start, length, partial = off, size, true
		if info.PartsCount > 0 {
			w.Header().Set("X-Amz-Mp-Parts-Count", strconv.Itoa(info.PartsCount))
		}
	} else if rh := r.Header.Get("Range"); rh != "" {
		s, l, ok, satisfiable := httprange.Parse(rh, info.Size)
		if ok && !satisfiable {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
			return s3err.ErrInvalidRange
		}
		if ok {
			start, length, partial = s, l, true
		}
	}

	hdr := w.Header()
	hdr.Set("Accept-Ranges", "bytes")
	hdr.Set("Content-Type", info.ContentType)
	hdr.Set("ETag", quoteETag(info.ETag))
	hdr.Set("Last-Modified", info.ModTime.Format(http.TimeFormat))
	setVersionHeader(w, info.VersionID, r.URL.Query().Has("versionId"))
	for k, v := range info.Headers {
		hdr.Set(k, v)
	}
	for k, v := range info.UserMeta {
		hdr["x-amz-meta-"+k] = []string{v} // keep the stored name as is
	}
	for param, header := range responseOverrides {
		if v := r.URL.Query().Get(param); v != "" {
			hdr.Set(header, v)
		}
	}
	if !partial && strings.EqualFold(r.Header.Get("X-Amz-Checksum-Mode"), "ENABLED") {
		setChecksumHeaders(w, info)
	}
	hdr.Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		hdr.Set("Content-Range", httprange.ContentRange(start, length, info.Size))
	}
	w.WriteHeader(status)
	if data == nil || r.Method == http.MethodHead {
		return nil
	}

	body := data.Verified()
	if partial {
		body = data.Range(start, length)
	}
	if _, err := io.Copy(w, body); err != nil {
		if errors.Is(err, storage.ErrCorrupt) {
			h.opts.Log.Error("corrupt object: SHA-256 mismatch, response aborted",
				"id", server.RequestID(r.Context()), "bucket", info.Bucket, "key", info.Key)
		}
		// The status line is already sent: the only way to signal the failure
		// is to cut the connection before the body is complete.
		panic(http.ErrAbortHandler)
	}
	return nil
}

// responseOverrides maps GetObject query parameters to the headers they set.
var responseOverrides = map[string]string{
	"response-content-type":        "Content-Type",
	"response-content-language":    "Content-Language",
	"response-expires":             "Expires",
	"response-cache-control":       "Cache-Control",
	"response-content-disposition": "Content-Disposition",
	"response-content-encoding":    "Content-Encoding",
}

// evaluateConditions applies If-Match / If-Unmodified-Since (412) and
// If-None-Match / If-Modified-Since (304) with S3's precedence: a passing
// If-Match overrides If-Unmodified-Since, a failing If-None-Match overrides
// If-Modified-Since. It returns 0 when the request should proceed.
func evaluateConditions(r *http.Request, info storage.ObjectInfo) int {
	mod := info.ModTime.Truncate(time.Second)
	if im := r.Header.Get("If-Match"); im != "" {
		if !etagListMatches(im, info.ETag) {
			return http.StatusPreconditionFailed
		}
	} else if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if t, err := http.ParseTime(ius); err == nil && mod.After(t) {
			return http.StatusPreconditionFailed
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if etagListMatches(inm, info.ETag) {
			return http.StatusNotModified
		}
	} else if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil && !mod.After(t) {
			return http.StatusNotModified
		}
	}
	return 0
}

func etagListMatches(list, etag string) bool {
	for _, v := range strings.Split(list, ",") {
		v = strings.TrimPrefix(strings.TrimSpace(v), "W/")
		if v == "*" || strings.Trim(v, `"`) == etag {
			return true
		}
	}
	return false
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, req Request) error {
	versionID, err := requestedVersion(r)
	if err != nil {
		return err
	}
	res, err := h.opts.Store.DeleteVersion(r.Context(), req.Bucket, req.Key, versionID)
	if err != nil {
		return storageError(err)
	}
	setVersionHeader(w, res.VersionID, versionID != "" || res.DeleteMarker)
	if res.DeleteMarker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// errCopyFromMarker refuses a copy whose source names a delete marker.
var errCopyFromMarker = s3err.ErrInvalidRequest.WithMessage(
	"The source of a copy request may not specifically refer to a delete marker by version id.")

type copyObjectResult struct {
	XMLName           xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CopyObjectResult"`
	ETag              string   `xml:"ETag"`
	LastModified      string   `xml:"LastModified"`
	ChecksumCRC32     string   `xml:"ChecksumCRC32,omitempty"`
	ChecksumCRC32C    string   `xml:"ChecksumCRC32C,omitempty"`
	ChecksumCRC64NVME string   `xml:"ChecksumCRC64NVME,omitempty"`
	ChecksumSHA1      string   `xml:"ChecksumSHA1,omitempty"`
	ChecksumSHA256    string   `xml:"ChecksumSHA256,omitempty"`
}

// parseCopySource reads x-amz-copy-source: "bucket/key" or "/bucket/key",
// URL-encoded, optionally with "?versionId=".
func parseCopySource(v string) (bucket, key, versionID string, err error) {
	src, query, _ := strings.Cut(v, "?")
	if query != "" {
		q, _ := url.ParseQuery(query)
		if versionID = q.Get("versionId"); q.Has("versionId") && !storage.ValidVersionID(versionID) {
			return "", "", "", s3err.ErrInvalidArgument.WithMessage("Invalid version id specified")
		}
	}
	decoded, err := url.PathUnescape(src)
	if err != nil {
		return "", "", "", s3err.ErrInvalidArgument.WithMessage("Copy Source must mention the source bucket and key: sourcebucket/sourcekey")
	}
	bucket, key, _ = strings.Cut(strings.TrimPrefix(decoded, "/"), "/")
	if bucket == "" || key == "" {
		return "", "", "", s3err.ErrInvalidArgument.WithMessage("Copy Source must mention the source bucket and key: sourcebucket/sourcekey")
	}
	return bucket, key, versionID, nil
}

func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, req Request) error {
	srcBucket, srcKey, srcVersion, err := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if err != nil {
		return err
	}
	directive := strings.ToUpper(r.Header.Get("X-Amz-Metadata-Directive"))
	switch directive {
	case "", "COPY", "REPLACE":
	default:
		return s3err.ErrInvalidArgument.WithMessage("Unknown metadata directive.")
	}
	replace := directive == "REPLACE"
	// Copying an older version over its own key is how a version is restored.
	if srcBucket == req.Bucket && srcKey == req.Key && srcVersion == "" && !replace {
		return s3err.ErrInvalidRequest.WithMessage("This copy request is illegal because it is trying to copy an " +
			"object to itself without changing the object's metadata, storage class, website redirect location or encryption attributes.")
	}
	var copiedVersion string
	opts := storage.CopyOptions{SourceVersionID: srcVersion, ReplaceMetadata: replace, Check: func(src storage.ObjectInfo) error {
		if src.Size > maxPutSize {
			return s3err.ErrInvalidRequest.WithMessage("The specified copy source is larger than the maximum allowable size for a copy source: 5368709120")
		}
		copiedVersion = src.VersionID
		return copySourceConditions(r, src)
	}}
	if replace {
		if opts.ContentType, opts.Headers, opts.UserMeta, err = objectMetadata(r); err != nil {
			return err
		}
	}
	if v := r.Header.Get("X-Amz-Checksum-Algorithm"); v != "" {
		a, ok := checksum.Parse(v)
		if !ok {
			return s3err.ErrInvalidRequest.WithMessage("Checksum algorithm provided is unsupported.")
		}
		opts.Checksums = []checksum.Algorithm{a}
	}

	info, err := h.opts.Store.CopyObject(r.Context(), srcBucket, srcKey, req.Bucket, req.Key, opts)
	if err != nil {
		if errors.Is(err, storage.ErrCorrupt) {
			h.opts.Log.Error("corrupt copy source: SHA-256 mismatch", "id", server.RequestID(r.Context()),
				"bucket", srcBucket, "key", srcKey)
		}
		if errors.Is(err, storage.ErrDeleteMarker) {
			return errCopyFromMarker
		}
		return storageError(err)
	}
	if copiedVersion != "" && (copiedVersion != storage.NullVersion || srcVersion != "") {
		w.Header().Set("X-Amz-Copy-Source-Version-Id", copiedVersion)
	}
	setVersionHeader(w, info.VersionID, false)
	res := copyObjectResult{ETag: quoteETag(info.ETag), LastModified: timeXML(info.ModTime)}
	for a, v := range info.Checksums {
		switch a {
		case checksum.CRC32:
			res.ChecksumCRC32 = v
		case checksum.CRC32C:
			res.ChecksumCRC32C = v
		case checksum.CRC64NVME:
			res.ChecksumCRC64NVME = v
		case checksum.SHA1:
			res.ChecksumSHA1 = v
		case checksum.SHA256:
			res.ChecksumSHA256 = v
		}
	}
	return writeXML(w, http.StatusOK, res)
}

// copySourceConditions evaluates x-amz-copy-source-if-*; any failure is a 412.
func copySourceConditions(r *http.Request, src storage.ObjectInfo) error {
	mod := src.ModTime.Truncate(time.Second)
	if v := r.Header.Get("X-Amz-Copy-Source-If-Match"); v != "" && !etagListMatches(v, src.ETag) {
		return s3err.ErrPreconditionFailed
	}
	if v := r.Header.Get("X-Amz-Copy-Source-If-None-Match"); v != "" && etagListMatches(v, src.ETag) {
		return s3err.ErrPreconditionFailed
	}
	if v := r.Header.Get("X-Amz-Copy-Source-If-Unmodified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && mod.After(t) && r.Header.Get("X-Amz-Copy-Source-If-Match") == "" {
			return s3err.ErrPreconditionFailed
		}
	}
	if v := r.Header.Get("X-Amz-Copy-Source-If-Modified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && !mod.After(t) && r.Header.Get("X-Amz-Copy-Source-If-None-Match") == "" {
			return s3err.ErrPreconditionFailed
		}
	}
	return nil
}
