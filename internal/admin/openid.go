package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OpenID Connect sign-in uses the authorization code flow with PKCE. The
// login endpoint redirects to the provider with a random state, nonce and
// code verifier, remembered server-side for a few minutes and bound to the
// browser by a cookie holding the state. The provider sends the browser back
// to /oauth_callback, which exchanges the code, verifies the ID token
// (signature, issuer, audience, expiry, nonce) and checks the admin claim.

const (
	oauthCallbackPath = "/oauth_callback"
	oidcStateCookie   = "bin_oidc_state"
	oidcPendingTTL    = 10 * time.Minute
	maxOIDCPending    = 1000
)

var oidcHTTPClient = &http.Client{Timeout: 15 * time.Second}

type oidcPending struct {
	nonce    string
	verifier string
	next     string
	expires  time.Time
}

// oidcStates holds sign-ins in progress, keyed by state.
type oidcStates struct {
	mu      sync.Mutex
	pending map[string]oidcPending
}

func newOIDCStates() *oidcStates { return &oidcStates{pending: map[string]oidcPending{}} }

func (s *oidcStates) put(state string, p oidcPending) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.pending {
		if now.After(v.expires) {
			delete(s.pending, k)
		}
	}
	if len(s.pending) >= maxOIDCPending {
		return false // bounded: unauthenticated callers can start sign-ins
	}
	s.pending[state] = p
	return true
}

// take returns and forgets a pending sign-in; each state works once.
func (s *oidcStates) take(state string) (oidcPending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	delete(s.pending, state)
	if !ok || time.Now().After(p.expires) {
		return oidcPending{}, false
	}
	return p, true
}

// issuerFromConfigURL derives the issuer from its discovery document URL.
func issuerFromConfigURL(configURL string) (string, error) {
	u, err := url.Parse(configURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		!strings.HasSuffix(u.Path, wellKnownSuffix) || u.RawQuery != "" {
		return "", errors.New("The configuration URL must be the provider's " + wellKnownSuffix + " address.")
	}
	return strings.TrimSuffix(configURL, wellKnownSuffix), nil
}

// oidcProvider fetches the discovery document and builds the OAuth2 client.
func (h *Handler) oidcProvider(ctx context.Context, r *http.Request, c openIDConfig) (*oidc.Provider, *oauth2.Config, error) {
	issuer, err := issuerFromConfigURL(c.ConfigURL)
	if err != nil {
		return nil, nil, err
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, oidcHTTPClient), issuer)
	if err != nil {
		return nil, nil, err
	}
	secret := ""
	if c.SecretEnc != "" {
		if secret, err = h.opts.Keys.Box.Decrypt(c.SecretEnc); err != nil {
			return nil, nil, fmt.Errorf("decrypting the client secret: %w", err)
		}
	}
	redirect := c.RedirectURI
	if redirect == "" {
		redirect = consoleBaseURL(r) + oauthCallbackPath
	}
	return provider, &oauth2.Config{
		ClientID: c.ClientID, ClientSecret: secret, Endpoint: provider.Endpoint(),
		RedirectURL: redirect, Scopes: strings.Fields(c.Scopes),
	}, nil
}

// loginOpenID starts a sign-in: GET /api/v1/login/openid?next=/path.
func (h *Handler) loginOpenID(w http.ResponseWriter, r *http.Request) {
	c, err := h.openIDConfig(r.Context())
	if err != nil || !c.usable() {
		redirectLoginError(w, r, "OpenID sign-in is not configured.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	_, oc, err := h.oidcProvider(ctx, r, c)
	if err != nil {
		h.opts.Log.Warn("openid: provider unavailable", "err", err)
		redirectLoginError(w, r, "The identity provider cannot be reached.")
		return
	}
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	if !h.oidcStates.put(state, oidcPending{nonce: nonce, verifier: verifier, next: safeNext(r.URL.Query().Get("next")),
		expires: time.Now().Add(oidcPendingTTL)}) {
		redirectLoginError(w, r, "Too many sign-ins in progress. Try again in a few minutes.")
		return
	}
	// Lax, not Strict: the provider sends the browser back with a cross-site GET.
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: state, Path: oauthCallbackPath,
		MaxAge: int(oidcPendingTTL / time.Second), HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, oc.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// oauthCallback finishes a sign-in started by loginOpenID.
func (h *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	setAuditActor(r, "(openid)")
	q := r.URL.Query()
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: oauthCallbackPath, MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || q.Get("state") == "" || cookie.Value != q.Get("state") {
		h.oidcFailed(w, r, "The sign-in expired or was started in another browser. Try again.", nil)
		return
	}
	pending, ok := h.oidcStates.take(q.Get("state"))
	if !ok {
		h.oidcFailed(w, r, "The sign-in expired. Try again.", nil)
		return
	}
	if e := q.Get("error"); e != "" {
		h.oidcFailed(w, r, "The identity provider refused the sign-in.", fmt.Errorf("%s: %s", e, q.Get("error_description")))
		return
	}
	c, err := h.openIDConfig(r.Context())
	if err != nil || !c.usable() {
		h.oidcFailed(w, r, "OpenID sign-in is not configured.", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ctx = oidc.ClientContext(ctx, oidcHTTPClient)
	provider, oc, err := h.oidcProvider(ctx, r, c)
	if err != nil {
		h.oidcFailed(w, r, "The identity provider cannot be reached.", err)
		return
	}
	token, err := oc.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(pending.verifier))
	if err != nil {
		h.oidcFailed(w, r, "The identity provider did not accept the sign-in code.", err)
		return
	}
	rawID, _ := token.Extra("id_token").(string)
	if rawID == "" {
		h.oidcFailed(w, r, "The identity provider sent no ID token.", nil)
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: c.ClientID}).Verify(ctx, rawID)
	if err != nil {
		h.oidcFailed(w, r, "The ID token is not valid.", err)
		return
	}
	if idToken.Nonce != pending.nonce {
		h.oidcFailed(w, r, "The ID token is not valid.", errors.New("nonce mismatch"))
		return
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		h.oidcFailed(w, r, "The ID token is not valid.", err)
		return
	}
	// Some providers put groups only in the UserInfo response.
	if _, ok := claims[c.ClaimName]; !ok {
		if info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil {
			extra := map[string]any{}
			if info.Claims(&extra) == nil && extra["sub"] == claims["sub"] {
				claims[c.ClaimName] = extra[c.ClaimName]
			}
		}
	}

	user := "openid:" + oidcUserName(claims)
	setAuditActor(r, user)
	if !claimHasValue(claims[c.ClaimName], c.AdminValue) {
		h.oidcFailed(w, r, "Your account is not allowed to use this console.",
			fmt.Errorf("user %s: claim %q does not contain %q", user, c.ClaimName, c.AdminValue))
		return
	}
	if _, err := h.startSession(w, r, user); err != nil {
		h.oidcFailed(w, r, "The session could not be created.", err)
		return
	}
	h.opts.Log.Info("openid sign-in", "user", user)
	http.Redirect(w, r, pending.next, http.StatusFound)
}

func (h *Handler) oidcFailed(w http.ResponseWriter, r *http.Request, message string, cause error) {
	if sw, ok := w.(*statusWriter); ok {
		sw.errCode = "openid_failed" // for the audit log
	}
	if cause != nil {
		h.opts.Log.Warn("openid sign-in failed", "reason", message, "err", cause)
	}
	redirectLoginError(w, r, message)
}

// redirectLoginError sends the browser to the login page with a message.
func redirectLoginError(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(message), http.StatusFound)
}

// safeNext keeps only local paths, so the sign-in cannot redirect elsewhere.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/buckets"
	}
	return next
}

// oidcUserName picks a readable name for the audit log and the session.
func oidcUserName(claims map[string]any) string {
	for _, k := range []string{"preferred_username", "email", "sub"} {
		if v, ok := claims[k].(string); ok && v != "" {
			return v
		}
	}
	return "unknown"
}

// claimHasValue reports whether a claim (a string, a list, or a space or
// comma separated string) contains want.
func claimHasValue(claim any, want string) bool {
	switch v := claim.(type) {
	case string:
		for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' }) {
			if s == want {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// testOpenID fetches the discovery document of the configuration in the body.
func (h *Handler) testOpenID(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConfigURL string `json:"configUrl"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	issuer, err := issuerFromConfigURL(strings.TrimSpace(in.ConfigURL))
	if err != nil {
		writeFieldError(w, "invalid_openid_config", "configUrl", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, oidcHTTPClient), issuer)
	if err != nil {
		h.opts.Log.Warn("openid test failed", "issuer", issuer, "err", err)
		writeError(w, http.StatusBadGateway, "openid_unreachable",
			"The discovery document could not be read, or its issuer does not match the URL.")
		return
	}
	var meta struct {
		Scopes []string `json:"scopes_supported"`
		Claims []string `json:"claims_supported"`
	}
	provider.Claims(&meta)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer": issuer, "authorizationEndpoint": provider.Endpoint().AuthURL,
		"scopesSupported": meta.Scopes, "claimsSupported": meta.Claims,
	})
}
