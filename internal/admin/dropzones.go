package admin

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"bin/internal/storage"
)

// Dropzones are public upload links: people without an account can upload
// files into one bucket prefix until the link expires. The token travels in
// the X-Dropzone-Token header, never in a URL the server logs; the page link
// carries it after "#", which browsers do not send.

const (
	dropzoneTokenHeader = "X-Dropzone-Token"
	maxDropzoneExpiry   = 30 * 24 * time.Hour
	maxDropzoneNote     = 500
	maxDropzonePrefix   = 512
	maxDropzoneName     = 255
	// maxNameAttempts bounds the "name (n).ext" variants tried when a file
	// with the uploaded name already exists; uploads never overwrite.
	maxNameAttempts = 100
)

type dropzoneJSON struct {
	ID        string    `json:"id"`
	Bucket    string    `json:"bucket"`
	Prefix    string    `json:"prefix"`
	Note      string    `json:"note"`
	MaxBytes  int64     `json:"maxBytes"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Expired   bool      `json:"expired"`
	URL       string    `json:"url,omitempty"`
}

func (h *Handler) toDropzoneJSON(r *http.Request, d storage.Dropzone) dropzoneJSON {
	j := dropzoneJSON{
		ID: d.ID, Bucket: d.Bucket, Prefix: d.Prefix, Note: d.Note, MaxBytes: d.MaxBytes,
		CreatedAt: d.Created, ExpiresAt: d.Expires, Expired: !d.Expires.After(time.Now()),
	}
	if token, err := h.opts.Keys.Box.Decrypt(d.TokenEnc); err == nil {
		j.URL = consoleBaseURL(r) + "/upload#" + token
	}
	return j
}

// consoleBaseURL is the console as the browser reached it.
func consoleBaseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *Handler) listDropzones(w http.ResponseWriter, r *http.Request) {
	list, err := h.opts.Store.ListDropzones(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]dropzoneJSON, 0, len(list))
	for _, d := range list {
		out = append(out, h.toDropzoneJSON(r, d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"dropzones": out})
}

func (h *Handler) createDropzone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Bucket    string `json:"bucket"`
		Prefix    string `json:"prefix"`
		Note      string `json:"note"`
		ExpiresIn int64  `json:"expiresIn"` // seconds
		MaxBytes  int64  `json:"maxBytes"`  // per file
	}
	if !readJSON(w, r, &in) {
		return
	}
	setAuditBucket(r, in.Bucket)
	prefix := strings.TrimLeft(strings.TrimSpace(in.Prefix), "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	expires := time.Duration(in.ExpiresIn) * time.Second
	switch {
	case len(prefix) > maxDropzonePrefix || !utf8.ValidString(prefix):
		writeError(w, http.StatusBadRequest, "invalid_prefix", "The folder must be valid text of at most 512 bytes.")
		return
	case utf8.RuneCountInString(in.Note) > maxDropzoneNote:
		writeError(w, http.StatusBadRequest, "invalid_note", "The note is limited to 500 characters.")
		return
	case expires < time.Minute || expires > maxDropzoneExpiry:
		writeError(w, http.StatusBadRequest, "invalid_expiry", "expiresIn must be between 1 minute and 30 days.")
		return
	case in.MaxBytes < 1 || in.MaxBytes > maxSingleUpload:
		writeError(w, http.StatusBadRequest, "invalid_max_bytes", "maxBytes must be between 1 byte and 5 GiB.")
		return
	}

	token := randomToken()
	d := storage.Dropzone{
		ID:        "dz_" + randomToken()[:12],
		TokenHash: hashToken(token),
		TokenEnc:  h.opts.Keys.Box.Encrypt(token),
		Bucket:    in.Bucket,
		Prefix:    prefix,
		Note:      strings.TrimSpace(in.Note),
		MaxBytes:  in.MaxBytes,
		Expires:   time.Now().Add(expires),
	}
	setAuditResource(r, d.ID)
	if err := h.opts.Store.CreateDropzone(r.Context(), d); err != nil {
		h.storageError(w, r, err)
		return
	}
	d.Created = time.Now()
	h.opts.Log.Info("dropzone created", "id", d.ID, "bucket", d.Bucket, "prefix", d.Prefix, "expires_in", expires.String())
	writeJSON(w, http.StatusCreated, h.toDropzoneJSON(r, d))
}

func (h *Handler) deleteDropzone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	setAuditResource(r, id)
	if err := h.opts.Store.DeleteDropzone(r.Context(), id); err != nil {
		h.storageError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- public side -----------------------------------------------------------------------

// liveDropzone resolves the request's token, answering 404 when it is missing,
// unknown or expired; the three are not told apart.
func (h *Handler) liveDropzone(w http.ResponseWriter, r *http.Request) (storage.Dropzone, bool) {
	token := r.Header.Get(dropzoneTokenHeader)
	if token != "" {
		d, err := h.opts.Store.DropzoneByToken(r.Context(), hashToken(token))
		if err == nil {
			setAuditActor(r, "dropzone:"+d.ID)
			setAuditBucket(r, d.Bucket)
			return d, true
		}
		if !errors.Is(err, storage.ErrNoSuchDropzone) {
			h.storageError(w, r, err)
			return storage.Dropzone{}, false
		}
	}
	writeError(w, http.StatusNotFound, "no_such_dropzone", "This upload link is invalid or has expired.")
	return storage.Dropzone{}, false
}

// publicDropzone tells the upload page what the link allows. The bucket and
// folder stay private.
func (h *Handler) publicDropzone(w http.ResponseWriter, r *http.Request) {
	d, ok := h.liveDropzone(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"note": d.Note, "maxBytes": d.MaxBytes, "expiresAt": d.Expires})
}

// cleanFileName keeps the last path element of an uploaded name, without
// control characters, or returns "" if nothing usable is left.
func cleanFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "/" || !utf8.ValidString(name) || len(name) > maxDropzoneName {
		return ""
	}
	return name
}

// numberedName turns "report.pdf" into "report (2).pdf".
func numberedName(name string, n int) string {
	if n == 0 {
		return name
	}
	ext := path.Ext(name)
	if ext == name {
		ext = "" // ".env" has no extension to keep apart
	}
	return strings.TrimSuffix(name, ext) + " (" + strconv.Itoa(n) + ")" + ext
}

// publicDropzoneUpload stores the body as one file under the dropzone prefix.
// Existing files are never replaced: a taken name gets a number appended.
func (h *Handler) publicDropzoneUpload(w http.ResponseWriter, r *http.Request) {
	d, ok := h.liveDropzone(w, r)
	if !ok {
		return
	}
	name := cleanFileName(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "The file needs a name of at most 255 bytes.")
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "length_required", "Content-Length is required.")
		return
	}
	if r.ContentLength > d.MaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "The file is larger than this link allows.")
		return
	}

	key := ""
	for n := 0; n < maxNameAttempts; n++ {
		candidate := d.Prefix + numberedName(name, n)
		if _, err := h.opts.Store.StatObject(r.Context(), d.Bucket, candidate); errors.Is(err, storage.ErrNoSuchKey) {
			key = candidate
			break
		} else if err != nil {
			h.storageError(w, r, err)
			return
		}
	}
	if key == "" {
		writeError(w, http.StatusConflict, "name_taken", "Too many files with this name were uploaded; rename the file.")
		return
	}
	setAuditResource(r, key)

	_, err := h.opts.Store.PutObject(r.Context(), d.Bucket, key, r.Body, r.ContentLength,
		storage.PutOptions{ContentType: contentType(r, key), IfNoneMatch: "*"})
	switch {
	case errors.Is(err, storage.ErrPrecondition):
		writeError(w, http.StatusConflict, "name_taken", "A file with this name was uploaded at the same moment; try again.")
		return
	case err != nil:
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": strings.TrimPrefix(key, d.Prefix)})
}
