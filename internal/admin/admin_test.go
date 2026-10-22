package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	h := NewHandler(Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || body["status"] != "ok" || body["version"] == "" {
		t.Errorf("got %d %v", rec.Code, body)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("missing request id")
	}
}

func TestUnknownAPIRouteIsJSON(t *testing.T) {
	h := NewHandler(Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/nope", nil))
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("got %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
