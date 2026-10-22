package s3err

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteFullBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/compat/missing.txt", nil)
	Write(rec, req, ErrNoSuchKey, Resource{Bucket: "compat", Key: "missing.txt"}, "18E0DF7A072D60C1", "host1")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml" {
		t.Errorf("content-type = %q", ct)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message>` +
		`<Key>missing.txt</Key><BucketName>compat</BucketName><Resource>/compat/missing.txt</Resource>` +
		`<RequestId>18E0DF7A072D60C1</RequestId><HostId>host1</HostId></Error>`
	if got := rec.Body.String(); got != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
}

func TestWriteBucketOnlyOmitsKey(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/compat", nil)
	Write(rec, req, ErrBucketAlreadyOwnedByYou, Resource{Bucket: "compat"}, "R", "H")
	want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<Error><Code>BucketAlreadyOwnedByYou</Code><Message>Your previous request to create the named bucket succeeded and you already own it.</Message>` +
		`<BucketName>compat</BucketName><Resource>/compat</Resource><RequestId>R</RequestId><HostId>H</HostId></Error>`
	if rec.Code != 409 || rec.Body.String() != want {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestWriteHeadHasNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/missing-bucket", nil)
	Write(rec, req, ErrNoSuchBucket, Resource{Bucket: "missing-bucket"}, "R", "H")
	if rec.Code != 404 || rec.Body.Len() != 0 {
		t.Errorf("got %d with %d body bytes", rec.Code, rec.Body.Len())
	}
}

func TestWriteEscapesAndUnknownErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/b/x", nil)
	Write(rec, req, fmt.Errorf("disk on fire"), Resource{Bucket: "b", Key: "a<b>&c"}, "R", "H")
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, s := range []string{"<Code>InternalError</Code>", "<Key>a&lt;b&gt;&amp;c</Key>"} {
		if !strings.Contains(body, s) {
			t.Errorf("body %q lacks %q", body, s)
		}
	}
	if strings.Contains(body, "disk on fire") {
		t.Error("internal error details must not leak to clients")
	}
}

func TestIsMatchesByCode(t *testing.T) {
	custom := ErrInvalidArgument.WithMessage("Part number must be an integer between 1 and 10000, inclusive")
	wrapped := fmt.Errorf("upload part: %w", custom)
	if !errors.Is(wrapped, ErrInvalidArgument) {
		t.Error("customized error should match its base code")
	}
	if errors.Is(wrapped, ErrNoSuchKey) {
		t.Error("different codes must not match")
	}
	if ErrInvalidArgument.Message == custom.Message {
		t.Error("WithMessage must not mutate the base error")
	}
}
