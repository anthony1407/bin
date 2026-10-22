package sigv4

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/encoding/httpbinding"
)

// The AWS SDK for Go v2 signer is an independent implementation of SigV4.
// Every request it signs must verify here.

var oracleKeys = []string{
	"plain.txt",
	"folder with spaces/ñandú +1.txt",
	"a//b/../c",
	"~tilde_-.!*'()",
	"emoji/😀.png",
	"q?x=1&y#frag",
	"%41literal-percent",
	"semi;colon,comma=equals@at:colon$dollar",
}

var oracleQueries = []string{
	"",
	"uploads",
	"list-type=2&prefix=dir%2F&delimiter=%2F&encoding-type=url",
	"partNumber=2&uploadId=abc%2Bdef%3D%3D",
	"x-id=GetObject&versionId=null",
	"prefix=%C3%B1and%C3%BA%20%2B&max-keys=1000",
}

func oracleURL(key, query string) string {
	u := "http://localhost:9000/bucket/" + httpbinding.EscapePath(key, false)
	if query != "" {
		u += "?" + query
	}
	return u
}

func TestSDKSignerHeaderAuth(t *testing.T) {
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: testAK, SecretAccessKey: testSecret}
	for _, method := range []string{"GET", "PUT", "HEAD", "DELETE", "POST"} {
		for _, key := range oracleKeys {
			for _, query := range oracleQueries {
				r := httptest.NewRequest(method, oracleURL(key, query), nil)
				r.Host = "localhost:9000"
				r.Header.Set("X-Amz-Content-Sha256", emptySHA256)
				r.Header.Set("Content-Type", "text/plain;   charset=utf-8")
				r.Header.Set("X-Amz-Meta-Note", "  spaced    value ")
				if err := signer.SignHTTP(context.Background(), creds, r, emptySHA256, "s3", "us-east-1", testNow); err != nil {
					t.Fatal(err)
				}
				if _, err := testVerifier().Verify(r); err != nil {
					t.Errorf("%s %s: %v\n  Authorization: %s", method, r.URL.RequestURI(), err, r.Header.Get("Authorization"))
				}
			}
		}
	}
}

func TestSDKSignerPresigned(t *testing.T) {
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: testAK, SecretAccessKey: testSecret}
	for _, key := range oracleKeys {
		for _, query := range oracleQueries {
			q := query
			if q != "" {
				q += "&"
			}
			src := httptest.NewRequest("GET", oracleURL(key, q+"X-Amz-Expires=900"), nil)
			src.Host = "localhost:9000"
			signed, _, err := signer.PresignHTTP(context.Background(), creds, src, UnsignedPayload, "s3", "us-east-1", testNow)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(signed)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("GET", u.RequestURI(), nil)
			r.Host = "localhost:9000"
			if _, err := testVerifier().Verify(r); err != nil {
				t.Errorf("GET %s: %v", u.RequestURI(), err)
			}
		}
	}
}

// A client that leaves sub-delimiters unescaped and signs the path as sent.
func TestRawPathAsSent(t *testing.T) {
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: testAK, SecretAccessKey: testSecret}
	r := httptest.NewRequest("GET", "/bucket/report(final)!.txt", nil)
	r.Host = "localhost:9000"
	r.Header.Set("X-Amz-Content-Sha256", emptySHA256)
	r.URL.RawPath = "/bucket/report(final)!.txt"
	if err := signer.SignHTTP(context.Background(), creds, r, emptySHA256, "s3", "us-east-1", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := testVerifier().Verify(r); err != nil {
		t.Fatalf("%v (Authorization: %s)", err, r.Header.Get("Authorization"))
	}
}

// The raw-path fallback must not let a signature for one path authorize
// another: the raw path has to decode to the routed path.
func TestRawPathMustMatchRoutedPath(t *testing.T) {
	r := presignedExample()
	r.Host = "examplebucket.s3.amazonaws.com"
	r.URL.Path = "/other.txt" // RequestURI still says /test.txt
	if _, err := exampleVerifier(exampleTime).Verify(r); err == nil {
		t.Fatal("signature for /test.txt accepted for /other.txt")
	}
}
