package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var bigJS = strings.Repeat("console.log('bin');\n", 200)

func testHandler() *Handler {
	return newHandler(fstest.MapFS{
		"dist/index.html":         {Data: []byte("<!doctype html><div id=root></div>")},
		"dist/favicon.svg":        {Data: []byte("<svg/>")},
		"dist/assets/index-ab.js": {Data: []byte(bigJS)},
		"dist/.gitkeep":           {Data: nil},
	})
}

func get(h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServesFilesAndFallsBackToIndex(t *testing.T) {
	h := testHandler()
	for _, tc := range []struct {
		path, wantBody, wantCache string
		wantStatus                int
	}{
		{"/", "<!doctype html>", "no-cache", 200},
		{"/buckets/photos/settings", "<!doctype html>", "no-cache", 200},
		{"/favicon.svg", "<svg/>", "no-cache", 200},
		{"/assets/index-ab.js", "console.log", "public, max-age=31536000, immutable", 200},
		{"/assets/missing-zz.js", "", "", 404},
		{"/.gitkeep", "", "", 404},
		{"/../../etc/passwd", "<!doctype html>", "no-cache", 200}, // cleaned to a panel route
	} {
		w := get(h, "GET", tc.path, nil)
		if w.Code != tc.wantStatus {
			t.Errorf("%s: status %d, want %d", tc.path, w.Code, tc.wantStatus)
			continue
		}
		if tc.wantStatus != 200 {
			continue
		}
		if !strings.HasPrefix(w.Body.String(), tc.wantBody) {
			t.Errorf("%s: body %q", tc.path, w.Body.String())
		}
		if got := w.Header().Get("Cache-Control"); got != tc.wantCache {
			t.Errorf("%s: Cache-Control %q, want %q", tc.path, got, tc.wantCache)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("%s: missing CSP", tc.path)
		}
	}
}

func TestGzipAndConditionalRequests(t *testing.T) {
	h := testHandler()
	w := get(h, "GET", "/assets/index-ab.js", map[string]string{"Accept-Encoding": "br, gzip"})
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("not compressed: %v", w.Header())
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != bigJS {
		t.Fatal("gzip body does not decompress to the file")
	}

	if w := get(h, "GET", "/assets/index-ab.js", map[string]string{"Accept-Encoding": "gzip;q=0"}); w.Header().Get("Content-Encoding") != "" {
		t.Error("compressed although gzip;q=0")
	}

	etag := w.Header().Get("ETag")
	if w := get(h, "GET", "/assets/index-ab.js", map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d", w.Code)
	}
	if w := get(h, "HEAD", "/", nil); w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD: %d, %d bytes, %v", w.Code, w.Body.Len(), w.Header())
	}
	if w := get(h, "POST", "/", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", w.Code)
	}
}

func TestNotBuilt(t *testing.T) {
	h := newHandler(fstest.MapFS{"dist/.gitkeep": {}})
	if h.Built() {
		t.Fatal("Built() with no index.html")
	}
	w := get(h, "GET", "/buckets", nil)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "not in this build") {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}
