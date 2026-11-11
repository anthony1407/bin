package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"bin/internal/auth"
	"bin/internal/server"
	"bin/internal/storage"
)

const sessionCookie = "bin_session"

type sessionKey struct{}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type loginRequest struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// loginBody is what POST /login reads: the credentials, and the
// authenticator code of a user who turned on a second factor.
type loginBody struct {
	loginRequest
	TOTP string `json:"totp"`
}

type sessionResponse struct {
	User      string    `json:"user"`
	CSRFToken string    `json:"csrfToken"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Role is "admin" (root, LDAP, OpenID) or "user" (an access key's user,
	// limited to its buckets).
	Role string `json:"role"`
	// CanCreateBuckets says whether a user may create buckets; admins may.
	CanCreateBuckets bool `json:"canCreateBuckets"`
	// TOTP says whether a user signs in with a second factor.
	TOTP bool `json:"totp"`
}

// login checks root's credentials, or an access key and its user's console
// password, and starts a session.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "bad_origin", "Cross-origin request refused.")
		return
	}
	ip := clientIP(r)
	if wait := h.limiter.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many failed sign-in attempts. Try again later.")
		return
	}
	var in loginBody
	if !readJSON(w, r, &in) {
		return
	}
	akOK := subtle.ConstantTimeCompare([]byte(in.AccessKey), []byte(h.opts.Keys.RootAccessKey)) == 1
	if !akOK {
		h.userLogin(w, r, ip, in)
		return
	}
	skOK := subtle.ConstantTimeCompare([]byte(in.SecretKey), []byte(h.opts.Keys.RootSecretKey)) == 1
	setAuditActor(r, in.AccessKey)
	if !skOK {
		h.loginFailed(w, r, ip)
		return
	}
	h.limiter.reset(ip)
	sess, err := h.startSession(w, r, in.AccessKey, "")
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (h *Handler) loginFailed(w http.ResponseWriter, r *http.Request, ip string) {
	h.limiter.fail(ip)
	server.Annotate(r.Context(), slog.String("login", "failed"))
	writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong access key or password.")
}

// consoleKey returns a stored access key whose user may sign in to the
// console: enabled, unexpired, not managed by bin itself.
func (h *Handler) consoleKey(ctx context.Context, accessKey string) (storage.AccessKey, bool) {
	if accessKey == "" || accessKey == h.opts.Keys.RootAccessKey {
		return storage.AccessKey{}, false
	}
	k, err := h.opts.Store.GetAccessKey(ctx, accessKey)
	if err != nil || !k.Enabled || k.Internal || (!k.Expires.IsZero() && time.Now().After(k.Expires)) {
		return storage.AccessKey{}, false
	}
	return k, true
}

// accountLimit is the limiter entry of one account: failures against it count
// whatever IP they come from, so spreading guesses over many addresses does
// not help. (It also means someone can lock an account for a while by
// failing on purpose; the window is short and an admin can still help.)
func accountLimit(accessKey string) string { return "key:" + accessKey }

// accountBlocked answers 429 when an account had too many recent failures.
func (h *Handler) accountBlocked(w http.ResponseWriter, accessKey string) bool {
	wait := h.limiter.blocked(accountLimit(accessKey))
	if wait <= 0 {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many failed attempts on this account. Try again later.")
	return true
}

// userLogin signs in an access key's user with its console password, and its
// authenticator code when the user turned on a second factor.
func (h *Handler) userLogin(w http.ResponseWriter, r *http.Request, ip string, in loginBody) {
	password := in.SecretKey
	k, ok := h.consoleKey(r.Context(), in.AccessKey)
	if !ok || k.PasswordHash == "" {
		// An unknown key is not recorded: people paste secrets into the wrong
		// field. The wait matches a real check, so keys are not told apart.
		// Only real accounts are tracked: random names must not fill the limiter.
		auth.DummyPasswordCheck(password)
		setAuditActor(r, "(unknown)")
		h.loginFailed(w, r, ip)
		return
	}
	setAuditActor(r, k.AccessKey)
	if h.accountBlocked(w, k.AccessKey) {
		return
	}
	if !auth.CheckPassword(k.PasswordHash, password) {
		h.limiter.fail(accountLimit(k.AccessKey))
		h.loginFailed(w, r, ip)
		return
	}
	if !h.checkSecondFactor(w, r, ip, k, in.TOTP) {
		return
	}
	h.limiter.reset(ip)
	h.limiter.reset(accountLimit(k.AccessKey))
	sess, err := h.startSession(w, r, k.AccessKey, k.AccessKey)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// startSession creates a console session for user and sets its cookie. Every
// sign-in method ends here. accessKey is set for a user's session (limited to
// that key's access) and empty for admins (root, OpenID, LDAP).
func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, user, accessKey string) (sessionResponse, error) {
	token, csrf := randomToken(), randomToken()
	expires := time.Now().Add(h.opts.SessionTTL)
	if err := h.opts.Store.CreateSession(r.Context(), storage.Session{
		TokenHash: hashToken(token), CSRF: csrf, User: user, AccessKey: accessKey, Expires: expires,
		Remote: clientIP(r), UserAgent: truncate(r.UserAgent(), maxUserAgent),
	}); err != nil {
		return sessionResponse{}, err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	server.Annotate(r.Context(), slog.String("login", "ok"), slog.String("user", user))
	return h.sessionJSON(r.Context(), storage.Session{User: user, CSRF: csrf, AccessKey: accessKey, Expires: expires}), nil
}

// sessionJSON describes a session to the console, with what it may do.
func (h *Handler) sessionJSON(ctx context.Context, s storage.Session) sessionResponse {
	out := sessionResponse{User: s.User, CSRFToken: s.CSRF, ExpiresAt: s.Expires, Role: "admin", CanCreateBuckets: true}
	if s.AccessKey != "" {
		out.Role, out.CanCreateBuckets = "user", false
		if k, err := h.opts.Store.GetAccessKey(ctx, s.AccessKey); err == nil {
			out.CanCreateBuckets, out.TOTP = k.CanCreateBuckets, k.TOTPSecretEnc != ""
		}
	}
	return out
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		h.opts.Store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.sessionJSON(r.Context(), sessionOf(r)))
}

// requireSession rejects requests without a live session and, for requests
// that change state, without the session's CSRF token or from another origin.
func (h *Handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not_signed_in", "Sign in first.")
			return
		}
		sess, err := h.opts.Store.GetSession(r.Context(), hashToken(c.Value))
		if errors.Is(err, storage.ErrNoSession) {
			writeError(w, http.StatusUnauthorized, "session_expired", "Your session has expired. Sign in again.")
			return
		} else if err != nil {
			h.storageError(w, r, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "bad_origin", "Cross-origin request refused.")
				return
			}
			token := r.Header.Get("X-CSRF-Token")
			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRF)) != 1 {
				writeError(w, http.StatusForbidden, "bad_csrf_token", "Missing or invalid X-CSRF-Token header.")
				return
			}
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, sess)
		if sess.AccessKey != "" {
			grants, ok := h.userSession(r.Context(), sess)
			if !ok {
				h.opts.Store.DeleteSession(r.Context(), sess.TokenHash)
				writeError(w, http.StatusUnauthorized, "session_expired", "Your session has ended. Sign in again.")
				return
			}
			ctx = context.WithValue(ctx, grantsCtxKey{}, grants)
		}
		server.Annotate(r.Context(), slog.String("user", sess.User))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- brute-force protection ------------------------------------------------------

const (
	maxLoginFailures = 10
	loginWindow      = 15 * time.Minute
)

// sweepAbove is how many tracked IPs make fail() drop the ones whose
// failures all left the window (at most once per window): an IP that failed
// once and never came back would otherwise be kept forever, and rotating
// addresses (cheap with IPv6) would grow the map without bound.
const sweepAbove = 10000

// loginLimiter blocks an IP after maxLoginFailures failed sign-ins within
// loginWindow, until the window passes.
type loginLimiter struct {
	mu        sync.Mutex
	fails     map[string][]time.Time
	now       func() time.Time
	lastSweep time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string][]time.Time{}, now: time.Now}
}

func (l *loginLimiter) recent(ip string) []time.Time {
	cutoff := l.now().Add(-loginWindow)
	kept := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = kept
	}
	return kept
}

// blocked returns how long ip must wait, or 0.
func (l *loginLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recent(ip)
	if len(recent) < maxLoginFailures {
		return 0
	}
	return recent[0].Add(loginWindow).Sub(l.now())
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip), l.now())
	if len(l.fails) > sweepAbove && l.now().Sub(l.lastSweep) >= loginWindow {
		l.lastSweep = l.now()
		for other := range l.fails {
			l.recent(other) // deletes the IP once nothing recent is left
		}
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
