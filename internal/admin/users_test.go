package admin

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// asUser is a second browser: its own cookies and CSRF token.
func (e *env) asUser() *env {
	jar, _ := cookiejar.New(nil)
	return &env{t: e.t, console: e.console, s3URL: e.s3URL, dir: e.dir, client: &http.Client{Jar: jar}}
}

// signIn signs u in with an access key and a console password.
func (u *env) signIn(ak, password string) int {
	u.t.Helper()
	var s sessionResponse
	code := u.call("POST", "/api/v1/login", loginRequest{ak, password}, &s)
	u.csrf = s.CSRFToken
	return code
}

func TestUserConsoleAccess(t *testing.T) {
	e := newEnv(t)
	e.login()
	for _, b := range []string{"mine", "other"} {
		e.call("POST", "/api/v1/buckets", map[string]string{"name": b}, nil)
	}
	e.call("PUT", "/api/v1/buckets/other/upload?key=secret.txt", "not yours", nil)
	var key keyJSON
	if code := e.call("POST", "/api/v1/access-keys", map[string]any{"name": "ana", "buckets": []string{"mine"}}, &key); code != 201 {
		t.Fatalf("create key: %d", code)
	}

	u := e.asUser()
	// No console password yet: the S3 secret is not one.
	if code := u.signIn(key.AccessKey, key.SecretKey); code != 401 {
		t.Fatalf("sign-in with the secret key: %d", code)
	}
	activate := func(secret, password string) int {
		var s sessionResponse
		code := u.call("POST", "/api/v1/login/activate", map[string]string{
			"accessKey": key.AccessKey, "secretKey": secret, "password": password}, &s)
		u.csrf = s.CSRFToken
		return code
	}
	if code := activate("wrong-secret", "a-good-password"); code != 401 {
		t.Errorf("activation with a wrong secret: %d", code)
	}
	if code := activate(key.SecretKey, "short"); code != 400 {
		t.Errorf("activation with a short password: %d", code)
	}
	if code := activate(key.SecretKey, "a-good-password"); code != 200 {
		t.Fatalf("activation: %d", code)
	}
	var sess sessionResponse
	u.call("GET", "/api/v1/session", nil, &sess)
	if sess.Role != "user" || sess.User != key.AccessKey || sess.CanCreateBuckets {
		t.Errorf("session: %+v", sess)
	}

	// Their bucket, and nothing else.
	var list struct{ Buckets []bucketJSON }
	u.call("GET", "/api/v1/buckets", nil, &list)
	if len(list.Buckets) != 1 || list.Buckets[0].Name != "mine" || list.Buckets[0].Access != "write" {
		t.Errorf("buckets seen by the user: %+v", list.Buckets)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/buckets/other"},
		{"GET", "/api/v1/buckets/other/objects"},
		{"GET", "/api/v1/buckets/other/download?key=secret.txt"},
		{"PUT", "/api/v1/buckets/other/upload?key=x.txt"},
		{"GET", "/api/v1/buckets/missing/objects"}, // no hint that it does not exist
		{"PUT", "/api/v1/buckets/mine/policy"},     // a bucket setting: admins only
		{"DELETE", "/api/v1/buckets/mine"},
		{"GET", "/api/v1/access-keys"},
		{"GET", "/api/v1/groups"},
		{"GET", "/api/v1/settings"},
		{"GET", "/api/v1/audit"},
		{"GET", "/api/v1/logs"},
		{"GET", "/api/v1/replication/rules"},
		{"GET", "/api/v1/dropzones"},
		{"POST", "/api/v1/buckets"}, // not allowed to create buckets yet
	} {
		var body any
		if c.method != "GET" {
			body = map[string]any{"name": "newbucket"}
		}
		if code := u.call(c.method, c.path, body, nil); code != 403 {
			t.Errorf("user %s %s: %d, want 403", c.method, c.path, code)
		}
	}
	if code := u.call("PUT", "/api/v1/buckets/mine/upload?key=hello.txt", "hi", nil); code != 201 && code != 200 {
		t.Errorf("upload to their bucket: %d", code)
	}
	if code := u.call("GET", "/api/v1/buckets/mine/objects", nil, nil); code != 200 {
		t.Errorf("list their bucket: %d", code)
	}
	// Copying into a bucket they cannot write is refused.
	if code := u.call("POST", "/api/v1/buckets/mine/copy", map[string]any{"keys": []string{"hello.txt"}, "targetBucket": "other"}, nil); code != 403 {
		t.Errorf("copy into another bucket: %d", code)
	}
	var info map[string]any
	u.call("GET", "/api/v1/info", nil, &info)
	if _, leaks := info["dataDir"]; leaks || info["s3Endpoint"] == "" {
		t.Errorf("info for users: %v", info)
	}

	// Allowed to create buckets: they own what they create, and the admin sees who did.
	e.call("PATCH", "/api/v1/access-keys/"+key.AccessKey, map[string]any{"canCreateBuckets": true}, nil)
	if code := u.call("POST", "/api/v1/buckets", map[string]any{"name": "anas-bucket"}, nil); code != 201 {
		t.Fatalf("user creates a bucket: %d", code)
	}
	if code := u.call("POST", "/api/v1/buckets", map[string]any{"name": "public-one", "policy": map[string]string{"mode": "public"}}, nil); code != 403 {
		t.Errorf("user creates a public bucket: %d", code)
	}
	u.call("GET", "/api/v1/buckets", nil, &list)
	if len(list.Buckets) != 2 {
		t.Errorf("after creating one: %+v", list.Buckets)
	}
	var b bucketJSON
	e.call("GET", "/api/v1/buckets/anas-bucket", nil, &b)
	if b.CreatedBy != key.AccessKey {
		t.Errorf("created by: %q", b.CreatedBy)
	}

	// Changing their own password keeps this session and ends the others.
	other := e.asUser()
	if code := other.signIn(key.AccessKey, "a-good-password"); code != 200 {
		t.Fatalf("second sign-in: %d", code)
	}
	if code := u.call("POST", "/api/v1/account/password", map[string]string{"currentPassword": "wrong", "password": "another-password"}, nil); code != 400 {
		t.Errorf("change with a wrong current password: %d", code)
	}
	if code := u.call("POST", "/api/v1/account/password", map[string]string{"currentPassword": "a-good-password", "password": "another-password"}, nil); code != 204 {
		t.Fatalf("change own password: %d", code)
	}
	if code := u.call("GET", "/api/v1/buckets", nil, nil); code != 200 {
		t.Errorf("own session after the change: %d", code)
	}
	if code := other.call("GET", "/api/v1/buckets", nil, nil); code != 401 {
		t.Errorf("other session after the change: %d", code)
	}
	if code := e.asUser().signIn(key.AccessKey, "a-good-password"); code != 401 {
		t.Errorf("old password still works: %d", code)
	}

	// An admin removes it: the session ends, S3 access stays.
	if code := e.call("DELETE", "/api/v1/access-keys/"+key.AccessKey+"/password", nil, nil); code != 204 {
		t.Fatalf("clear password: %d", code)
	}
	if code := u.call("GET", "/api/v1/buckets", nil, nil); code != 401 {
		t.Errorf("session after the password was removed: %d", code)
	}
	var keys struct{ AccessKeys []keyJSON }
	e.call("GET", "/api/v1/access-keys", nil, &keys)
	for _, k := range keys.AccessKeys {
		if k.AccessKey == key.AccessKey && (k.ConsolePassword || !k.Enabled) {
			t.Errorf("key after clearing: %+v", k)
		}
	}
	// And sets a new one.
	if code := e.call("PUT", "/api/v1/access-keys/"+key.AccessKey+"/password", map[string]string{"password": "set-by-the-admin"}, nil); code != 204 {
		t.Fatalf("admin sets a password: %d", code)
	}
	u = e.asUser()
	if code := u.signIn(key.AccessKey, "set-by-the-admin"); code != 200 {
		t.Fatalf("sign-in with the admin's password: %d", code)
	}
	// Disabling the key ends the session at once.
	e.call("PATCH", "/api/v1/access-keys/"+key.AccessKey, map[string]any{"enabled": false}, nil)
	if code := u.call("GET", "/api/v1/buckets", nil, nil); code != 401 {
		t.Errorf("session of a disabled key: %d", code)
	}
	if code := e.asUser().signIn(key.AccessKey, "set-by-the-admin"); code != 401 {
		t.Errorf("sign-in of a disabled key: %d", code)
	}
}

func TestReadOnlyUser(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "docs"}, nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=a.txt", "alpha", nil)
	var key keyJSON
	e.call("POST", "/api/v1/access-keys", map[string]any{
		"name": "reader", "buckets": []string{"docs"}, "readOnly": true, "password": "reader-password",
	}, &key)
	if !key.ConsolePassword {
		t.Fatalf("password given at creation not set: %+v", key)
	}
	u := e.asUser()
	if code := u.signIn(key.AccessKey, "reader-password"); code != 200 {
		t.Fatalf("sign-in: %d", code)
	}
	var b bucketJSON
	if code := u.call("GET", "/api/v1/buckets/docs", nil, &b); code != 200 || b.Access != "read" {
		t.Errorf("bucket: %d %+v", code, b)
	}
	if code := u.call("GET", "/api/v1/buckets/docs/download?key=a.txt", nil, nil); code != 200 {
		t.Errorf("download: %d", code)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/v1/buckets/docs/upload?key=b.txt"},
		{"POST", "/api/v1/buckets/docs/trash"},
		{"DELETE", "/api/v1/buckets/docs/objects"},
		{"POST", "/api/v1/buckets/docs/folders"},
		{"POST", "/api/v1/buckets/docs/uploads"},
	} {
		if code := u.call(c.method, c.path, map[string]any{"keys": []string{"a.txt"}, "prefix": "x"}, nil); code != 403 {
			t.Errorf("read-only %s %s: %d", c.method, c.path, code)
		}
	}
	// Moving would take the originals away: refused, even into its own bucket.
	if code := u.call("POST", "/api/v1/buckets/docs/copy", map[string]any{"keys": []string{"a.txt"}, "targetPrefix": "x", "move": true}, nil); code != 403 {
		t.Errorf("read-only move: %d", code)
	}
	// Root is not a user: it cannot use activation to take a password.
	var s map[string]string
	if code := e.asUser().call("POST", "/api/v1/login/activate", map[string]string{
		"accessKey": rootAK, "secretKey": rootSK, "password": "whatever-long"}, &s); code != 401 || strings.Contains(s["message"], "root") {
		t.Errorf("root activation: %d %v", code, s)
	}
}
