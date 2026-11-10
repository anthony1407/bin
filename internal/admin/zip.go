package admin

import (
	"archive/zip"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"bin/internal/storage"
)

// Downloading a selection as one ZIP: the console posts the selection, gets
// a short-lived ticket, and navigates to the ticket's URL, so the browser
// saves the archive as the server streams it instead of building it in
// memory. Entries are stored, not compressed: most large files (media,
// archives) do not shrink, and storing keeps the download at disk speed.

// zipTicketTTL is how long a ticket can start a download.
const zipTicketTTL = 5 * time.Minute

type zipTicket struct {
	user     string
	bucket   string
	base     string // folder the selection was made in, left out of entry names
	keys     []string
	prefixes []string
	expires  time.Time
}

type zipTickets struct {
	mu sync.Mutex
	m  map[string]zipTicket
}

func newZipTickets() *zipTickets { return &zipTickets{m: map[string]zipTicket{}} }

func (t *zipTickets) add(z zipTicket) string {
	id := randomToken()
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	t.m[id] = z
	return id
}

func (t *zipTickets) get(id string) (zipTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	z, ok := t.m[id]
	if !ok || time.Now().After(z.expires) {
		return zipTicket{}, false
	}
	return z, true
}

// createZip registers a selection (files and folders) for download as a ZIP
// and returns the URL that streams it.
func (h *Handler) createZip(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys     []string `json:"keys"`
		Prefixes []string `json:"prefixes"`
		Base     string   `json:"base"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Keys)+len(in.Prefixes) == 0 {
		writeError(w, http.StatusBadRequest, "empty_selection", "Select at least one file or folder.")
		return
	}
	for _, p := range in.Prefixes {
		if !strings.HasSuffix(p, "/") {
			writeError(w, http.StatusBadRequest, "invalid_prefix", "Folders must end with /.")
			return
		}
	}
	bucket := r.PathValue("bucket")
	if _, err := h.opts.Store.GetBucket(r.Context(), bucket); err != nil {
		h.storageError(w, r, err)
		return
	}
	id := h.zips.add(zipTicket{
		user: sessionUser(r), bucket: bucket, base: in.Base, keys: in.Keys, prefixes: in.Prefixes,
		expires: time.Now().Add(zipTicketTTL),
	})
	writeJSON(w, http.StatusCreated, map[string]string{"url": "/api/v1/zip/" + id})
}

// zipEntryName is key's name inside the archive: relative to the folder the
// selection was made in, and never able to climb out of the folder the
// archive is extracted to.
func zipEntryName(key, base string) string {
	name := strings.TrimPrefix(key, base)
	dir := strings.HasSuffix(name, "/")
	name = strings.TrimLeft(path.Clean("/"+name), "/")
	if dir && name != "" {
		name += "/"
	}
	return name
}

// downloadZip streams a registered selection as a ZIP archive.
func (h *Handler) downloadZip(w http.ResponseWriter, r *http.Request) {
	t, ok := h.zips.get(r.PathValue("id"))
	if !ok || t.user != sessionUser(r) {
		writeError(w, http.StatusNotFound, "no_such_download", "This download link expired; start the download again.")
		return
	}
	ctx := r.Context()
	// A file selected along with its folder goes in once.
	var keys []string
	seen := map[string]bool{}
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range t.keys {
		add(k)
	}
	for _, p := range t.prefixes {
		objs, err := h.listUnder(ctx, t.bucket, p)
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		for _, o := range objs {
			add(o.Key)
		}
	}

	hdr := w.Header()
	hdr.Set("Content-Type", "application/zip")
	hdr.Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": t.bucket + "-" + time.Now().UTC().Format("20060102-150405") + ".zip"}))
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(w)
	for _, key := range keys {
		name := zipEntryName(key, t.base)
		if name == "" {
			continue // the folder the selection was made in
		}
		if strings.HasSuffix(name, "/") { // folder marker: keeps empty folders
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store}); err != nil {
				panic(http.ErrAbortHandler)
			}
			continue
		}
		info, data, err := h.opts.Store.OpenObject(ctx, t.bucket, key)
		if errors.Is(err, storage.ErrNoSuchKey) {
			continue // deleted since it was selected
		}
		if err != nil {
			h.opts.Log.Error("zip download: opening an object", "bucket", t.bucket, "key", key, "err", err)
			panic(http.ErrAbortHandler)
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: info.ModTime})
		if err == nil {
			_, err = io.Copy(fw, data.Reader())
		}
		data.Close()
		if err != nil {
			if errors.Is(err, storage.ErrCorrupt) {
				h.opts.Log.Error("corrupt object: SHA-256 mismatch, zip download aborted", "bucket", t.bucket, "key", key)
			}
			panic(http.ErrAbortHandler) // the client sees a broken download, not a silently incomplete one
		}
	}
	if err := zw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}
