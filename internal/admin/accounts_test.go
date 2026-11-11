package admin

import (
	"net/http/httptest"
	"testing"
	"time"

	"bin/internal/auth"
)

// newUser creates a key with a console password and returns it signed in.
func newUser(t *testing.T, e *env, body map[string]any) (keyJSON, *env) {
	t.Helper()
	body["password"] = "user-password-1"
	var key keyJSON
	if code := e.call("POST", "/api/v1/access-keys", body, &key); code != 201 {
		t.Fatalf("create key: %d", code)
	}
	u := e.asUser()
	if code := u.signIn(key.AccessKey, "user-password-1"); code != 200 {
		t.Fatalf("sign-in: %d", code)
	}
	return key, u
}

// Users who may create buckets are bounded by the settings, get the quota,
// and may delete only their own buckets, once empty.
func TestUserBucketLimits(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("PUT", "/api/v1/settings", map[string]any{"userBucketLimit": 2, "userBucketQuotaBytes": 1 << 20}, nil)
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "admins"}, nil)
	_, u := newUser(t, e, map[string]any{"name": "maker", "buckets": []string{"admins"}, "canCreateBuckets": true})

	for _, name := range []string{"first-one", "second-one"} {
		if code := u.call("POST", "/api/v1/buckets", map[string]string{"name": name}, nil); code != 201 {
			t.Fatalf("create %s: %d", name, code)
		}
	}
	if code := u.call("POST", "/api/v1/buckets", map[string]string{"name": "third-one"}, nil); code != 403 {
		t.Errorf("past the limit: %d", code)
	}
	var b bucketJSON
	u.call("GET", "/api/v1/buckets/first-one", nil, &b)
	if b.QuotaBytes != 1<<20 || !b.Owned {
		t.Errorf("user bucket: %+v", b)
	}

	u.call("PUT", "/api/v1/buckets/first-one/upload?key=a.txt", "a", nil)
	if code := u.call("DELETE", "/api/v1/buckets/first-one", nil, nil); code != 409 {
		t.Errorf("delete a non-empty own bucket: %d", code)
	}
	if code := u.call("DELETE", "/api/v1/buckets/first-one?force=true", nil, nil); code != 403 {
		t.Errorf("force delete by a user: %d", code)
	}
	if code := u.call("DELETE", "/api/v1/buckets/second-one", nil, nil); code != 204 {
		t.Errorf("delete own empty bucket: %d", code)
	}
	if code := u.call("DELETE", "/api/v1/buckets/admins", nil, nil); code != 403 {
		t.Errorf("delete a bucket it did not create: %d", code)
	}
}

// Failures against one account lock it, even when no single IP failed often:
// guesses spread over many addresses still count against the account.
func TestAccountLockout(t *testing.T) {
	h := &Handler{limiter: newLoginLimiter()}
	for i := range maxLoginFailures {
		h.limiter.fail("203.0.113." + string(rune('0'+i))) // a different IP each time: none is blocked
		h.limiter.fail(accountLimit("target"))
	}
	if h.limiter.blocked("203.0.113.1") > 0 {
		t.Fatal("an IP with one failure is blocked")
	}
	w := httptest.NewRecorder()
	if !h.accountBlocked(w, "target") || w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Errorf("account after %d failures from different IPs: %d", maxLoginFailures, w.Code)
	}
	if h.accountBlocked(httptest.NewRecorder(), "someone-else") {
		t.Error("another account is blocked too")
	}

	// End to end: a locked account refuses even the right password.
	e := newEnv(t)
	e.login()
	key, _ := newUser(t, e, map[string]any{"name": "target"})
	for range maxLoginFailures {
		e.asUser().signIn(key.AccessKey, "wrong-password")
	}
	if code := e.asUser().signIn(key.AccessKey, "user-password-1"); code != 429 {
		t.Errorf("right password on a locked account: %d", code)
	}
}

func TestSessionsListAndEnd(t *testing.T) {
	e := newEnv(t)
	e.login()
	key, u := newUser(t, e, map[string]any{"name": "ana"})
	other := e.asUser()
	other.signIn(key.AccessKey, "user-password-1")

	type list struct {
		Sessions []sessionInfoJSON
	}
	var mine list
	u.call("GET", "/api/v1/sessions", nil, &mine)
	if len(mine.Sessions) != 2 {
		t.Fatalf("user sees %d sessions, want their 2", len(mine.Sessions))
	}
	var all list
	e.call("GET", "/api/v1/sessions", nil, &all)
	if len(all.Sessions) != 3 {
		t.Errorf("admin sees %d sessions, want 3", len(all.Sessions))
	}
	var adminID, otherID string
	for _, s := range all.Sessions {
		if s.Role == "admin" {
			adminID = s.ID
		}
	}
	for _, s := range mine.Sessions {
		if !s.Current {
			otherID = s.ID
		}
	}
	if code := u.call("DELETE", "/api/v1/sessions/"+adminID, nil, nil); code != 404 {
		t.Errorf("user ends an admin session: %d", code)
	}
	if code := u.call("DELETE", "/api/v1/sessions/"+otherID, nil, nil); code != 204 {
		t.Fatalf("user ends their other session: %d", code)
	}
	if code := other.call("GET", "/api/v1/buckets", nil, nil); code != 401 {
		t.Errorf("ended session still works: %d", code)
	}
}

func TestSecondFactor(t *testing.T) {
	e := newEnv(t)
	e.login()
	key, u := newUser(t, e, map[string]any{"name": "careful"})

	var setup struct{ Secret, URL string }
	if code := u.call("POST", "/api/v1/account/totp/setup", nil, &setup); code != 200 || setup.Secret == "" {
		t.Fatalf("setup: %d %+v", code, setup)
	}
	if code := u.call("POST", "/api/v1/account/totp/enable", map[string]string{"code": "000000"}, nil); code != 400 {
		t.Errorf("enable with a wrong code: %d", code)
	}
	code, _ := auth.TOTPCodeAt(setup.Secret, time.Now())
	if c := u.call("POST", "/api/v1/account/totp/enable", map[string]string{"code": code}, nil); c != 204 {
		t.Fatalf("enable: %d", c)
	}

	// Signing in now needs a code; the one used to enable it is spent.
	login := func(totp string) (int, string) {
		var out map[string]any
		c := e.asUser().call("POST", "/api/v1/login", map[string]string{
			"accessKey": key.AccessKey, "secretKey": "user-password-1", "totp": totp}, &out)
		errCode, _ := out["error"].(string)
		return c, errCode
	}
	if c, why := login(""); c != 401 || why != "totp_required" {
		t.Errorf("without a code: %d %s", c, why)
	}
	if c, why := login(code); c != 401 || why != "invalid_totp" {
		t.Errorf("reusing the code: %d %s", c, why)
	}
	next, _ := auth.TOTPCodeAt(setup.Secret, time.Now().Add(30*time.Second))
	if c, why := login(next); c != 200 {
		t.Errorf("with a fresh code: %d %s", c, why)
	}
	// The secret key cannot reset the password around the second factor.
	var keyFull keyJSON
	e.call("POST", "/api/v1/access-keys", map[string]any{"name": "x"}, &keyFull) // unrelated, keeps ids distinct
	if c := e.asUser().call("POST", "/api/v1/login/activate", map[string]string{
		"accessKey": key.AccessKey, "secretKey": key.SecretKey, "password": "brand-new-password"}, nil); c != 401 {
		t.Errorf("activation without a code: %d", c)
	}
	// An admin removes it (lost phone): password alone works again.
	if c := e.call("DELETE", "/api/v1/access-keys/"+key.AccessKey+"/totp", nil, nil); c != 204 {
		t.Fatalf("admin removes it: %d", c)
	}
	if c, why := login(""); c != 200 {
		t.Errorf("after removal: %d %s", c, why)
	}
}
