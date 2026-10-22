package sigv4

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

func verifyURL(t *testing.T, signed string, now time.Time) error {
	t.Helper()
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", u.RequestURI(), nil)
	r.Host = u.Host
	v := testVerifier()
	v.Now = func() time.Time { return now }
	_, err = v.Verify(r)
	return err
}

func TestPresignVerifies(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:9000/photos/a.jpg",
		"http://localhost:9000/photos/folder%20with%20spaces/%C3%B1and%C3%BA%20%2B1.txt",
		"http://localhost:9000/photos/a.jpg?response-content-disposition=attachment%3B%20filename%3D%22a%20b.jpg%22",
	} {
		signed, err := Presign("GET", raw, testAK, testSecret, "us-east-1", time.Hour, testNow)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyURL(t, signed, testNow.Add(time.Minute)); err != nil {
			t.Errorf("%s: %v\n%s", raw, err, signed)
		}
		// Query values must survive unchanged: a space stays a space.
		want, _ := url.Parse(raw)
		got, _ := url.Parse(signed)
		for k := range want.Query() {
			if got.Query().Get(k) != want.Query().Get(k) {
				t.Errorf("%s: parameter %s became %q", raw, k, got.Query().Get(k))
			}
		}
		if err := verifyURL(t, signed, testNow.Add(2*time.Hour)); err == nil {
			t.Errorf("%s: expired URL accepted", raw)
		}
		if err := verifyURL(t, strings.Replace(signed, "/photos/", "/other/", 1), testNow); err == nil {
			t.Errorf("%s: tampered URL accepted", raw)
		}
	}
	if _, err := Presign("GET", "http://h/b/k", testAK, testSecret, "us-east-1", 8*24*time.Hour, testNow); err == nil {
		t.Error("expiry over 7 days accepted")
	}
}

// The AWS SDK presigner must produce the same signature for the same input.
func TestPresignMatchesSDK(t *testing.T) {
	raw := "http://localhost:9000/photos/a%20b.jpg?response-content-type=image%2Fjpeg"
	ours, err := Presign("GET", raw, testAK, testSecret, "us-east-1", 15*time.Minute, testNow)
	if err != nil {
		t.Fatal(err)
	}
	src := httptest.NewRequest("GET", raw+"&X-Amz-Expires=900", nil)
	src.Host = "localhost:9000"
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	theirs, _, err := signer.PresignHTTP(context.Background(),
		aws.Credentials{AccessKeyID: testAK, SecretAccessKey: testSecret}, src, UnsignedPayload, "s3", "us-east-1", testNow)
	if err != nil {
		t.Fatal(err)
	}
	sig := func(s string) string { u, _ := url.Parse(s); return u.Query().Get("X-Amz-Signature") }
	if sig(ours) != sig(theirs) {
		t.Errorf("signatures differ:\n ours   %s\n theirs %s", ours, theirs)
	}
}
