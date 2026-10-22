package s3

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"bin/internal/checksum"
	"bin/internal/s3/s3err"
	"bin/internal/storage"
)

// checksumFieldsXML carries per-algorithm checksum elements; S3 uses the
// same field names in several multipart documents.
type checksumFieldsXML struct {
	ChecksumCRC32     string `xml:"ChecksumCRC32,omitempty"`
	ChecksumCRC32C    string `xml:"ChecksumCRC32C,omitempty"`
	ChecksumCRC64NVME string `xml:"ChecksumCRC64NVME,omitempty"`
	ChecksumSHA1      string `xml:"ChecksumSHA1,omitempty"`
	ChecksumSHA256    string `xml:"ChecksumSHA256,omitempty"`
}

func checksumFields(sums map[checksum.Algorithm]string) checksumFieldsXML {
	return checksumFieldsXML{
		ChecksumCRC32:     sums[checksum.CRC32],
		ChecksumCRC32C:    sums[checksum.CRC32C],
		ChecksumCRC64NVME: sums[checksum.CRC64NVME],
		ChecksumSHA1:      sums[checksum.SHA1],
		ChecksumSHA256:    sums[checksum.SHA256],
	}
}

func (c checksumFieldsXML) toMap() map[checksum.Algorithm]string {
	m := map[checksum.Algorithm]string{}
	for a, v := range map[checksum.Algorithm]string{
		checksum.CRC32: c.ChecksumCRC32, checksum.CRC32C: c.ChecksumCRC32C, checksum.CRC64NVME: c.ChecksumCRC64NVME,
		checksum.SHA1: c.ChecksumSHA1, checksum.SHA256: c.ChecksumSHA256,
	} {
		if v != "" {
			m[a] = v
		}
	}
	return m
}

func partNumber(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
	if err != nil || n < 1 || n > storage.MaxPartNumber {
		return 0, s3err.ErrInvalidArgument.WithMessage("Part number must be an integer between 1 and 10000, inclusive")
	}
	return n, nil
}

type initiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

func (h *Handler) createMultipartUpload(w http.ResponseWriter, r *http.Request, req Request) error {
	for _, name := range unsupportedPutHeaders {
		if r.Header.Get(name) != "" {
			return s3err.ErrNotImplemented.WithMessage(name + " is not supported.")
		}
	}
	contentType, headers, meta, err := objectMetadata(r)
	if err != nil {
		return err
	}
	opts := storage.UploadOptions{ContentType: contentType, Headers: headers, UserMeta: meta}
	if v := r.Header.Get("X-Amz-Checksum-Algorithm"); v != "" {
		a, ok := checksum.Parse(v)
		if !ok {
			return s3err.ErrInvalidRequest.WithMessage("Checksum algorithm provided is unsupported.")
		}
		opts.ChecksumAlgorithm = a
	}
	switch t := strings.ToUpper(r.Header.Get("X-Amz-Checksum-Type")); t {
	case "", "FULL_OBJECT", "COMPOSITE":
		if t != "" && opts.ChecksumAlgorithm == "" {
			return s3err.ErrInvalidRequest.WithMessage("x-amz-checksum-type requires x-amz-checksum-algorithm.")
		}
		opts.ChecksumType = t
	default:
		return s3err.ErrInvalidRequest.WithMessage("Value for x-amz-checksum-type header is invalid.")
	}
	u, err := h.opts.Store.CreateUpload(r.Context(), req.Bucket, req.Key, opts)
	if err != nil {
		return storageError(err)
	}
	if u.ChecksumAlgorithm != "" {
		w.Header().Set("X-Amz-Checksum-Algorithm", string(u.ChecksumAlgorithm))
		w.Header().Set("X-Amz-Checksum-Type", u.ChecksumType)
	}
	return writeXML(w, http.StatusOK, initiateMultipartUploadResult{Bucket: req.Bucket, Key: req.Key, UploadID: u.ID})
}

func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request, req Request) error {
	n, err := partNumber(r)
	if err != nil {
		return err
	}
	if r.ContentLength < 0 {
		return s3err.ErrMissingContentLength
	}
	if r.ContentLength > maxPutSize {
		return s3err.ErrEntityTooLarge
	}
	sums, err := requestedChecksums(r)
	if err != nil {
		return err
	}
	part, err := h.opts.Store.PutPart(r.Context(), req.Bucket, req.Key, r.URL.Query().Get("uploadId"), n,
		r.Body, r.ContentLength, storage.PartOptions{
			Checksums: sums.algs,
			Verify:    func(s storage.Sums) error { return sums.verify(r, s) },
		})
	if err != nil {
		return storageError(err)
	}
	w.Header().Set("ETag", quoteETag(part.ETag))
	for a, v := range part.Checksums {
		w.Header().Set(a.Header(), v)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

type copyPartResult struct {
	XMLName      xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CopyPartResult"`
	ETag         string   `xml:"ETag"`
	LastModified string   `xml:"LastModified"`
	checksumFieldsXML
}

func (h *Handler) uploadPartCopy(w http.ResponseWriter, r *http.Request, req Request) error {
	n, err := partNumber(r)
	if err != nil {
		return err
	}
	srcBucket, srcKey, err := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if err != nil {
		return err
	}
	opts := storage.CopyPartOptions{Length: -1}
	if v := r.Header.Get("X-Amz-Copy-Source-Range"); v != "" {
		first, last, ok := strings.Cut(strings.TrimPrefix(v, "bytes="), "-")
		a, errA := strconv.ParseInt(first, 10, 64)
		b, errB := strconv.ParseInt(last, 10, 64)
		if !strings.HasPrefix(v, "bytes=") || !ok || errA != nil || errB != nil || a < 0 || b < a {
			return s3err.ErrInvalidArgument.WithMessage("The x-amz-copy-source-range value must be of the form " +
				"bytes=first-last where first and last are the zero-based offsets of the first and last bytes to copy")
		}
		opts.Offset, opts.Length = a, b-a+1
	}
	if opts.Length > maxPutSize {
		return s3err.ErrEntityTooLarge
	}
	opts.Check = func(src storage.ObjectInfo) error {
		if opts.Length < 0 && src.Size > maxPutSize {
			return s3err.ErrInvalidRequest.WithMessage("The specified copy source is larger than the maximum allowable size for a copy source: 5368709120")
		}
		return copySourceConditions(r, src)
	}
	part, err := h.opts.Store.CopyPart(r.Context(), srcBucket, srcKey, req.Bucket, req.Key,
		r.URL.Query().Get("uploadId"), n, opts)
	if err != nil {
		return storageError(err)
	}
	return writeXML(w, http.StatusOK, copyPartResult{
		ETag: quoteETag(part.ETag), LastModified: timeXML(part.ModTime), checksumFieldsXML: checksumFields(part.Checksums),
	})
}

type completeMultipartUpload struct {
	Parts []struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
		checksumFieldsXML
	} `xml:"Part"`
}

type completeMultipartUploadResult struct {
	XMLName  xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
	checksumFieldsXML
	ChecksumType string `xml:"ChecksumType,omitempty"`
}

func (h *Handler) completeMultipartUpload(w http.ResponseWriter, r *http.Request, req Request) error {
	var in completeMultipartUpload
	empty, err := readXMLBody(r, &in)
	if err != nil {
		return err
	}
	if empty || len(in.Parts) == 0 {
		return s3err.ErrMalformedXML
	}
	opts := storage.CompleteOptions{
		ObjectSize:  -1,
		IfMatch:     r.Header.Get("If-Match"),
		IfNoneMatch: r.Header.Get("If-None-Match"),
	}
	for _, p := range in.Parts {
		opts.Parts = append(opts.Parts, storage.CompletePart{Number: p.PartNumber, ETag: p.ETag, Checksums: p.toMap()})
	}
	if v := r.Header.Get("X-Amz-Mp-Object-Size"); v != "" {
		size, err := strconv.ParseInt(v, 10, 64)
		if err != nil || size < 0 {
			return s3err.ErrInvalidArgument.WithMessage("Value for x-amz-mp-object-size header is invalid.")
		}
		opts.ObjectSize = size
	}
	for _, a := range checksum.All {
		if v := r.Header.Get(a.Header()); v != "" {
			opts.FullChecksum = v
		}
	}
	info, err := h.opts.Store.CompleteUpload(r.Context(), req.Bucket, req.Key, r.URL.Query().Get("uploadId"), opts)
	if err != nil {
		return storageError(err)
	}
	setChecksumHeaders(w, info)
	return writeXML(w, http.StatusOK, completeMultipartUploadResult{
		Location:          objectLocation(r, req),
		Bucket:            req.Bucket,
		Key:               req.Key,
		ETag:              quoteETag(info.ETag),
		checksumFieldsXML: checksumFields(info.Checksums),
		ChecksumType:      info.ChecksumType,
	})
}

// objectLocation is the URL S3 reports for a completed object.
func objectLocation(r *http.Request, req Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/" + req.Bucket + "/" + (&url.URL{Path: req.Key}).EscapedPath()
}

func (h *Handler) abortMultipartUpload(w http.ResponseWriter, r *http.Request, req Request) error {
	if err := h.opts.Store.AbortUpload(r.Context(), req.Bucket, req.Key, r.URL.Query().Get("uploadId")); err != nil {
		return storageError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type partXML struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	checksumFieldsXML
}

type listPartsResult struct {
	XMLName              xml.Name  `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListPartsResult"`
	Bucket               string    `xml:"Bucket"`
	Key                  string    `xml:"Key"`
	UploadID             string    `xml:"UploadId"`
	Initiator            ownerXML  `xml:"Initiator"`
	Owner                ownerXML  `xml:"Owner"`
	StorageClass         string    `xml:"StorageClass"`
	PartNumberMarker     int       `xml:"PartNumberMarker"`
	NextPartNumberMarker int       `xml:"NextPartNumberMarker"`
	MaxParts             int       `xml:"MaxParts"`
	IsTruncated          bool      `xml:"IsTruncated"`
	ChecksumAlgorithm    string    `xml:"ChecksumAlgorithm,omitempty"`
	ChecksumType         string    `xml:"ChecksumType,omitempty"`
	Parts                []partXML `xml:"Part"`
}

func (h *Handler) listParts(w http.ResponseWriter, r *http.Request, req Request) error {
	q := r.URL.Query()
	maxParts := 1000
	if v := q.Get("max-parts"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return s3err.ErrInvalidArgument.WithMessage("Argument max-parts must be an integer between 0 and 2147483647")
		}
		maxParts = min(n, 1000)
	}
	marker := 0
	if v := q.Get("part-number-marker"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return s3err.ErrInvalidArgument.WithMessage("Argument part-number-marker must be an integer between 0 and 2147483647")
		}
		marker = n
	}
	u, parts, truncated, err := h.opts.Store.ListParts(r.Context(), req.Bucket, req.Key, q.Get("uploadId"), marker, maxParts)
	if err != nil {
		return storageError(err)
	}
	owner := ownerXML(h.opts.Owner)
	res := listPartsResult{
		Bucket: req.Bucket, Key: req.Key, UploadID: u.ID, Initiator: owner, Owner: owner,
		StorageClass: "STANDARD", PartNumberMarker: marker, MaxParts: maxParts, IsTruncated: truncated,
		ChecksumAlgorithm: string(u.ChecksumAlgorithm), ChecksumType: u.ChecksumType,
	}
	for _, p := range parts {
		res.Parts = append(res.Parts, partXML{
			PartNumber: p.Number, LastModified: timeXML(p.ModTime), ETag: quoteETag(p.ETag), Size: p.Size,
			checksumFieldsXML: checksumFields(p.Checksums),
		})
		res.NextPartNumberMarker = p.Number
	}
	return writeXML(w, http.StatusOK, res)
}

type uploadXML struct {
	Key               string   `xml:"Key"`
	UploadID          string   `xml:"UploadId"`
	Initiator         ownerXML `xml:"Initiator"`
	Owner             ownerXML `xml:"Owner"`
	StorageClass      string   `xml:"StorageClass"`
	Initiated         string   `xml:"Initiated"`
	ChecksumAlgorithm string   `xml:"ChecksumAlgorithm,omitempty"`
	ChecksumType      string   `xml:"ChecksumType,omitempty"`
}

type listMultipartUploadsResult struct {
	XMLName            xml.Name          `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListMultipartUploadsResult"`
	Bucket             string            `xml:"Bucket"`
	KeyMarker          string            `xml:"KeyMarker"`
	UploadIDMarker     string            `xml:"UploadIdMarker"`
	NextKeyMarker      string            `xml:"NextKeyMarker,omitempty"`
	NextUploadIDMarker string            `xml:"NextUploadIdMarker,omitempty"`
	Prefix             string            `xml:"Prefix"`
	Delimiter          string            `xml:"Delimiter,omitempty"`
	MaxUploads         int               `xml:"MaxUploads"`
	EncodingType       string            `xml:"EncodingType,omitempty"`
	IsTruncated        bool              `xml:"IsTruncated"`
	Uploads            []uploadXML       `xml:"Upload"`
	CommonPrefixes     []commonPrefixXML `xml:"CommonPrefixes"`
}

func (h *Handler) listMultipartUploads(w http.ResponseWriter, r *http.Request, req Request) error {
	q := r.URL.Query()
	prefix, delimiter, _, encode, err := listParams(r)
	if err != nil {
		return err
	}
	maxUploads := 1000
	if v := q.Get("max-uploads"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return s3err.ErrInvalidArgument.WithMessage("Argument max-uploads must be an integer between 0 and 2147483647")
		}
		maxUploads = min(n, 1000)
	}
	list, err := h.opts.Store.ListUploads(r.Context(), req.Bucket, storage.UploadListOptions{
		Prefix: prefix, Delimiter: delimiter, KeyMarker: q.Get("key-marker"),
		UploadIDMarker: q.Get("upload-id-marker"), MaxUploads: maxUploads,
	})
	if err != nil {
		return storageError(err)
	}
	owner := ownerXML(h.opts.Owner)
	res := listMultipartUploadsResult{
		Bucket: req.Bucket, KeyMarker: encode(q.Get("key-marker")), UploadIDMarker: q.Get("upload-id-marker"),
		NextKeyMarker: encode(list.NextKeyMarker), NextUploadIDMarker: list.NextUploadIDMarker,
		Prefix: encode(prefix), Delimiter: encode(delimiter), MaxUploads: maxUploads, IsTruncated: list.IsTruncated,
		CommonPrefixes: prefixesXML(list.CommonPrefixes, encode),
	}
	if q.Get("encoding-type") != "" {
		res.EncodingType = "url"
	}
	for _, u := range list.Uploads {
		res.Uploads = append(res.Uploads, uploadXML{
			Key: encode(u.Key), UploadID: u.ID, Initiator: owner, Owner: owner, StorageClass: "STANDARD",
			Initiated: timeXML(u.Created), ChecksumAlgorithm: string(u.ChecksumAlgorithm), ChecksumType: u.ChecksumType,
		})
	}
	return writeXML(w, http.StatusOK, res)
}

// --- DeleteObjects ---------------------------------------------------------------

type deleteRequest struct {
	Quiet   bool `xml:"Quiet"`
	Objects []struct {
		Key       string `xml:"Key"`
		VersionID string `xml:"VersionId"`
	} `xml:"Object"`
}

type deletedXML struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId,omitempty"`
}

type deleteErrorXML struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId,omitempty"`
	Code      string `xml:"Code"`
	Message   string `xml:"Message"`
}

type deleteResult struct {
	XMLName xml.Name         `xml:"http://s3.amazonaws.com/doc/2006-03-01/ DeleteResult"`
	Deleted []deletedXML     `xml:"Deleted"`
	Errors  []deleteErrorXML `xml:"Error"`
}

// deleteObjects deletes up to 1000 keys in one request, reporting each
// outcome. A key that does not exist counts as deleted, as with DeleteObject.
func (h *Handler) deleteObjects(w http.ResponseWriter, r *http.Request, req Request) error {
	var in deleteRequest
	empty, err := readXMLBody(r, &in)
	if err != nil {
		return err
	}
	if empty || len(in.Objects) == 0 || len(in.Objects) > 1000 {
		return s3err.ErrMalformedXML
	}
	if _, err := h.opts.Store.GetBucket(r.Context(), req.Bucket); err != nil {
		return storageError(err)
	}
	var res deleteResult
	for _, o := range in.Objects {
		if o.VersionID != "" && o.VersionID != "null" {
			res.Errors = append(res.Errors, deleteErrorXML{Key: o.Key, VersionID: o.VersionID,
				Code: "NotImplemented", Message: "Deleting specific object versions is not supported yet."})
			continue
		}
		if err := h.opts.Store.DeleteObject(r.Context(), req.Bucket, o.Key); err != nil {
			apiErr, ok := storageError(err).(*s3err.Error)
			if !ok {
				h.opts.Log.Error("delete object failed", "bucket", req.Bucket, "key", o.Key, "err", err)
				apiErr = s3err.ErrInternalError
			}
			res.Errors = append(res.Errors, deleteErrorXML{Key: o.Key, VersionID: o.VersionID, Code: apiErr.Code, Message: apiErr.Message})
			continue
		}
		if !in.Quiet {
			res.Deleted = append(res.Deleted, deletedXML{Key: o.Key, VersionID: o.VersionID})
		}
	}
	return writeXML(w, http.StatusOK, res)
}
