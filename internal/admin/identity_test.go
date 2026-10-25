package admin

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIdP is a minimal OpenID Connect provider: discovery, JWKS, an
// authorize endpoint that approves at once, and a token endpoint that checks
// PKCE and signs ID tokens.
type fakeIdP struct {
	*httptest.Server
	key    *rsa.PrivateKey
	groups []string // the groups claim of the next sign-in
	mu     sync.Mutex
	codes  map[string]fakeCode
}

type fakeCode struct{ nonce, challenge string }

func newFakeIdP(t *testing.T) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	idp := &fakeIdP{key: key, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize", "token_endpoint": idp.URL + "/token",
			"jwks_uri": idp.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "bin-console" {
			http.Error(w, "bad request", 400)
			return
		}
		code := randomToken()
		idp.mu.Lock()
		idp.codes[code] = fakeCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		idp.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		idp.mu.Lock()
		c, ok := idp.codes[r.PostForm.Get("code")]
		delete(idp.codes, r.PostForm.Get("code"))
		idp.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		now := time.Now()
		idToken, _ := jwt.Signed(signer).Claims(map[string]any{
			"iss": idp.URL, "sub": "u-1", "aud": "bin-console", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
			"nonce": c.nonce, "preferred_username": "ana", "groups": idp.groups,
		}).Serialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

// browser signs in through OpenID like a browser: following redirects with its own cookies.
func (e *env) openIDSignIn() (*http.Client, *url.URL) {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(e.console + "/api/v1/login/openid?next=/buckets/photos")
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return client, resp.Request.URL
}

func sessionUser(t *testing.T, client *http.Client, console string) string {
	resp, err := client.Get(console + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s sessionResponse
	json.NewDecoder(resp.Body).Decode(&s)
	return s.User
}

func TestOpenIDSignIn(t *testing.T) {
	idp := newFakeIdP(t)
	e := newEnv(t)
	e.login()

	var providers map[string]any
	e.call("GET", "/api/v1/login/providers", nil, &providers)
	if providers["openid"] != nil {
		t.Fatalf("offered before being configured: %v", providers)
	}

	cfg := map[string]any{
		"enabled": true, "configUrl": idp.URL + wellKnownSuffix, "clientId": "bin-console", "clientSecret": "s3cret",
		"claimName": "groups", "adminValue": "bin-admins", "displayName": "Company SSO", "scopes": "openid profile",
	}
	var saved openIDJSON
	if code := e.call("PUT", "/api/v1/identity/openid", cfg, &saved); code != 200 || !saved.SecretSet || saved.ClientSecret != "" {
		t.Fatalf("save: %d %+v", code, saved)
	}
	if saved.DefaultRedirectURI != e.console+oauthCallbackPath {
		t.Errorf("default redirect: %q", saved.DefaultRedirectURI)
	}
	e.call("GET", "/api/v1/login/providers", nil, &providers)
	if p, _ := providers["openid"].(map[string]any); p["displayName"] != "Company SSO" {
		t.Errorf("providers: %v", providers)
	}

	// A member of the admin group gets a session and lands where it was going.
	idp.groups = []string{"dev", "bin-admins"}
	client, final := e.openIDSignIn()
	if final.Path != "/buckets/photos" {
		t.Errorf("landed on %s", final)
	}
	if u := sessionUser(t, client, e.console); u != "openid:ana" {
		t.Errorf("session user %q", u)
	}

	// Anyone else is sent back to the login page with a reason.
	idp.groups = []string{"dev"}
	client, final = e.openIDSignIn()
	if final.Path != "/login" || !strings.Contains(final.Query().Get("error"), "not allowed") {
		t.Errorf("non-admin landed on %s", final)
	}
	if u := sessionUser(t, client, e.console); u != "" {
		t.Errorf("non-admin got a session: %q", u)
	}

	// A callback without the browser's state cookie is refused (login CSRF).
	resp, err := http.Get(e.console + oauthCallbackPath + "?code=x&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("forged callback landed on %s", resp.Request.URL)
	}

	// Errors name the field, so the console shows them under it. An empty
	// form is refused even with sign-in off.
	var bad map[string]string
	if code := e.call("PUT", "/api/v1/identity/openid", map[string]any{"enabled": false}, &bad); code != 400 || bad["field"] != "configUrl" {
		t.Errorf("empty form: %d %v", code, bad)
	}
	bad = nil
	if code := e.call("PUT", "/api/v1/identity/openid", map[string]any{
		"enabled": true, "configUrl": "https://idp.example.com/no-discovery", "clientId": "x", "adminValue": "a",
	}, &bad); code != 400 || bad["field"] != "configUrl" {
		t.Errorf("bad config URL: %d %v", code, bad)
	}
	bad = nil
	if code := e.call("PUT", "/api/v1/identity/openid", map[string]any{
		"enabled": true, "configUrl": idp.URL + wellKnownSuffix, "clientId": "bin-console",
	}, &bad); code != 400 || bad["field"] != "adminValue" {
		t.Errorf("missing admin value: %d %v", code, bad)
	}
}

func TestClaimHasValue(t *testing.T) {
	for _, tc := range []struct {
		claim any
		want  bool
	}{
		{[]any{"dev", "bin-admins"}, true},
		{"bin-admins", true},
		{"dev bin-admins", true},
		{"dev,bin-admins", true},
		{"bin-admins-old", false},
		{[]any{"BIN-ADMINS"}, false},
		{nil, false},
		{42.0, false},
	} {
		if got := claimHasValue(tc.claim, "bin-admins"); got != tc.want {
			t.Errorf("claimHasValue(%v) = %v", tc.claim, got)
		}
	}
	for in, want := range map[string]string{"/buckets": "/buckets", "//evil.com": "/buckets", "https://evil.com": "/buckets", `/\evil.com`: "/buckets", "": "/buckets"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q", in, got)
		}
	}
}

func TestLDAPFilterEscaping(t *testing.T) {
	got := fillFilter("(&(objectClass=person)(uid=%s))", "*)(uid=admin", "")
	if got != `(&(objectClass=person)(uid=\2a\29\28uid=admin))` {
		t.Errorf("username not escaped: %s", got)
	}
	got = fillFilter("(member=%d)", "", "cn=a*,dc=x")
	if got != `(member=cn=a\2a,dc=x)` {
		t.Errorf("DN not escaped: %s", got)
	}
}

func TestLDAPConfigValidation(t *testing.T) {
	ok := ldapConfig{Enabled: true, ServerAddr: "ldap.example.com:636", TLSMode: "tls",
		UserDNSearchBase: "ou=people,dc=example,dc=com", UserDNSearchFilter: "(uid=%s)",
		GroupSearchBase: "ou=groups,dc=example,dc=com", GroupSearchFilter: "(member=%d)",
		AdminGroupDN: "cn=admins,ou=groups,dc=example,dc=com"}
	if _, msg := validateLDAP(ok, true); msg != "" {
		t.Fatalf("valid config refused: %s", msg)
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*ldapConfig)
	}{
		{"no port", "serverAddr", func(c *ldapConfig) { c.ServerAddr = "ldap.example.com" }},
		{"bad tls mode", "tlsMode", func(c *ldapConfig) { c.TLSMode = "maybe" }},
		{"filter no %s", "userDnSearchFilter", func(c *ldapConfig) { c.UserDNSearchFilter = "(uid=admin)" }},
		{"bad DN", "adminGroupDn", func(c *ldapConfig) { c.AdminGroupDN = "not a dn" }},
		{"enabled, empty", "groupSearchBase", func(c *ldapConfig) { c.GroupSearchBase = "" }},
	} {
		c := ok
		tc.mutate(&c)
		if field, msg := validateLDAP(c, true); msg == "" || field != tc.field {
			t.Errorf("%s: field %q, message %q", tc.name, field, msg)
		}
	}
	// Saving needs every required field, even disabled; testing a connection does not.
	partial := ldapConfig{TLSMode: "tls", ServerAddr: "ldap.example.com:636"}
	if field, msg := validateLDAP(partial, true); field != "userDnSearchBase" {
		t.Errorf("incomplete saved: %q %q", field, msg)
	}
	if _, msg := validateLDAP(partial, false); msg != "" {
		t.Errorf("incomplete test refused: %s", msg)
	}
}

// TestLDAPSignIn runs against a real directory when BIN_LDAP_TEST_ADDR is set
// (host:port of an OpenLDAP for dc=example,dc=org, admin password "adminpw",
// seeded with uid=ana in cn=bin-admins and uid=luis outside it).
func TestLDAPSignIn(t *testing.T) {
	addr := os.Getenv("BIN_LDAP_TEST_ADDR")
	if addr == "" {
		t.Skip("BIN_LDAP_TEST_ADDR not set")
	}
	e := newEnv(t)
	e.login()
	cfg := map[string]any{
		"enabled": true, "serverAddr": addr, "tlsMode": "none",
		"lookupBindDn": "cn=admin,dc=example,dc=org", "lookupBindPassword": "adminpw",
		"userDnSearchBase": "ou=people,dc=example,dc=org", "userDnSearchFilter": "(&(objectClass=inetOrgPerson)(uid=%s))",
		"groupSearchBase": "ou=groups,dc=example,dc=org", "groupSearchFilter": "(&(objectClass=groupOfNames)(member=%d))",
		"adminGroupDn": "CN=bin-admins,OU=groups,DC=example,DC=org", // compared case-insensitively
	}
	if code := e.call("POST", "/api/v1/identity/ldap/test", cfg, nil); code != 200 {
		t.Fatalf("test connection: %d", code)
	}
	var saved ldapJSON
	if code := e.call("PUT", "/api/v1/identity/ldap", cfg, &saved); code != 200 || !saved.LookupPasswordSet || saved.LookupPassword != "" {
		t.Fatalf("save: %d %+v", code, saved)
	}

	login := func(user, pass string) (int, string) {
		t.Helper()
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
		resp, err := client.Post(e.console+"/api/v1/login/ldap", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, sessionUser(t, client, e.console)
	}
	if code, user := login("ana", "ana-pass"); code != 200 || user != "ldap:ana" {
		t.Errorf("admin: %d %q", code, user)
	}
	if code, user := login("luis", "luis-pass"); code != 403 || user != "" {
		t.Errorf("non-admin: %d %q", code, user)
	}
	if code, _ := login("ana", "wrong"); code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if code, _ := login("*", "ana-pass"); code != 401 {
		t.Errorf("wildcard username: %d", code)
	}
	if code, _ := login("ana)(uid=*", "ana-pass"); code != 401 {
		t.Errorf("injected username: %d", code)
	}
	var providers map[string]any
	e.call("GET", "/api/v1/login/providers", nil, &providers)
	if providers["ldap"] != true {
		t.Errorf("providers: %v", providers)
	}
}

func TestLDAPRejectsEmptyPassword(t *testing.T) {
	h := &Handler{}
	// Must fail before any connection: an empty password is an anonymous bind.
	if _, err := h.ldapAuthenticate(ldapConfig{ServerAddr: "192.0.2.1:389"}, "ana", ""); err != errLDAPCredentials {
		t.Errorf("got %v", err)
	}
}
