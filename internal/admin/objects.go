package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"bin/internal/httprange"
	"bin/internal/s3/sigv4"
	"bin/internal/server"
	"bin/internal/storage"
)

// maxSingleUpload is the largest body accepted by the single-request upload;
// larger files go through the multipart endpoints.
const maxSingleUpload = 5 << 30

type objectJSON struct {
	Key string `json:"key"`
	// VersionID is left out for objects of buckets that were never
	// versioned; in a listing of deleted objects it names the delete marker.
	VersionID    string            `json:"versionId,omitempty"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"`
	LastModified time.Time         `json:"lastModified"`
	ContentType  string            `json:"contentType,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Checksums    map[string]string `json:"checksums,omitempty"`
	PartsCount   int               `json:"partsCount,omitempty"`
	// Active share links: how many, and until when the last one works.
	Shares         int        `json:"shares,omitempty"`
	ShareExpiresAt *time.Time `json:"shareExpiresAt,omitempty"`
}

// withShares adds the active share links to each listed object.
func (h *Handler) withShares(r *http.Request, bucket string, objs []objectJSON) []objectJSON {
	sums, err := h.opts.Store.ShareSummaries(r.Context(), bucket)
	if err != nil {
		h.opts.Log.Warn("share summaries unavailable", "bucket", bucket, "err", err)
		return objs
	}
	for i := range objs {
		if s, ok := sums[objs[i].Key]; ok {
			objs[i].Shares = s.Active
			objs[i].ShareExpiresAt = optionalTime(s.LastExpires)
		}
	}
	return objs
}

func toObjectJSON(o storage.ObjectInfo) objectJSON {
	j := objectJSON{
		Key: o.Key, VersionID: o.VersionID, Size: o.Size, ETag: o.ETag, LastModified: o.ModTime, ContentType: o.ContentType,
		Headers: o.Headers, Metadata: o.UserMeta, PartsCount: o.PartsCount,
	}
	if j.VersionID == storage.NullVersion {
		j.VersionID = ""
	}
	if len(o.Checksums) > 0 {
		j.Checksums = map[string]string{}
		for a, v := range o.Checksums {
			j.Checksums[string(a)] = v
		}
	}
	return j
}

// listObjects lists a "folder" (prefix + delimiter), or searches under the
// prefix when ?search= is set. With ?deleted=true it lists the objects of the
// folder whose latest version is a delete marker.
// folderJSON is what a listed folder (common prefix) holds.
type folderJSON struct {
	Objects      int64      `json:"objects"`
	Size         int64      `json:"size"`
	LastModified *time.Time `json:"lastModified"`
}

func (h *Handler) listObjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket, prefix := r.PathValue("bucket"), q.Get("prefix")
	limit := 200
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive number.")
			return
		}
		limit = min(n, 1000)
	}
	if term := q.Get("search"); term != "" {
		found, err := h.opts.Store.SearchObjects(r.Context(), bucket, prefix, term, limit+1)
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		out := make([]objectJSON, 0, len(found))
		for i, o := range found {
			if i < limit {
				out = append(out, toObjectJSON(o))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"objects": h.withShares(r, bucket, out), "prefixes": []string{}, "truncated": len(found) > limit})
		return
	}
	delimiter := "/"
	if q.Has("delimiter") {
		delimiter = q.Get("delimiter")
	}
	res, err := h.opts.Store.ListObjects(r.Context(), bucket, storage.ListOptions{
		Prefix: prefix, Delimiter: delimiter, After: q.Get("after"), MaxKeys: limit, Deleted: q.Get("deleted") == "true",
	})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]objectJSON, 0, len(res.Objects))
	for _, o := range res.Objects {
		out = append(out, toObjectJSON(o))
	}
	prefixes := res.CommonPrefixes
	if prefixes == nil {
		prefixes = []string{}
	}
	// Folders show what they hold: object count, total size and last change.
	usage, err := h.opts.Store.PrefixUsage(r.Context(), bucket, prefixes)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	folders := make(map[string]folderJSON, len(usage))
	for p, u := range usage {
		folders[p] = folderJSON{Objects: u.Objects, Size: u.Bytes, LastModified: optionalTime(u.Modified)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"objects": h.withShares(r, bucket, out), "prefixes": prefixes, "folders": folders,
		"truncated": res.IsTruncated, "nextAfter": res.NextMarker,
	})
}

// statObject returns an object's metadata: its current version, or the one
// named by ?versionId=.
func (h *Handler) statObject(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	info, err := h.opts.Store.StatVersion(r.Context(), r.PathValue("bucket"), q.Get("key"), q.Get("versionId"))
	if errors.Is(err, storage.ErrDeleteMarker) && !q.Has("versionId") {
		err = storage.ErrNoSuchKey
	}
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toObjectJSON(info))
}

// download streams an object (or the version named by ?versionId=) for saving
// (attachment) or previewing (?inline=1). Every response is sandboxed by CSP:
// the console origin also serves the admin API, and an uploaded HTML or SVG
// file must never run scripts in it.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := q.Get("key")
	info, data, err := h.opts.Store.OpenVersion(r.Context(), r.PathValue("bucket"), key, q.Get("versionId"))
	if errors.Is(err, storage.ErrDeleteMarker) && !q.Has("versionId") {
		err = storage.ErrNoSuchKey
	}
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	defer data.Close()

	hdr := w.Header()
	hdr.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "private, no-cache")
	hdr.Set("Content-Type", info.ContentType)
	hdr.Set("ETag", `"`+info.ETag+`"`)
	hdr.Set("Last-Modified", info.ModTime.Format(http.TimeFormat))
	hdr.Set("Accept-Ranges", "bytes")
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	hdr.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": path.Base(key)}))

	body := data.Verified()
	status, length := http.StatusOK, info.Size
	if rh := r.Header.Get("Range"); rh != "" {
		start, n, ok, satisfiable := httprange.Parse(rh, info.Size)
		if ok && !satisfiable {
			hdr.Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
			writeError(w, http.StatusRequestedRangeNotSatisfiable, "invalid_range", "The requested range is not satisfiable.")
			return
		}
		if ok {
			status, length, body = http.StatusPartialContent, n, data.Range(start, n)
			hdr.Set("Content-Range", httprange.ContentRange(start, n, info.Size))
		}
	}
	hdr.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, body); err != nil {
		if errors.Is(err, storage.ErrCorrupt) {
			h.opts.Log.Error("corrupt object: SHA-256 mismatch, download aborted", "bucket", info.Bucket, "key", key)
		}
		panic(http.ErrAbortHandler)
	}
}

// contentType picks the stored type: the browser's, unless it is the generic
// one, in which case the file extension decides.
func contentType(r *http.Request, key string) string {
	ct := r.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		if byExt := mime.TypeByExtension(path.Ext(key)); byExt != "" {
			return byExt
		}
	}
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

// upload stores the raw request body as one object (up to 5 GiB).
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "length_required", "Content-Length is required.")
		return
	}
	if r.ContentLength > maxSingleUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Files over 5 GiB must use the multipart upload endpoints.")
		return
	}
	info, err := h.opts.Store.PutObject(r.Context(), r.PathValue("bucket"), key, r.Body, r.ContentLength,
		storage.PutOptions{ContentType: contentType(r, key)})
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			err = storage.ErrIncompleteBody
		}
		h.storageError(w, r, err)
		return
	}
	server.Annotate(r.Context(), slog.String("key", key))
	writeJSON(w, http.StatusCreated, toObjectJSON(info))
}

// createFolder stores the empty "prefix/" object S3 tools use as a folder.
func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prefix string `json:"prefix"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.Trim(in.Prefix, "/") == "" {
		writeError(w, http.StatusBadRequest, "invalid_prefix", "Give the folder a name.")
		return
	}
	if !strings.HasSuffix(in.Prefix, "/") {
		in.Prefix += "/"
	}
	info, err := h.opts.Store.PutObject(r.Context(), r.PathValue("bucket"), in.Prefix, strings.NewReader(""), 0,
		storage.PutOptions{ContentType: "application/x-directory", IfNoneMatch: "*"})
	if errors.Is(err, storage.ErrPrecondition) {
		writeError(w, http.StatusConflict, "folder_exists", "The folder already exists.")
		return
	}
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toObjectJSON(info))
}

// deleteObjects deletes the listed keys and everything under the listed
// prefixes (folders).
func (h *Handler) deleteObjects(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys     []string `json:"keys"`
		Prefixes []string `json:"prefixes"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	bucket := r.PathValue("bucket")
	deleted := 0
	for _, k := range in.Keys {
		if err := h.opts.Store.DeleteObject(r.Context(), bucket, k); err != nil {
			h.storageError(w, r, err)
			return
		}
		deleted++
	}
	for _, p := range in.Prefixes {
		if p == "" {
			writeError(w, http.StatusBadRequest, "invalid_prefix", "Deleting a whole bucket's content needs DELETE /buckets/{bucket}?force=true.")
			return
		}
		n, err := h.opts.Store.DeletePrefix(r.Context(), bucket, p)
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		deleted += n
	}
	server.Annotate(r.Context(), slog.Int("deleted", deleted))
	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}

// --- versions ----------------------------------------------------------------------------

// maxVersionsListed bounds the history the console shows for one object.
const maxVersionsListed = 1000

type versionJSON struct {
	VersionID    string    `json:"versionId"`
	IsLatest     bool      `json:"isLatest"`
	DeleteMarker bool      `json:"deleteMarker"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"lastModified"`
}

// listVersions returns the history of one object, newest first.
func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.opts.Store.ObjectVersions(r.Context(), r.PathValue("bucket"), r.URL.Query().Get("key"), maxVersionsListed+1)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]versionJSON, 0, len(versions))
	for i, v := range versions {
		if i == maxVersionsListed {
			break
		}
		out = append(out, versionJSON{VersionID: v.VersionID, IsLatest: v.IsLatest, DeleteMarker: v.DeleteMarker,
			Size: v.Size, ETag: v.ETag, LastModified: v.ModTime})
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out, "truncated": len(versions) > maxVersionsListed})
}

// deleteVersion removes one version of an object for good. Removing the
// delete marker that hides an object brings the object back.
func (h *Handler) deleteVersion(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key, versionID := q.Get("key"), q.Get("versionId")
	if versionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_version", "Name the version to delete with versionId.")
		return
	}
	setAuditResource(r, key+" (version "+versionID+")")
	res, err := h.opts.Store.DeleteVersion(r.Context(), r.PathValue("bucket"), key, versionID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	server.Annotate(r.Context(), slog.String("key", key), slog.String("version", versionID))
	writeJSON(w, http.StatusOK, map[string]any{"versionId": res.VersionID, "deleteMarker": res.DeleteMarker})
}

// restoreVersion makes an older version current again by copying it over the
// object, as S3 clients do; the history keeps every version.
func (h *Handler) restoreVersion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key       string `json:"key"`
		VersionID string `json:"versionId"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.VersionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_version", "Name the version to restore.")
		return
	}
	bucket := r.PathValue("bucket")
	setAuditResource(r, in.Key+" (version "+in.VersionID+")")
	info, err := h.opts.Store.CopyObject(r.Context(), bucket, in.Key, bucket, in.Key, storage.CopyOptions{SourceVersionID: in.VersionID})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	server.Annotate(r.Context(), slog.String("key", in.Key), slog.String("version", in.VersionID))
	writeJSON(w, http.StatusOK, toObjectJSON(info))
}

// share returns a presigned GET URL for an object, signed with the internal
// read-only share key and limited by the configured maximum expiry.
func (h *Handler) share(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key       string `json:"key"`
		ExpiresIn int64  `json:"expiresIn"` // seconds
		Download  bool   `json:"download"`  // force "save as" instead of opening
	}
	if !readJSON(w, r, &in) {
		return
	}
	bucket := r.PathValue("bucket")
	if _, err := h.opts.Store.StatObject(r.Context(), bucket, in.Key); err != nil {
		h.storageError(w, r, err)
		return
	}
	maxExpiry, err := h.shareMaxExpiry(r)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	expires := time.Duration(in.ExpiresIn) * time.Second
	if expires <= 0 || expires > maxExpiry {
		writeError(w, http.StatusBadRequest, "invalid_expiry",
			"expiresIn must be between 1 second and the maximum set in the settings ("+maxExpiry.String()+").")
		return
	}
	ak, sk, err := h.opts.Keys.ShareCredentials(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	target := h.s3BaseURL(r) + "/" + bucket + "/" + (&url.URL{Path: in.Key}).EscapedPath()
	if in.Download {
		target += "?response-content-disposition=" +
			url.QueryEscape(mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(in.Key)}))
	}
	now := time.Now()
	signed, err := sigv4.Presign(http.MethodGet, target, ak, sk, h.opts.Region, expires, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_expiry", err.Error())
		return
	}
	link := storage.ShareLink{
		ID: "sl_" + randomToken()[:12], Bucket: bucket, Key: in.Key, Download: in.Download,
		SignatureHash: signatureHash(signed), URLEnc: h.opts.Keys.Box.Encrypt(signed),
		CreatedBy: sessionUser(r), Expires: now.Add(expires),
	}
	if err := h.opts.Store.CreateShareLink(r.Context(), link); err != nil {
		h.storageError(w, r, err)
		return
	}
	setAuditResource(r, in.Key)
	h.opts.Log.Info("share link created", "bucket", bucket, "key", in.Key, "expires_in", expires.String())
	writeJSON(w, http.StatusOK, map[string]any{"id": link.ID, "url": signed, "expiresAt": link.Expires.UTC()})
}

// signatureHash identifies a presigned URL by its signature, which the S3
// API sees on every use.
func signatureHash(signedURL string) string {
	u, err := url.Parse(signedURL)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(u.Query().Get("X-Amz-Signature")))
	return hex.EncodeToString(sum[:])
}

func sessionUser(r *http.Request) string {
	if s, ok := r.Context().Value(sessionKey{}).(storage.Session); ok {
		return s.User
	}
	return ""
}

type shareLinkJSON struct {
	ID        string    `json:"id"`
	URL       string    `json:"url,omitempty"`
	Download  bool      `json:"download"`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// listShares returns the active share links of one object (?key=).
func (h *Handler) listShares(w http.ResponseWriter, r *http.Request) {
	links, err := h.opts.Store.ActiveShareLinks(r.Context(), r.PathValue("bucket"), r.URL.Query().Get("key"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]shareLinkJSON, 0, len(links))
	for _, l := range links {
		j := shareLinkJSON{ID: l.ID, Download: l.Download, CreatedBy: l.CreatedBy, CreatedAt: l.Created, ExpiresAt: l.Expires}
		if u, err := h.opts.Keys.Box.Decrypt(l.URLEnc); err == nil {
			j.URL = u
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// revokeShare stops one share link at once; the others keep working.
func (h *Handler) revokeShare(w http.ResponseWriter, r *http.Request) {
	l, err := h.opts.Store.RevokeShareLink(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	setAuditBucket(r, l.Bucket)
	setAuditResource(r, l.Key)
	h.opts.Log.Info("share link revoked", "bucket", l.Bucket, "key", l.Key, "id", l.ID)
	w.WriteHeader(http.StatusNoContent)
}

// s3BaseURL is where clients reach the S3 API: BIN_PUBLIC_URL when set,
// otherwise the host the console was reached on, with the S3 port.
func (h *Handler) s3BaseURL(r *http.Request) string {
	if h.opts.PublicURL != "" {
		return strings.TrimRight(h.opts.PublicURL, "/")
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	_, port, err := net.SplitHostPort(h.opts.S3Addr)
	if err != nil || port == "" {
		port = "9000"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// --- multipart uploads for large files ----------------------------------------------

func (h *Handler) createUpload(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	u, err := h.opts.Store.CreateUpload(r.Context(), r.PathValue("bucket"), key,
		storage.UploadOptions{ContentType: contentType(r, key)})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"uploadId": u.ID, "key": key})
}

func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 || n > storage.MaxPartNumber {
		writeError(w, http.StatusBadRequest, "invalid_part_number", "Part numbers go from 1 to 10000.")
		return
	}
	if r.ContentLength < 0 || r.ContentLength > maxSingleUpload {
		writeError(w, http.StatusBadRequest, "invalid_part_size", "Parts need a Content-Length of at most 5 GiB.")
		return
	}
	part, err := h.opts.Store.PutPart(r.Context(), r.PathValue("bucket"), r.URL.Query().Get("key"),
		r.PathValue("id"), n, r.Body, r.ContentLength, storage.PartOptions{})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"number": part.Number, "etag": part.ETag, "size": part.Size})
}

func (h *Handler) completeUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Parts []struct {
			Number int    `json:"number"`
			ETag   string `json:"etag"`
		} `json:"parts"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	opts := storage.CompleteOptions{ObjectSize: -1}
	for _, p := range in.Parts {
		opts.Parts = append(opts.Parts, storage.CompletePart{Number: p.Number, ETag: p.ETag})
	}
	info, err := h.opts.Store.CompleteUpload(r.Context(), r.PathValue("bucket"), r.URL.Query().Get("key"), r.PathValue("id"), opts)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toObjectJSON(info))
}

func (h *Handler) abortUpload(w http.ResponseWriter, r *http.Request) {
	if err := h.opts.Store.AbortUpload(r.Context(), r.PathValue("bucket"), r.URL.Query().Get("key"), r.PathValue("id")); err != nil {
		h.storageError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
