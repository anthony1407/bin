package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/secure-io/sio-go"
	"golang.org/x/crypto/argon2"

	"bin/internal/auth"
)

func TestScopeFromIAM(t *testing.T) {
	cases := []struct {
		name, doc string
		want      auth.Scope
		ok        bool
	}{
		{"readwrite", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::*"]}]}`,
			auth.Scope{}, true},
		{"readonly", `{"Statement":[{"Effect":"Allow","Action":["s3:GetBucketLocation","s3:GetObject"],"Resource":["arn:aws:s3:::*"]}]}`,
			auth.Scope{ReadOnly: true}, true},
		{"one bucket, account-level actions on *", `{"Statement":[
			{"Effect":"Allow","Action":["s3:ListAllMyBuckets","s3:GetBucketLocation"],"Resource":["arn:aws:s3:::*"]},
			{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::photos","arn:aws:s3:::photos/*"]}]}`,
			auth.Scope{Buckets: []string{"photos"}}, true},
		{"read two buckets", `{"Statement":{"Effect":"Allow","Action":"s3:GetObject","Resource":["arn:aws:s3:::b/*","arn:aws:s3:::a/*"]}}`,
			auth.Scope{Buckets: []string{"a", "b"}, ReadOnly: true}, true},
		{"admin only", `{"Statement":[{"Effect":"Allow","Action":["admin:*"]}]}`, auth.Scope{}, false},
		{"writeonly", `{"Statement":[{"Effect":"Allow","Action":["s3:PutObject"],"Resource":["arn:aws:s3:::*"]}]}`, auth.Scope{}, false},
		{"deny", `{"Statement":[{"Effect":"Deny","Action":["s3:*"],"Resource":["*"]}]}`, auth.Scope{}, false},
		{"folder", `{"Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::b/home/*"]}]}`, auth.Scope{}, false},
		{"bucket wildcard", `{"Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::logs-*"]}]}`, auth.Scope{}, false},
		{"condition", `{"Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["*"],"Condition":{"IpAddress":{"aws:SourceIp":"10.0.0.0/8"}}}]}`, auth.Scope{}, false},
		{"mixed levels", `{"Statement":[
			{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::a/*"]},
			{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/*"]}]}`, auth.Scope{}, false},
	}
	for _, c := range cases {
		got, reason, ok := scopeFromIAM([]byte(c.doc))
		if ok != c.ok || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%s: got %+v ok=%v (%s), want %+v ok=%v", c.name, got, ok, reason, c.want, c.ok)
		}
		if !ok && reason == "" {
			t.Errorf("%s: refused without a reason", c.name)
		}
	}
}

func TestAnonymousPolicy(t *testing.T) {
	download := `{"Version":"2012-10-17","Statement":[
		{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetBucketLocation","s3:ListBucket"],"Resource":["arn:aws:s3:::b"]},
		{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/*"]}]}`
	if p, _, ok := anonymousPolicy("b", download); !ok || p.Mode != "public" {
		t.Errorf("download: %+v %v", p, ok)
	}
	prefix := `{"Statement":[
		{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::b"],"Condition":{"StringEquals":{"s3:prefix":["pub"]}}},
		{"Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/pub*"]},
		{"Effect":"Allow","Principal":"*","Action":["s3:PutObject"],"Resource":["arn:aws:s3:::b/inbox/*"]},
		{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/x"]},"Action":["s3:*"],"Resource":["arn:aws:s3:::b/*"]}]}`
	p, notes, ok := anonymousPolicy("b", prefix)
	want := []auth.Rule{{Prefix: "inbox/", Access: auth.WriteOnly}, {Prefix: "pub", Access: auth.ReadOnly}}
	if !ok || p.Mode != "custom" || !reflect.DeepEqual(p.Rules, want) || len(notes) == 0 {
		t.Errorf("prefixes: %+v %v %v", p, notes, ok)
	}
	if _, _, ok := anonymousPolicy("b", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":["s3:GetObject","s3:PutObject"],"Resource":["arn:aws:s3:::b/*"]}]}`); ok {
		t.Error("read+write without delete was accepted")
	}
}

// encryptAdminData is the server side of decryptAdminData (argon2id + AES-GCM).
func encryptAdminData(t *testing.T, password string, data []byte) []byte {
	t.Helper()
	salt := make([]byte, 32)
	rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	stream, err := sio.AES_256_GCM.Stream(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, stream.NonceSize())
	rand.Read(nonce)
	var out bytes.Buffer
	out.Write(salt)
	out.WriteByte(adminArgon2idAESGCM)
	out.Write(nonce)
	w := stream.EncryptWriter(&out, nonce, nil)
	w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestDecryptAdminData(t *testing.T) {
	enc := encryptAdminData(t, "secret", []byte(`{"a":1}`))
	if got, err := decryptAdminData("secret", enc); err != nil || string(got) != `{"a":1}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := decryptAdminData("wrong", enc); err == nil {
		t.Fatal("decrypted with the wrong password")
	}
}

// fakeMinIO answers the admin endpoints the import reads, as MinIO would.
func fakeMinIO(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	policies := map[string]json.RawMessage{
		"readwrite": json.RawMessage(`{"Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::*"]}]}`),
		"photos-ro": json.RawMessage(`{"Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::photos","arn:aws:s3:::photos/*"]}]}`),
		"docs-rw":   json.RawMessage(`{"Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::docs/*"]}]}`),
		"folder":    json.RawMessage(`{"Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::docs/home/*"]}]}`),
	}
	users := map[string]minioUser{
		"alice": {PolicyName: "photos-ro", Status: "enabled"},
		"bob":   {Status: "enabled", MemberOf: []string{"editors"}},
		"carol": {PolicyName: "folder", Status: "enabled"},
		"dave":  {PolicyName: "photos-ro,docs-rw", Status: "disabled"},
	}
	groups := map[string]minioGroup{
		"editors": {Members: []string{"bob"}, Policy: "docs-rw", Status: "enabled"},
	}
	accounts := map[string][]minioServiceAccount{
		"alice": {{AccessKey: "alice-app", ParentUser: "alice", AccountStatus: "on", ImpliedPolicy: true}},
		"bob": {{AccessKey: "bob-ci", ParentUser: "bob", AccountStatus: "on",
			Policy: `{"Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::docs/*"]}]}`}},
	}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any, encrypted bool) {
		b, _ := json.Marshal(v)
		if encrypted {
			b = encryptAdminData(t, secret, b)
		}
		w.Write(b)
	}
	mux.HandleFunc("GET /minio/admin/v4/list-canned-policies", func(w http.ResponseWriter, r *http.Request) { reply(w, policies, false) })
	mux.HandleFunc("GET /minio/admin/v4/list-users", func(w http.ResponseWriter, r *http.Request) { reply(w, users, true) })
	mux.HandleFunc("GET /minio/admin/v4/groups", func(w http.ResponseWriter, r *http.Request) { reply(w, []string{"editors"}, false) })
	mux.HandleFunc("GET /minio/admin/v4/group", func(w http.ResponseWriter, r *http.Request) {
		reply(w, groups[r.URL.Query().Get("group")], false)
	})
	mux.HandleFunc("GET /minio/admin/v4/list-service-accounts", func(w http.ResponseWriter, r *http.Request) {
		var list []minioServiceAccount
		for _, sa := range accounts[r.URL.Query().Get("user")] {
			sa.Policy = "" // the listing leaves it out
			list = append(list, sa)
		}
		reply(w, map[string]any{"accounts": list}, true)
	})
	mux.HandleFunc("GET /minio/admin/v4/info-service-account", func(w http.ResponseWriter, r *http.Request) {
		for _, list := range accounts {
			for _, sa := range list {
				if sa.AccessKey == r.URL.Query().Get("accessKey") {
					reply(w, sa, true)
					return
				}
			}
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestImportIAM(t *testing.T) {
	old := dialGuard
	dialGuard = func(string, string, syscall.RawConn) error { return nil }
	t.Cleanup(func() { dialGuard = old })
	srv := fakeMinIO(t, "minio-secret")

	e := newEnv(t)
	e.login()
	for _, b := range []string{"photos", "docs"} {
		e.call("POST", "/api/v1/buckets", map[string]string{"name": b}, nil)
	}
	var out struct {
		IAM      iamImportResult `json:"iam"`
		IAMError string          `json:"iamError"`
	}
	if code := e.call("POST", "/api/v1/import/start", map[string]any{
		"endpoint": srv.URL, "accessKey": "minio", "secretKey": "minio-secret", "region": "us-east-1", "iam": true,
	}, &out); code != 200 || out.IAMError != "" {
		t.Fatalf("start: %d %q", code, out.IAMError)
	}

	keys := map[string]importedKey{}
	for _, k := range out.IAM.Keys {
		if k.SecretKey == "" {
			t.Errorf("%s: no secret", k.AccessKey)
		}
		keys[k.AccessKey] = k
	}
	check := func(name string, enabled bool, groups ...string) {
		t.Helper()
		k, ok := keys[name]
		if groups == nil {
			groups = []string{}
		}
		if !ok || k.Enabled != enabled || !reflect.DeepEqual(k.Groups, groups) {
			t.Errorf("%s: %+v (created %v)", name, k, ok)
		}
	}
	check("alice", true, "policy-photos-ro")
	check("bob", true, "editors")
	check("dave", false, "policy-docs-rw", "policy-photos-ro") // disabled in MinIO
	check("carol", false)                                       // its only policy is untranslatable
	check("alice-app", true, "policy-photos-ro")                // implied: its user's access
	check("bob-ci", true)                                       // own scope: docs, read-only

	var groups struct{ Groups []groupJSON }
	e.call("GET", "/api/v1/groups", nil, &groups)
	got := map[string]groupJSON{}
	for _, g := range groups.Groups {
		got[g.Name] = g
	}
	if g := got["policy-photos-ro"]; !reflect.DeepEqual(g.Buckets, []string{"photos"}) || !g.ReadOnly {
		t.Errorf("policy-photos-ro: %+v", g)
	}
	if g := got["editors"]; !reflect.DeepEqual(g.Buckets, []string{"docs"}) || g.ReadOnly || !reflect.DeepEqual(g.Members, []string{"bob"}) {
		t.Errorf("editors: %+v", g)
	}
	var key keyJSON
	e.call("GET", "/api/v1/access-keys", nil, nil)
	var list struct{ AccessKeys []keyJSON }
	e.call("GET", "/api/v1/access-keys", nil, &list)
	for _, k := range list.AccessKeys {
		if k.AccessKey == "bob-ci" {
			key = k
		}
	}
	if !reflect.DeepEqual(key.Buckets, []string{"docs"}) || !key.ReadOnly {
		t.Errorf("bob-ci scope: %+v", key)
	}
	skipped := map[string]bool{}
	for _, s := range out.IAM.Skipped {
		skipped[s.Kind+":"+s.Name] = true
	}
	if !skipped["policy:folder"] {
		t.Errorf("the folder policy was not reported: %+v", out.IAM.Skipped)
	}

	// A second run finds everything in place and changes nothing.
	out.IAM = iamImportResult{}
	e.call("POST", "/api/v1/import/start", map[string]any{
		"endpoint": srv.URL, "accessKey": "minio", "secretKey": "minio-secret", "iam": true,
	}, &out)
	if len(out.IAM.Keys) != 0 || len(out.IAM.Groups) != 0 {
		t.Errorf("second run created: %+v", out.IAM)
	}
}

// Bucket settings come along with the objects when asked for.
func TestImportBucketSettings(t *testing.T) {
	remote, rc := newRemote(t)
	ctx := context.Background()
	rc.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("conf")})
	rc.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{Bucket: aws.String("conf"),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}})
	rc.PutBucketTagging(ctx, &awss3.PutBucketTaggingInput{Bucket: aws.String("conf"),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("team"), Value: aws.String("data")}}}})

	e := newEnv(t)
	e.login()
	var out struct{ Jobs []importJob }
	if code := e.call("POST", "/api/v1/import/start", map[string]any{
		"endpoint": remote.URL, "accessKey": remoteAK, "secretKey": remoteSK, "region": "us-east-1",
		"buckets": []string{"conf"}, "settings": true,
	}, &out); code != 200 || len(out.Jobs) != 1 {
		t.Fatalf("start: %d %+v", code, out)
	}
	if s := strings.Join(out.Jobs[0].Settings, ","); !strings.Contains(s, "versioning enabled") || !strings.Contains(s, "1 tag") {
		t.Errorf("settings: %q notes %q", s, out.Jobs[0].Notes)
	}
	var b bucketJSON
	e.call("GET", "/api/v1/buckets/conf", nil, &b)
	if b.Versioning != "Enabled" || b.Tags["team"] != "data" {
		t.Errorf("bucket: %+v", b)
	}
	time.Sleep(100 * time.Millisecond) // let the (empty) copy job finish before cleanup
}
