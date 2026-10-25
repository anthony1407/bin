package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"bin/internal/audit"
	"bin/internal/auth"
	"bin/internal/logging"
	"bin/internal/s3"
	"bin/internal/s3/sigv4"
	"bin/internal/secrets"
	"bin/internal/storage"
)

const (
	rootAK = "rootaccess"
	rootSK = "rootsecret-long"
)

type env struct {
	t       *testing.T
	console string
	s3URL   string
	dir     string
	client  *http.Client
	csrf    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ring := logging.NewRing(500)
	log := slog.New(logging.Tee(slog.NewTextHandler(io.Discard, nil), ring))
	dir := t.TempDir()
	store, err := storage.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{3}, 32))
	keys := &auth.Keyring{RootAccessKey: rootAK, RootSecretKey: rootSK, Store: store, Box: box, Log: log}
	rec := audit.New(store, log)
	t.Cleanup(rec.Close) // before the store closes
	s3srv := httptest.NewServer(s3.NewHandler(s3.Options{
		Store: store, Region: "us-east-1", HostID: "h", Owner: s3.Owner{ID: "o", DisplayName: "bin"},
		Auth: &sigv4.Verifier{Region: "us-east-1", Store: keys}, Keys: keys, Audit: rec, Log: log,
	}))
	t.Cleanup(s3srv.Close)
	console := httptest.NewServer(NewHandler(Options{
		Store: store, Keys: keys, Audit: rec, Region: "us-east-1", DataDir: dir, PublicURL: s3srv.URL, Log: log, LogRing: ring,
	}))
	t.Cleanup(console.Close)
	jar, _ := cookiejar.New(nil)
	return &env{t: t, console: console.URL, s3URL: s3srv.URL, dir: dir, client: &http.Client{Jar: jar}}
}

// call sends a console API request and decodes the JSON answer into out.
func (e *env) call(method, path string, body any, out any) int {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		rd = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, e.console+path, rd)
	if e.csrf != "" {
		req.Header.Set("X-CSRF-Token", e.csrf)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (e *env) login() {
	e.t.Helper()
	var s sessionResponse
	if code := e.call("POST", "/api/v1/login", loginRequest{rootAK, rootSK}, &s); code != 200 || s.CSRFToken == "" {
		e.t.Fatalf("login: %d %+v", code, s)
	}
	e.csrf = s.CSRFToken
}

func s3Client(url, ak, sk string) *awss3.Client {
	return awss3.New(awss3.Options{BaseEndpoint: aws.String(url), Region: "us-east-1", UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(ak, sk, "")})
}

func TestLoginAndSessionSecurity(t *testing.T) {
	e := newEnv(t)
	var apiErr apiError
	if code := e.call("GET", "/api/v1/buckets", nil, &apiErr); code != 401 || apiErr.Error != "not_signed_in" {
		t.Errorf("no session: %d %+v", code, apiErr)
	}
	if code := e.call("POST", "/api/v1/login", loginRequest{rootAK, "wrong"}, &apiErr); code != 401 {
		t.Errorf("wrong secret: %d", code)
	}

	e.login()
	resp, _ := e.client.Get(e.console + "/api/v1/session")
	resp.Body.Close()
	u, _ := http.NewRequest("GET", e.console, nil)
	for _, c := range e.client.Jar.Cookies(u.URL) {
		if c.Name == sessionCookie && c.Value == "" {
			t.Error("empty session cookie")
		}
	}
	if code := e.call("GET", "/api/v1/info", nil, nil); code != 200 {
		t.Errorf("info with a session: %d", code)
	}

	csrf := e.csrf
	e.csrf = ""
	if code := e.call("POST", "/api/v1/buckets", map[string]string{"name": "nocsrf"}, &apiErr); code != 403 || apiErr.Error != "bad_csrf_token" {
		t.Errorf("missing CSRF: %d %+v", code, apiErr)
	}
	e.csrf = csrf
	req, _ := http.NewRequest("POST", e.console+"/api/v1/buckets", strings.NewReader(`{"name":"evil"}`))
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ := e.client.Do(req); resp.StatusCode != 403 {
		t.Errorf("foreign origin: %d", resp.StatusCode)
	}

	if code := e.call("POST", "/api/v1/logout", nil, nil); code != 204 {
		t.Errorf("logout: %d", code)
	}
	if code := e.call("GET", "/api/v1/buckets", nil, nil); code != 401 {
		t.Errorf("after logout: %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < maxLoginFailures; i++ {
		e.call("POST", "/api/v1/login", loginRequest{rootAK, "wrong"}, nil)
	}
	if code := e.call("POST", "/api/v1/login", loginRequest{rootAK, rootSK}, nil); code != 429 {
		t.Errorf("after %d failures even the right password must wait: %d", maxLoginFailures, code)
	}
}

func TestBucketsAndPolicies(t *testing.T) {
	e := newEnv(t)
	e.login()
	var b bucketJSON
	if code := e.call("POST", "/api/v1/buckets", map[string]string{"name": "photos"}, &b); code != 201 || b.Name != "photos" || b.Policy.Mode != "private" {
		t.Fatalf("create: %d %+v", code, b)
	}
	var apiErr apiError
	if code := e.call("POST", "/api/v1/buckets", map[string]string{"name": "Bad_Name"}, &apiErr); code != 400 || apiErr.Error != "invalid_bucket_name" {
		t.Errorf("invalid name: %d %+v", code, apiErr)
	}
	e.call("PUT", "/api/v1/buckets/photos/upload?key=a.txt", "hello", nil)
	e.call("PUT", "/api/v1/buckets/photos/tags", map[string]any{"tags": map[string]string{"team": "home"}}, nil)
	e.call("PUT", "/api/v1/buckets/photos/quota", map[string]int64{"bytes": 1 << 30}, nil)
	if code := e.call("PUT", "/api/v1/buckets/photos/policy", map[string]any{"mode": "public"}, &b); code != 200 {
		t.Fatalf("policy: %d", code)
	}
	var list struct{ Buckets []bucketJSON }
	e.call("GET", "/api/v1/buckets", nil, &list)
	got := list.Buckets[0]
	if got.Objects != 1 || got.Size != 5 || got.Tags["team"] != "home" || got.QuotaBytes != 1<<30 || got.Policy.Mode != "public" {
		t.Errorf("bucket listing: %+v", got)
	}
	resp, _ := http.Get(e.s3URL + "/photos/a.txt") // anonymous S3 read, opened by the policy
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(data) != "hello" {
		t.Errorf("public read through S3: %d %q", resp.StatusCode, data)
	}
	if code := e.call("PUT", "/api/v1/buckets/photos/policy", map[string]any{"mode": "custom"}, &apiErr); code != 400 {
		t.Errorf("invalid policy accepted: %d", code)
	}

	if code := e.call("DELETE", "/api/v1/buckets/photos", nil, &apiErr); code != 409 || apiErr.Error != "bucket_not_empty" {
		t.Errorf("delete non-empty: %d %+v", code, apiErr)
	}
	if code := e.call("DELETE", "/api/v1/buckets/photos?force=true", nil, nil); code != 204 {
		t.Errorf("force delete: %d", code)
	}

	// A bucket can start public, and one object can be opened on its own.
	if code := e.call("POST", "/api/v1/buckets", map[string]any{"name": "site", "policy": map[string]any{"mode": "public"}}, &b); code != 201 || b.Policy.Mode != "public" {
		t.Errorf("create public: %d %+v", code, b)
	}
	if code := e.call("POST", "/api/v1/buckets", map[string]any{"name": "nope", "policy": map[string]any{"mode": "open"}}, nil); code != 400 {
		t.Errorf("bad policy at creation: %d", code)
	}
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "docs"}, nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=logo.png", "png", nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=logo.png.bak", "old", nil)
	e.call("PUT", "/api/v1/buckets/docs/policy", map[string]any{"mode": "custom",
		"rules": []map[string]any{{"prefix": "logo.png", "access": "readonly", "exact": true}}}, nil)
	for key, want := range map[string]int{"logo.png": 200, "logo.png.bak": 403} {
		resp, _ := http.Get(e.s3URL + "/docs/" + key)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("anonymous GET %s: %d, want %d", key, resp.StatusCode, want)
		}
	}
}

func TestExplorer(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "files"}, nil)
	for _, k := range []string{"docs/Report Final.pdf", "docs/notes.txt", "photos/beach.jpg", "readme.md"} {
		if code := e.call("PUT", "/api/v1/buckets/files/upload?key="+urlQuery(k), "content of "+k, nil); code != 201 {
			t.Fatalf("upload %s: %d", k, code)
		}
	}
	if code := e.call("POST", "/api/v1/buckets/files/folders", map[string]string{"prefix": "empty"}, nil); code != 201 {
		t.Errorf("create folder: %d", code)
	}
	var page struct {
		Objects  []objectJSON
		Prefixes []string
	}
	e.call("GET", "/api/v1/buckets/files/objects", nil, &page)
	if len(page.Objects) != 1 || page.Objects[0].Key != "readme.md" || strings.Join(page.Prefixes, ",") != "docs/,empty/,photos/" {
		t.Errorf("root listing: %+v", page)
	}
	e.call("GET", "/api/v1/buckets/files/objects?search=REPORT", nil, &page)
	if len(page.Objects) != 1 || page.Objects[0].Key != "docs/Report Final.pdf" {
		t.Errorf("search: %+v", page)
	}

	var st objectJSON
	e.call("GET", "/api/v1/buckets/files/object?key="+urlQuery("photos/beach.jpg"), nil, &st)
	if st.ContentType != "image/jpeg" || st.Size != int64(len("content of photos/beach.jpg")) {
		t.Errorf("stat (content type from the extension): %+v", st)
	}

	resp, _ := e.client.Get(e.console + "/api/v1/buckets/files/download?key=" + urlQuery("docs/Report Final.pdf"))
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(data) != "content of docs/Report Final.pdf" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download: %q %v", data, resp.Header)
	}
	req, _ := http.NewRequest("GET", e.console+"/api/v1/buckets/files/download?inline=1&key=readme.md", nil)
	req.Header.Set("Range", "bytes=0-6")
	resp, _ = e.client.Do(req)
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(data) != "content" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("ranged inline download: %d %q", resp.StatusCode, data)
	}

	var del struct{ Deleted int }
	e.call("DELETE", "/api/v1/buckets/files/objects", map[string]any{"keys": []string{"readme.md"}, "prefixes": []string{"docs/"}}, &del)
	if del.Deleted != 3 {
		t.Errorf("deleted %d, want 3", del.Deleted)
	}

	// Multipart through the console, for files over 5 GiB.
	var up struct{ UploadID string }
	e.call("POST", "/api/v1/buckets/files/uploads?key=big.bin", nil, &up)
	part1 := bytes.Repeat([]byte("a"), storage.MinPartSize)
	var p1, p2 struct{ ETag string }
	e.call("PUT", fmt.Sprintf("/api/v1/buckets/files/uploads/%s/parts/1?key=big.bin", up.UploadID), part1, &p1)
	e.call("PUT", fmt.Sprintf("/api/v1/buckets/files/uploads/%s/parts/2?key=big.bin", up.UploadID), "tail", &p2)
	var done objectJSON
	code := e.call("POST", fmt.Sprintf("/api/v1/buckets/files/uploads/%s/complete?key=big.bin", up.UploadID),
		map[string]any{"parts": []map[string]any{{"number": 1, "etag": p1.ETag}, {"number": 2, "etag": p2.ETag}}}, &done)
	if code != 200 || done.Size != int64(len(part1)+4) || done.PartsCount != 2 {
		t.Errorf("multipart complete: %d %+v", code, done)
	}
}

func TestShareLinks(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "share"}, nil)
	e.call("PUT", "/api/v1/buckets/share/upload?key="+urlQuery("my photo.jpg"), "jpeg bytes", nil)

	var link struct {
		URL       string
		ExpiresAt time.Time
	}
	if code := e.call("POST", "/api/v1/buckets/share/share", map[string]any{"key": "my photo.jpg", "expiresIn": 3600, "download": true}, &link); code != 200 {
		t.Fatalf("share: %d", code)
	}
	if !strings.HasPrefix(link.URL, e.s3URL+"/share/my%20photo.jpg?") || time.Until(link.ExpiresAt) > time.Hour {
		t.Errorf("link %+v", link)
	}
	resp, _ := http.Get(link.URL)
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(data) != "jpeg bytes" || !strings.Contains(resp.Header.Get("Content-Disposition"), "my photo.jpg") {
		t.Errorf("share link download: %d %q %v", resp.StatusCode, data, resp.Header)
	}

	e.call("PUT", "/api/v1/settings", map[string]int64{"shareMaxExpirySeconds": 600}, nil)
	var apiErr apiError
	if code := e.call("POST", "/api/v1/buckets/share/share", map[string]any{"key": "my photo.jpg", "expiresIn": 3600}, &apiErr); code != 400 {
		t.Errorf("expiry over the maximum: %d", code)
	}
	if code := e.call("POST", "/api/v1/settings/revoke-share-links", nil, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if resp, _ := http.Get(link.URL); resp.StatusCode != 403 {
		t.Errorf("revoked link still works: %d", resp.StatusCode)
	}
}

func TestAccessKeys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "data"}, nil)
	e.call("PUT", "/api/v1/buckets/data/upload?key=k", "v", nil)

	var created keyJSON
	if code := e.call("POST", "/api/v1/access-keys", map[string]any{"name": "backup job"}, &created); code != 201 || len(created.SecretKey) != 40 {
		t.Fatalf("create key: %d %+v", code, created)
	}
	c := s3Client(e.s3URL, created.AccessKey, created.SecretKey)
	if _, err := c.ListBuckets(ctx, &awss3.ListBucketsInput{}); err != nil {
		t.Errorf("new key cannot use S3: %v", err)
	}

	var list struct{ AccessKeys []keyJSON }
	e.call("GET", "/api/v1/access-keys", nil, &list)
	if len(list.AccessKeys) != 1 || list.AccessKeys[0].SecretKey != "" || list.AccessKeys[0].LastUsed == nil {
		t.Errorf("listing must hide secrets and show last use: %+v", list)
	}

	e.call("PATCH", "/api/v1/access-keys/"+created.AccessKey, map[string]any{"enabled": false}, nil)
	_, err := c.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if code := smithyCode(err); code != "InvalidAccessKeyId" {
		t.Errorf("disabled key: %v", err)
	}
	e.call("PATCH", "/api/v1/access-keys/"+created.AccessKey, map[string]any{"enabled": true, "readOnly": true, "buckets": []string{"data"}}, nil)
	_, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("data"), Key: aws.String("x"), Body: strings.NewReader("x")})
	if code := smithyCode(err); code != "AccessDenied" {
		t.Errorf("read-only key wrote: %v", err)
	}
	var apiErr apiError
	if code := e.call("PATCH", "/api/v1/access-keys/"+created.AccessKey, map[string]any{"buckets": []string{"ghost"}}, &apiErr); code != 400 {
		t.Errorf("scope with an unknown bucket: %d", code)
	}
	if code := e.call("DELETE", "/api/v1/access-keys/"+created.AccessKey, nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	_, err = c.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if code := smithyCode(err); code != "InvalidAccessKeyId" {
		t.Errorf("deleted key: %v", err)
	}
}

func TestScrubFindsCorruption(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "vault"}, nil)
	e.call("PUT", "/api/v1/buckets/vault/upload?key=precious.bin", strings.Repeat("data", 1000), nil)
	filepath.WalkDir(filepath.Join(e.dir, "blobs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			raw, _ := os.ReadFile(p)
			raw[7] ^= 1
			os.WriteFile(p, raw, 0o640)
		}
		return nil
	})
	var st scrubStatus
	if code := e.call("POST", "/api/v1/scrub", map[string]string{}, &st); code != 200 || st.State != "running" {
		t.Fatalf("start scrub: %d %+v", code, st)
	}
	for i := 0; i < 100 && st.State == "running"; i++ {
		time.Sleep(20 * time.Millisecond)
		e.call("GET", "/api/v1/scrub", nil, &st)
	}
	if st.State != "done" || st.Objects != 1 || len(st.Corrupt) != 1 || st.Corrupt[0].Key != "precious.bin" {
		t.Errorf("scrub result: %+v", st)
	}

	// Chosen buckets only; duplicates are ignored and unknown names refused.
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "other"}, nil)
	if code := e.call("POST", "/api/v1/scrub", map[string]any{"buckets": []string{"other", "missing"}}, nil); code != 404 {
		t.Errorf("unknown bucket: %d", code)
	}
	if code := e.call("POST", "/api/v1/scrub", map[string]any{"buckets": []string{"other", "other"}}, &st); code != 200 || len(st.Buckets) != 1 {
		t.Fatalf("scrub chosen buckets: %d %+v", code, st)
	}
	for i := 0; i < 100 && st.State == "running"; i++ {
		time.Sleep(20 * time.Millisecond)
		e.call("GET", "/api/v1/scrub", nil, &st)
	}
	if st.State != "done" || st.Objects != 0 || len(st.Corrupt) != 0 {
		t.Errorf("scrub of the empty bucket: %+v", st)
	}
}

func TestInfoAndSettings(t *testing.T) {
	e := newEnv(t)
	e.login()
	var info map[string]any
	e.call("GET", "/api/v1/info", nil, &info)
	if info["region"] != "us-east-1" || info["s3Endpoint"] != e.s3URL {
		t.Errorf("info: %+v", info)
	}
	if disk, _ := info["disk"].(map[string]any); disk["free"] == nil {
		t.Errorf("disk space missing: %+v", info["disk"])
	}
	var s settingsJSON
	e.call("GET", "/api/v1/settings", nil, &s)
	if s.ShareMaxExpiry != 7*24*3600 {
		t.Errorf("default share expiry %d", s.ShareMaxExpiry)
	}
	if code := e.call("PUT", "/api/v1/settings", map[string]int64{"shareMaxExpirySeconds": 10}, nil); code != 400 {
		t.Errorf("too short accepted: %d", code)
	}
	if s.AuditRetentionDays != audit.DefaultRetentionDays {
		t.Errorf("default audit retention %d", s.AuditRetentionDays)
	}
	// Each setting can change alone.
	if code := e.call("PUT", "/api/v1/settings", map[string]int{"auditRetentionDays": 90}, &s); code != 200 ||
		s.AuditRetentionDays != 90 || s.ShareMaxExpiry != 7*24*3600 {
		t.Errorf("audit retention: %d %+v", code, s)
	}
	if code := e.call("PUT", "/api/v1/settings", map[string]int{"auditRetentionDays": -1}, nil); code != 400 {
		t.Errorf("negative retention accepted: %d", code)
	}
}

func TestGroups(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.login()
	for _, b := range []string{"photos", "inbox"} {
		e.call("POST", "/api/v1/buckets", map[string]string{"name": b}, nil)
	}
	e.call("PUT", "/api/v1/buckets/photos/upload?key=cat.jpg", "meow", nil)

	var key keyJSON
	e.call("POST", "/api/v1/access-keys", map[string]any{"name": "app"}, &key)
	c := s3Client(e.s3URL, key.AccessKey, key.SecretKey)

	var g groupJSON
	if code := e.call("POST", "/api/v1/groups", map[string]any{
		"name": "viewers", "description": "Read photos", "buckets": []string{"photos"}, "readOnly": true,
		"members": []string{key.AccessKey},
	}, &g); code != 201 || !g.Enabled || len(g.Members) != 1 {
		t.Fatalf("create group: %d %+v", code, g)
	}
	for _, bad := range []map[string]any{
		{"name": "bad name!"},
		{"name": "ghosts", "buckets": []string{"nope"}},
		{"name": "strangers", "members": []string{"NOSUCHKEY"}},
		{"name": "viewers"},
	} {
		if code := e.call("POST", "/api/v1/groups", bad, nil); code < 400 {
			t.Errorf("%v accepted: %d", bad, code)
		}
	}

	// The group now decides what the key may do.
	if _, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("cat.jpg")}); err != nil {
		t.Errorf("member cannot read: %v", err)
	}
	_, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("inbox"), Key: aws.String("x"), Body: strings.NewReader("x")})
	if smithyCode(err) != "AccessDenied" {
		t.Errorf("member wrote outside the group: %v", err)
	}
	var list struct{ AccessKeys []keyJSON }
	e.call("GET", "/api/v1/access-keys", nil, &list)
	if len(list.AccessKeys) != 1 || len(list.AccessKeys[0].Groups) != 1 || list.AccessKeys[0].Groups[0] != "viewers" {
		t.Errorf("key groups: %+v", list.AccessKeys)
	}

	// Widen the group; then disable it.
	e.call("PATCH", "/api/v1/groups/viewers", map[string]any{"buckets": []string{"photos", "inbox"}, "readOnly": false}, &g)
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("inbox"), Key: aws.String("x"), Body: strings.NewReader("x")}); err != nil {
		t.Errorf("widened group: %v", err)
	}
	e.call("PATCH", "/api/v1/groups/viewers", map[string]any{"enabled": false}, nil)
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("cat.jpg")})
	if smithyCode(err) != "AccessDenied" {
		t.Errorf("disabled group still grants: %v", err)
	}

	// Membership can also be set from the key; deleting the group frees the key.
	if code := e.call("PATCH", "/api/v1/access-keys/"+key.AccessKey, map[string]any{"groups": []string{"ghost"}}, nil); code != 400 {
		t.Errorf("unknown group accepted: %d", code)
	}
	if code := e.call("DELETE", "/api/v1/groups/viewers", nil, nil); code != 204 {
		t.Errorf("delete group: %d", code)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("photos"), Key: aws.String("y"), Body: strings.NewReader("y")}); err != nil {
		t.Errorf("own full scope not back: %v", err)
	}
	var groups struct{ Groups []groupJSON }
	if e.call("GET", "/api/v1/groups", nil, &groups); len(groups.Groups) != 0 {
		t.Errorf("groups left: %+v", groups)
	}
}

func TestAuditLog(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	// A failed sign-in does not record the key that was typed.
	e.call("POST", "/api/v1/login", loginRequest{"typo", "x"}, nil)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "logs"}, nil)
	var key keyJSON
	e.call("POST", "/api/v1/access-keys", map[string]any{"name": "writer", "buckets": []string{"logs"}}, &key)
	c := s3Client(e.s3URL, key.AccessKey, key.SecretKey)
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("logs"), Key: aws.String("a.txt"), Body: strings.NewReader("a")})
	c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("logs"), Key: aws.String("a.txt")}) // reads are not recorded
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("other")})                    // refused: recorded

	type page struct {
		Entries    []auditJSON
		NextBefore int64
	}
	var all page
	if code := e.call("GET", "/api/v1/audit", nil, &all); code != 200 {
		t.Fatalf("list audit: %d", code)
	}
	var got []string
	for _, en := range all.Entries {
		got = append(got, fmt.Sprintf("%s %s %s %d", en.Source, en.Actor, en.Action, en.Status))
	}
	want := []string{
		"s3 " + key.AccessKey + " CreateBucket 403",
		"s3 " + key.AccessKey + " PutObject 200",
		"console " + rootAK + " CreateAccessKey 201",
		"console " + rootAK + " CreateBucket 201",
		"console " + rootAK + " Login 200",
		"console (unknown) Login 401",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if all.Entries[1].Bucket != "logs" || all.Entries[1].Resource != "a.txt" || all.Entries[0].Error != "AccessDenied" {
		t.Errorf("S3 entry details: %+v %+v", all.Entries[1], all.Entries[0])
	}
	if all.Entries[2].Resource != key.AccessKey || all.Entries[3].Bucket != "logs" || all.Entries[5].Error != "invalid_credentials" {
		t.Errorf("console entry details: %+v", all.Entries)
	}

	var filtered page
	e.call("GET", "/api/v1/audit?source=console&failed=true", nil, &filtered)
	if len(filtered.Entries) != 1 || filtered.Entries[0].Action != "Login" {
		t.Errorf("filtered: %+v", filtered.Entries)
	}
	var first, second page
	e.call("GET", "/api/v1/audit?limit=4", nil, &first)
	e.call("GET", fmt.Sprintf("/api/v1/audit?limit=4&before=%d", first.NextBefore), nil, &second)
	if len(first.Entries) != 4 || first.NextBefore == 0 || len(second.Entries) != 2 || second.NextBefore != 0 {
		t.Errorf("paging: %d entries next %d, then %d next %d", len(first.Entries), first.NextBefore, len(second.Entries), second.NextBefore)
	}
	if code := e.call("GET", "/api/v1/audit?limit=9999", nil, nil); code != 400 {
		t.Errorf("huge limit accepted: %d", code)
	}
}

func urlQuery(s string) string {
	return strings.NewReplacer(" ", "%20", "/", "%2F").Replace(s)
}

func smithyCode(err error) string {
	var apiErr smithy.APIError
	if ok := asAPIError(err, &apiErr); ok {
		return apiErr.ErrorCode()
	}
	return ""
}
