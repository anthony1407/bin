// Package webui serves the web panel built from web/ by `npm run build`,
// embedded in the binary. Unknown paths fall back to index.html so the
// panel's client-side routes survive a reload.
package webui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Content-Security-Policy for the panel. Inline styles are needed for the
// progress bars React renders with style attributes; scripts are files only.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

type file struct {
	data        []byte
	gz          []byte // nil when compression does not pay off
	etag        string
	contentType string
	immutable   bool // content-hashed asset name
}

// Handler serves the embedded panel.
type Handler struct {
	files map[string]*file // by path without the leading slash
}

// New loads the embedded build, precompressing text files once.
func New() *Handler {
	return newHandler(dist)
}

func newHandler(fsys fs.FS) *Handler {
	h := &Handler{files: map[string]*file{}}
	sub, err := fs.Sub(fsys, "dist")
	if err != nil {
		return h
	}
	fs.WalkDir(sub, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path.Base(name), ".") {
			return nil
		}
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		f := &file{
			data:        data,
			etag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
			contentType: mime.TypeByExtension(path.Ext(name)),
			immutable:   strings.HasPrefix(name, "assets/"),
		}
		if f.contentType == "" {
			f.contentType = http.DetectContentType(data)
		}
		if compressible(f.contentType) && len(data) > 1024 {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			zw.Write(data)
			zw.Close()
			if buf.Len() < len(data)*9/10 {
				f.gz = buf.Bytes()
			}
		}
		h.files[name] = f
		return nil
	})
	return h
}

func compressible(contentType string) bool {
	for _, p := range []string{"text/", "application/javascript", "text/javascript", "application/json", "image/svg+xml"} {
		if strings.HasPrefix(contentType, p) {
			return true
		}
	}
	return false
}

// Built reports whether the binary contains a panel build.
func (h *Handler) Built() bool {
	return h.files["index.html"] != nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", csp)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "same-origin")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hdr.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.Built() {
		hdr.Set("Content-Type", "text/html; charset=utf-8")
		hdr.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(notBuilt))
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	f := h.files[name]
	if f == nil {
		// A missing file with an extension (a stale asset, a typo) is a real
		// 404; anything else is a panel route.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		f = h.files["index.html"]
	}

	if f.immutable {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hdr.Set("Cache-Control", "no-cache")
	}
	hdr.Set("ETag", f.etag)
	hdr.Set("Content-Type", f.contentType)
	if f.gz != nil {
		hdr.Add("Vary", "Accept-Encoding")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body := f.data
	if f.gz != nil && acceptsGzip(r) {
		hdr.Set("Content-Encoding", "gzip")
		body = f.gz
	}
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			return strings.ReplaceAll(strings.TrimSpace(q), " ", "") != "q=0"
		}
	}
	return false
}

const notBuilt = `<!doctype html>
<meta charset="utf-8">
<title>bin console</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:36rem;margin:15vh auto;padding:0 1rem;color:#222}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
<h1>The panel is not in this build</h1>
<p>This binary was compiled without the web panel. The S3 API and the admin API under <code>/api/v1</code> work normally.</p>
<p>Build it with <code>make web</code> (or <code>npm run build</code> in <code>web/</code>) and compile again, or use the Docker image.</p>
`
