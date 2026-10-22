package s3

import (
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler(t *testing.T, ops map[Op]opFunc) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newHandler(Options{HostID: "test-host", Log: log})
	for op, fn := range ops {
		h.ops[op] = fn
	}
	return h.withMiddleware()
}

type errBody struct {
	Code, Message, Resource, RequestId, HostId string
}

func doRequest(h http.Handler, method, target string) (*httptest.ResponseRecorder, errBody) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	var b errBody
	xml.Unmarshal(rec.Body.Bytes(), &b)
	return rec, b
}

func TestUnimplementedOperation(t *testing.T) {
	h := newTestHandler(t, nil)
	rec, body := doRequest(h, "GET", "/b/k.txt")
	if rec.Code != http.StatusNotImplemented || body.Code != "NotImplemented" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
	id := rec.Header().Get("X-Amz-Request-Id")
	if len(id) != 16 || body.RequestId != id {
		t.Errorf("request id header %q vs body %q", id, body.RequestId)
	}
	if rec.Header().Get("X-Amz-Id-2") != "test-host" || body.HostId != "test-host" {
		t.Errorf("host id not propagated: %q / %q", rec.Header().Get("X-Amz-Id-2"), body.HostId)
	}
	if body.Resource != "/b/k.txt" {
		t.Errorf("resource = %q", body.Resource)
	}
	if rec.Header().Get("Server") != "bin" {
		t.Errorf("server header = %q", rec.Header().Get("Server"))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec, body := doRequest(newTestHandler(t, nil), "PATCH", "/b/k.txt")
	if rec.Code != http.StatusMethodNotAllowed || body.Code != "MethodNotAllowed" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
}

func TestPanicBecomesInternalError(t *testing.T) {
	h := newTestHandler(t, map[Op]opFunc{
		OpGetObject: func(http.ResponseWriter, *http.Request, Request) error { panic("boom") },
	})
	rec, body := doRequest(h, "GET", "/b/k")
	if rec.Code != 500 || body.Code != "InternalError" || body.Resource != "/b/k" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
}

func TestOperationDispatch(t *testing.T) {
	var got Request
	h := newTestHandler(t, map[Op]opFunc{
		OpListBuckets: func(w http.ResponseWriter, _ *http.Request, req Request) error {
			got = req
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	rec, _ := doRequest(h, "GET", "/")
	if rec.Code != 200 || got.Op != OpListBuckets {
		t.Fatalf("got %d %+v", rec.Code, got)
	}
}
