package admin

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"bin/internal/auth"
	"bin/internal/s3"
	"bin/internal/s3/sigv4"
	"bin/internal/secrets"
	"bin/internal/storage"
)

const remoteAK, remoteSK = "remoteaccess", "remotesecret-long"

// newRemote starts a second, separate S3 server to replicate to and from.
// The SSRF guard is lifted for the test: the remote listens on 127.0.0.1.
func newRemote(t *testing.T) (*httptest.Server, *awss3.Client) {
	t.Helper()
	old := dialGuard
	dialGuard = func(string, string, syscall.RawConn) error { return nil }
	t.Cleanup(func() { dialGuard = old })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{5}, 32))
	keys := &auth.Keyring{RootAccessKey: remoteAK, RootSecretKey: remoteSK, Store: store, Box: box, Log: log}
	srv := httptest.NewServer(s3.NewHandler(s3.Options{
		Store: store, Region: "us-east-1", HostID: "r", Owner: s3.Owner{ID: "o", DisplayName: "remote"},
		Auth: &sigv4.Verifier{Region: "us-east-1", Store: keys}, Keys: keys, Log: log,
	}))
	t.Cleanup(srv.Close)
	return srv, s3Client(srv.URL, remoteAK, remoteSK)
}

func remoteKeys(t *testing.T, c *awss3.Client, bucket string) map[string]string {
	t.Helper()
	out, err := c.ListObjectsV2(context.Background(), &awss3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, o := range out.Contents {
		keys[*o.Key] = ""
		got, err := c.GetObject(context.Background(), &awss3.GetObjectInput{Bucket: aws.String(bucket), Key: o.Key})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(got.Body)
		got.Body.Close()
		keys[*o.Key] = string(b)
	}
	return keys
}

// runRule starts a run and waits for it to finish, returning the rule after.
func (e *env) runRule(id string) replicationRuleJSON {
	e.t.Helper()
	if code := e.call("POST", "/api/v1/replication/rules/"+id+"/run", nil, nil); code != 202 {
		e.t.Fatalf("run: %d", code)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var list struct{ Rules []replicationRuleJSON }
		e.call("GET", "/api/v1/replication/rules", nil, &list)
		for _, r := range list.Rules {
			if r.ID == id && !r.Running && r.LastRunAt != nil {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("run did not finish")
	return replicationRuleJSON{}
}

func TestReplicationPush(t *testing.T) {
	remote, rc := newRemote(t)
	ctx := context.Background()
	rc.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("backup")})
	rc.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("backup"), Key: aws.String("bin/manual.txt"), Body: strings.NewReader("not ours")})

	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "photos"}, nil)
	e.call("PUT", "/api/v1/buckets/photos/upload?key=a.txt", "first", nil)
	e.call("PUT", "/api/v1/buckets/photos/upload?key=dir%2Fb.txt", "bee", nil)

	var rule replicationRuleJSON
	if code := e.call("POST", "/api/v1/replication/rules", map[string]any{
		"name": "offsite", "direction": "push", "bucket": "photos", "endpoint": remote.URL,
		"accessKey": remoteAK, "secretKey": remoteSK, "region": "us-east-1",
		"remoteBucket": "backup", "remotePrefix": "bin", "intervalSeconds": 300, "mirrorDeletes": true, "enabled": false,
	}, &rule); code != 201 || !rule.SecretKeySet || rule.SecretKey != "" || rule.RemotePrefix != "bin/" {
		t.Fatalf("create: %d %+v", code, rule)
	}

	r := e.runRule(rule.ID)
	if r.LastStatus != "ok" || r.LastCopied != 2 {
		t.Fatalf("first run: %+v", r)
	}
	if got := remoteKeys(t, rc, "backup"); got["bin/a.txt"] != "first" || got["bin/dir/b.txt"] != "bee" {
		t.Fatalf("remote after first run: %v", got)
	}

	if r := e.runRule(rule.ID); r.LastStatus != "ok" || r.LastCopied != 0 {
		t.Errorf("unchanged run copied: %+v", r)
	}

	e.call("PUT", "/api/v1/buckets/photos/upload?key=a.txt", "second", nil)
	e.call("DELETE", "/api/v1/buckets/photos/objects", map[string]any{"keys": []string{"dir/b.txt"}}, nil)
	r = e.runRule(rule.ID)
	if r.LastStatus != "ok" || r.LastCopied != 1 || r.LastDeleted != 1 || r.TotalCopied != 3 {
		t.Fatalf("third run: %+v", r)
	}
	got := remoteKeys(t, rc, "backup")
	if got["bin/a.txt"] != "second" || got["bin/manual.txt"] != "not ours" {
		t.Errorf("remote after changes: %v", got)
	}
	if _, ok := got["bin/dir/b.txt"]; ok {
		t.Errorf("deleted object still on the remote: %v", got)
	}
}

func TestReplicationPull(t *testing.T) {
	remote, rc := newRemote(t)
	ctx := context.Background()
	rc.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("incoming")})
	for k, v := range map[string]string{"x.txt": "ex", "y.txt": "why"} {
		rc.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("incoming"), Key: aws.String(k), Body: strings.NewReader(v), ContentType: aws.String("text/plain")})
	}

	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "inbox"}, nil)
	var rule replicationRuleJSON
	e.call("POST", "/api/v1/replication/rules", map[string]any{
		"name": "partner feed", "direction": "pull", "bucket": "inbox", "prefix": "partner/", "endpoint": remote.URL,
		"accessKey": remoteAK, "secretKey": remoteSK, "remoteBucket": "incoming", "intervalSeconds": 60,
		"mirrorDeletes": true, "enabled": false,
	}, &rule)

	if r := e.runRule(rule.ID); r.LastStatus != "ok" || r.LastCopied != 2 {
		t.Fatalf("first pull: %+v", r)
	}
	var obj objectJSON
	e.call("GET", "/api/v1/buckets/inbox/object?key=partner%2Fx.txt", nil, &obj)
	if obj.Size != 2 || obj.ContentType != "text/plain" {
		t.Errorf("pulled object: %+v", obj)
	}

	// Gone on the remote: removed here. Changed here: kept, it is not ours anymore.
	rc.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("incoming"), Key: aws.String("x.txt")})
	rc.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("incoming"), Key: aws.String("y.txt")})
	e.call("PUT", "/api/v1/buckets/inbox/upload?key=partner%2Fy.txt", "edited here", nil)
	if r := e.runRule(rule.ID); r.LastStatus != "ok" || r.LastDeleted != 1 {
		t.Fatalf("second pull: %+v", r)
	}
	if code := e.call("GET", "/api/v1/buckets/inbox/object?key=partner%2Fx.txt", nil, nil); code != 404 {
		t.Errorf("x.txt should be gone: %d", code)
	}
	if code := e.call("GET", "/api/v1/buckets/inbox/object?key=partner%2Fy.txt", nil, nil); code != 200 {
		t.Errorf("locally edited y.txt should stay: %d", code)
	}
}

func TestReplicationRuleValidation(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "photos"}, nil)
	base := map[string]any{
		"name": "r", "direction": "push", "bucket": "photos", "endpoint": "https://s3.example.com",
		"accessKey": "a", "secretKey": "b", "remoteBucket": "backup", "intervalSeconds": 300,
	}
	for field, bad := range map[string]any{
		"name": "", "direction": "both", "bucket": "missing", "endpoint": "s3.example.com",
		"remoteBucket": "x", "intervalSeconds": 10, "secretKey": "",
	} {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		body[field] = bad
		var out map[string]string
		if code := e.call("POST", "/api/v1/replication/rules", body, &out); code != 400 || out["field"] != field {
			t.Errorf("%s=%v: %d %v", field, bad, code, out)
		}
	}
	// A failing remote is reported on the rule, without echoing the remote's answer.
	var rule replicationRuleJSON
	base["endpoint"] = "http://127.0.0.1:1"
	e.call("POST", "/api/v1/replication/rules", base, &rule)
	if r := e.runRule(rule.ID); r.LastStatus != "error" || !strings.Contains(r.LastError, "loopback") {
		t.Errorf("unreachable remote: %+v", r)
	}
}
