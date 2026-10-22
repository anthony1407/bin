package s3

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"bin/internal/s3/sigv4"
)

const (
	testAK     = "testaccess"
	testSecret = "testsecret123"
	emptyHash  = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func newTestHandler(t *testing.T, ops map[Op]opFunc) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newHandler(Options{
		HostID: "test-host",
		Auth:   &sigv4.Verifier{Region: "us-east-1", Store: sigv4.StaticStore{testAK: testSecret}},
		Log:    log,
	})
	for op, fn := range ops {
		if fn == nil {
			delete(h.ops, op) // simulate an operation that is not implemented
		} else {
			h.ops[op] = fn
		}
	}
	return h.withMiddleware()
}

type errBody struct {
	Code, Message, Resource, RequestId, HostId string
}

// doRequest sends a request signed with the AWS SDK signer.
func doRequest(h http.Handler, method, target string) (*httptest.ResponseRecorder, errBody) {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("X-Amz-Content-Sha256", emptyHash)
	creds := aws.Credentials{AccessKeyID: testAK, SecretAccessKey: testSecret}
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	if err := signer.SignHTTP(context.Background(), creds, r, emptyHash, "s3", "us-east-1", time.Now()); err != nil {
		panic(err)
	}
	return send(h, r)
}

func send(h http.Handler, r *http.Request) (*httptest.ResponseRecorder, errBody) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var b errBody
	xml.Unmarshal(rec.Body.Bytes(), &b)
	return rec, b
}

func TestUnimplementedOperation(t *testing.T) {
	h := newTestHandler(t, map[Op]opFunc{OpGetObject: nil})
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

func TestAnonymousIsDenied(t *testing.T) {
	for _, method := range []string{"GET", "PUT", "PATCH"} {
		rec, body := send(newTestHandler(t, nil), httptest.NewRequest(method, "/b/k.txt", nil))
		if rec.Code != http.StatusForbidden || body.Code != "AccessDenied" || body.Resource != "/b/k.txt" {
			t.Errorf("%s: got %d %+v", method, rec.Code, body)
		}
	}
}

func TestBadSignatureIsRejected(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Amz-Content-Sha256", emptyHash)
	creds := aws.Credentials{AccessKeyID: testAK, SecretAccessKey: "wrong-secret"}
	v4.NewSigner().SignHTTP(context.Background(), creds, r, emptyHash, "s3", "us-east-1", time.Now())
	rec, body := send(newTestHandler(t, nil), r)
	if rec.Code != http.StatusForbidden || body.Code != "SignatureDoesNotMatch" {
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
	var gotKey string
	h := newTestHandler(t, map[Op]opFunc{
		OpListBuckets: func(w http.ResponseWriter, r *http.Request, req Request) error {
			got, gotKey = req, AccessKey(r.Context())
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	rec, _ := doRequest(h, "GET", "/")
	if rec.Code != 200 || got.Op != OpListBuckets || gotKey != testAK {
		t.Fatalf("got %d %+v key=%q", rec.Code, got, gotKey)
	}
}
