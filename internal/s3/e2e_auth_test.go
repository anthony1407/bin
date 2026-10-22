package s3

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"bin/internal/auth"
	"bin/internal/s3/sigv4"
	"bin/internal/secrets"
	"bin/internal/storage"
)

type authEnv struct {
	url   string
	store *storage.Store
	keys  *auth.Keyring
	root  *awss3.Client
}

// newAuthE2E runs the S3 API with the real keyring: root plus stored keys.
func newAuthE2E(t *testing.T) authEnv {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{9}, 32))
	keys := &auth.Keyring{RootAccessKey: testAK, RootSecretKey: testSecret, Store: store, Box: box, Log: log}
	srv := httptest.NewServer(NewHandler(Options{
		Store: store, Region: "us-east-1", HostID: "h", Owner: Owner{ID: "o", DisplayName: "bin"},
		Auth: &sigv4.Verifier{Region: "us-east-1", Store: keys}, Keys: keys, Log: log,
	}))
	t.Cleanup(srv.Close)
	return authEnv{url: srv.URL, store: store, keys: keys, root: clientFor(srv.URL, testAK, testSecret)}
}

func clientFor(url, ak, sk string) *awss3.Client {
	return awss3.New(awss3.Options{
		BaseEndpoint: aws.String(url), Region: "us-east-1", UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(ak, sk, ""),
	})
}

func (e authEnv) newKey(t *testing.T, scope auth.Scope) *awss3.Client {
	t.Helper()
	ak, sk := auth.NewCredentials()
	if err := e.store.CreateAccessKey(context.Background(), storage.AccessKey{
		AccessKey: ak, SecretEnc: e.keys.Box.Encrypt(sk), Enabled: true, Scope: scope.String(),
	}); err != nil {
		t.Fatal(err)
	}
	return clientFor(e.url, ak, sk)
}

func (e authEnv) put(t *testing.T, bucket, key, body string) {
	t.Helper()
	if _, err := e.root.PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader(body),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestE2EScopedAccessKey(t *testing.T) {
	ctx := context.Background()
	e := newAuthE2E(t)
	mustBucket(t, e.root, "photos")
	mustBucket(t, e.root, "backups")
	e.put(t, "photos", "a.jpg", "jpeg")
	e.put(t, "backups", "db.sql", "dump")

	ro := e.newKey(t, auth.Scope{Buckets: []string{"photos"}, ReadOnly: true})
	if _, body := getBody(t, ro, &awss3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("a.jpg")}); body != "jpeg" {
		t.Errorf("read in scope: %q", body)
	}
	list, err := ro.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil || len(list.Buckets) != 1 || aws.ToString(list.Buckets[0].Name) != "photos" {
		t.Errorf("ListBuckets must be filtered: %+v, %v", list, err)
	}
	for name, err := range map[string]error{
		"write in scope": func() error {
			_, err := ro.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("photos"), Key: aws.String("b"), Body: strings.NewReader("x")})
			return err
		}(),
		"delete in scope": func() error {
			_, err := ro.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("photos"), Key: aws.String("a.jpg")})
			return err
		}(),
		"read out of scope": func() error {
			_, err := ro.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("backups"), Key: aws.String("db.sql")})
			return err
		}(),
		"create bucket": func() error {
			_, err := ro.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("newone")})
			return err
		}(),
	} {
		if code := apiCode(err); code != "AccessDenied" {
			t.Errorf("%s: %v", name, err)
		}
	}

	rw := e.newKey(t, auth.Scope{Buckets: []string{"photos"}})
	if _, err := rw.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("photos"), Key: aws.String("b"), Body: strings.NewReader("x")}); err != nil {
		t.Errorf("write with a read-write scoped key: %v", err)
	}
	_, err = rw.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("photos"), Key: aws.String("stolen"), CopySource: aws.String("backups/db.sql")})
	if code := apiCode(err); code != "AccessDenied" {
		t.Errorf("copy from an out-of-scope bucket: %v", err)
	}
}

// anon sends an unsigned request.
func anon(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestE2EAnonymousPolicies(t *testing.T) {
	ctx := context.Background()
	e := newAuthE2E(t)
	mustBucket(t, e.root, "site")
	e.put(t, "site", "index.html", "<h1>hi</h1>")
	e.put(t, "site", "shared/photo.jpg", "jpeg")
	e.put(t, "site", "private/secret.txt", "secret")

	if code, _ := anon(t, "GET", e.url+"/site/index.html", ""); code != 403 {
		t.Errorf("private bucket served anonymously: %d", code)
	}
	if code, _ := anon(t, "GET", e.url+"/missing/index.html", ""); code != 403 {
		t.Errorf("missing bucket must look like a denied one: %d", code)
	}

	e.store.SetBucketPolicy(ctx, "site", auth.Policy{Mode: "public"}.String())
	if code, body := anon(t, "GET", e.url+"/site/index.html", ""); code != 200 || body != "<h1>hi</h1>" {
		t.Errorf("public read: %d %q", code, body)
	}
	if code, body := anon(t, "GET", e.url+"/site?list-type=2", ""); code != 200 || !strings.Contains(body, "index.html") {
		t.Errorf("public list: %d", code)
	}
	if code, _ := anon(t, "PUT", e.url+"/site/defaced.html", "x"); code != 403 {
		t.Errorf("public bucket accepted an anonymous write: %d", code)
	}
	if code, _ := anon(t, "GET", e.url+"/", ""); code != 403 {
		t.Errorf("anonymous ListBuckets: %d", code)
	}

	e.store.SetBucketPolicy(ctx, "site", auth.Policy{Mode: "custom", Rules: []auth.Rule{
		{Prefix: "shared/", Access: auth.ReadOnly},
		{Prefix: "inbox/", Access: auth.WriteOnly},
	}}.String())
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/site/shared/photo.jpg", 200},
		{"GET", "/site/private/secret.txt", 403},
		{"GET", "/site/index.html", 403},
		{"GET", "/site?list-type=2&prefix=shared/", 200},
		{"GET", "/site?list-type=2", 403},
		{"PUT", "/site/inbox/drop.txt", 200},
		{"GET", "/site/inbox/drop.txt", 403},
		{"DELETE", "/site/inbox/drop.txt", 403},
		{"PUT", "/site/shared/overwrite.jpg", 403},
	} {
		body := ""
		if tc.method == "PUT" {
			body = "dropped"
		}
		if code, _ := anon(t, tc.method, e.url+tc.path, body); code != tc.want {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.path, code, tc.want)
		}
	}
	if _, body := getBody(t, e.root, &awss3.GetObjectInput{Bucket: aws.String("site"), Key: aws.String("inbox/drop.txt")}); body != "dropped" {
		t.Errorf("anonymous upload content: %q", body)
	}
}

func TestE2EQuota(t *testing.T) {
	ctx := context.Background()
	e := newAuthE2E(t)
	mustBucket(t, e.root, "small")
	e.store.SetBucketQuota(ctx, "small", 10)
	e.put(t, "small", "a", "12345")
	_, err := e.root.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("small"), Key: aws.String("b"), Body: strings.NewReader("123456")})
	if code := apiCode(err); code != "QuotaExceeded" {
		t.Errorf("over quota: %v", err)
	}
}
