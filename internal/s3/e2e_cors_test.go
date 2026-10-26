package s3

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"bin/internal/auth"
)

// rawRequest sends an unsigned request with headers and returns the response
// (body closed).
func rawRequest(t *testing.T, method, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestE2ECORS(t *testing.T) {
	ctx := context.Background()
	e := newAuthE2E(t)
	mustBucket(t, e.root, "assets")
	e.put(t, "assets", "logo.png", "png")
	const app = "https://app.example.com"

	_, err := e.root.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String("assets")})
	if apiCode(err) != "NoSuchCORSConfiguration" {
		t.Errorf("get without configuration: %v", err)
	}
	preflight := map[string]string{"Origin": app, "Access-Control-Request-Method": "PUT",
		"Access-Control-Request-Headers": "content-type, x-amz-date"}
	if resp := rawRequest(t, "OPTIONS", e.url+"/assets/upload.bin", preflight); resp.StatusCode != 403 {
		t.Errorf("preflight without CORS: %d", resp.StatusCode)
	}

	_, err = e.root.PutBucketCors(ctx, &awss3.PutBucketCorsInput{Bucket: aws.String("assets"), CORSConfiguration: &types.CORSConfiguration{
		CORSRules: []types.CORSRule{
			{ID: aws.String("app"), AllowedOrigins: []string{app}, AllowedMethods: []string{"GET", "PUT"},
				AllowedHeaders: []string{"content-type", "x-amz-*"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: aws.Int32(600)},
			{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.root.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String("assets")})
	if err != nil || len(got.CORSRules) != 2 || aws.ToString(got.CORSRules[0].ID) != "app" ||
		aws.ToInt32(got.CORSRules[0].MaxAgeSeconds) != 600 || got.CORSRules[1].MaxAgeSeconds != nil {
		t.Fatalf("get = %+v, %v", got, err)
	}

	// Preflights, unauthenticated.
	resp := rawRequest(t, "OPTIONS", e.url+"/assets/upload.bin", preflight)
	h := resp.Header
	if resp.StatusCode != 200 || h.Get("Access-Control-Allow-Origin") != app || h.Get("Access-Control-Allow-Methods") != "GET, PUT" ||
		h.Get("Access-Control-Allow-Headers") != "content-type, x-amz-date" || h.Get("Access-Control-Max-Age") != "600" ||
		h.Get("Access-Control-Expose-Headers") != "ETag" || h.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("preflight = %d %v", resp.StatusCode, h)
	}
	for name, headers := range map[string]map[string]string{
		"header not allowed": {"Origin": app, "Access-Control-Request-Method": "PUT", "Access-Control-Request-Headers": "authorization"},
		"method not allowed": {"Origin": app, "Access-Control-Request-Method": "DELETE"},
		"other origin, PUT":  {"Origin": "https://evil.test", "Access-Control-Request-Method": "PUT"},
	} {
		if resp := rawRequest(t, "OPTIONS", e.url+"/assets/upload.bin", headers); resp.StatusCode != 403 {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	resp = rawRequest(t, "OPTIONS", e.url+"/assets/logo.png", map[string]string{"Origin": "https://other.test", "Access-Control-Request-Method": "GET"})
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("wildcard preflight = %d %v", resp.StatusCode, resp.Header)
	}
	if resp := rawRequest(t, "OPTIONS", e.url+"/assets/x", map[string]string{"Access-Control-Request-Method": "GET"}); resp.StatusCode != 400 {
		t.Errorf("preflight without Origin: %d", resp.StatusCode)
	}
	if resp := rawRequest(t, "OPTIONS", e.url+"/assets/x", map[string]string{"Origin": app, "Access-Control-Request-Method": "PATCH"}); resp.StatusCode != 400 {
		t.Errorf("preflight with PATCH: %d", resp.StatusCode)
	}
	if resp := rawRequest(t, "OPTIONS", e.url+"/missing/x", map[string]string{"Origin": app, "Access-Control-Request-Method": "GET"}); resp.StatusCode != 403 {
		t.Errorf("preflight on a missing bucket: %d", resp.StatusCode)
	}

	// Actual requests carry the headers, errors included, so the page can read them.
	resp = rawRequest(t, "GET", e.url+"/assets/logo.png", map[string]string{"Origin": app})
	if resp.StatusCode != 403 || resp.Header.Get("Access-Control-Allow-Origin") != app || resp.Header.Get("Vary") != "Origin" {
		t.Errorf("denied GET from the app = %d %v", resp.StatusCode, resp.Header)
	}
	e.store.SetBucketPolicy(ctx, "assets", auth.Policy{Mode: "public"}.String())
	resp = rawRequest(t, "GET", e.url+"/assets/logo.png", map[string]string{"Origin": "https://other.test"})
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("public GET from another site = %d %v", resp.StatusCode, resp.Header)
	}
	resp = rawRequest(t, "HEAD", e.url+"/assets/logo.png", map[string]string{"Origin": "https://other.test"})
	if resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.Header.Get("Vary") != "Origin" {
		t.Errorf("HEAD is not allowed for that origin, yet: %v", resp.Header)
	}
	resp = rawRequest(t, "GET", e.url+"/assets/logo.png", nil)
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS headers without Origin: %v", resp.Header)
	}

	// Invalid configurations are refused; delete removes the rules.
	for name, rules := range map[string][]types.CORSRule{
		"PATCH":         {{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"PATCH"}}},
		"two wildcards": {{AllowedOrigins: []string{"https://*.*.com"}, AllowedMethods: []string{"GET"}}},
	} {
		_, err := e.root.PutBucketCors(ctx, &awss3.PutBucketCorsInput{Bucket: aws.String("assets"),
			CORSConfiguration: &types.CORSConfiguration{CORSRules: rules}})
		if apiCode(err) != "InvalidRequest" {
			t.Errorf("%s: %v", name, err)
		}
	}
	ro := e.newKey(t, auth.Scope{Buckets: []string{"assets"}, ReadOnly: true})
	if _, err := ro.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String("assets")}); err != nil {
		t.Errorf("read-only key, get CORS: %v", err)
	}
	if _, err := ro.DeleteBucketCors(ctx, &awss3.DeleteBucketCorsInput{Bucket: aws.String("assets")}); apiCode(err) != "AccessDenied" {
		t.Errorf("read-only key deleted CORS: %v", err)
	}
	if _, err := e.root.DeleteBucketCors(ctx, &awss3.DeleteBucketCorsInput{Bucket: aws.String("assets")}); err != nil {
		t.Fatal(err)
	}
	if resp := rawRequest(t, "OPTIONS", e.url+"/assets/logo.png", map[string]string{"Origin": app, "Access-Control-Request-Method": "GET"}); resp.StatusCode != 403 {
		t.Errorf("preflight after delete: %d", resp.StatusCode)
	}
}
