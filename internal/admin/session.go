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

type sessionResponse struct {
	User      string    `json:"user"`
	CSRFToken string    `json:"csrfToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// login checks the root credentials and starts a session. Only root can sign
// in to the console in v1; access keys are for S3 clients.
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
	var in loginRequest
	if !readJSON(w, r, &in) {
		return
	}
	akOK := subtle.ConstantTimeCompare([]byte(in.AccessKey), []byte(h.opts.Keys.RootAccessKey)) == 1
	skOK := subtle.ConstantTimeCompare([]byte(in.SecretKey), []byte(h.opts.Keys.RootSecretKey)) == 1
	// An unknown access key is not recorded: people paste secrets into the wrong field.
	if akOK {
		setAuditActor(r, in.AccessKey)
	} else {
		setAuditActor(r, "(unknown)")
	}
	if !akOK || !skOK {
		h.limiter.fail(ip)
		server.Annotate(r.Context(), slog.String("login", "failed"))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong access key or secret key.")
		return
	}
	h.limiter.reset(ip)

	token, csrf := randomToken(), randomToken()
	expires := time.Now().Add(h.opts.SessionTTL)
	if err := h.opts.Store.CreateSession(r.Context(), storage.Session{
		TokenHash: hashToken(token), CSRF: csrf, User: in.AccessKey, Expires: expires,
	}); err != nil {
		h.storageError(w, r, err)
		return
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
	server.Annotate(r.Context(), slog.String("login", "ok"))
	writeJSON(w, http.StatusOK, sessionResponse{User: in.AccessKey, CSRFToken: csrf, ExpiresAt: expires})
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
	s := r.Context().Value(sessionKey{}).(storage.Session)
	writeJSON(w, http.StatusOK, sessionResponse{User: s.User, CSRFToken: s.CSRF, ExpiresAt: s.Expires})
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
		server.Annotate(r.Context(), slog.String("user", sess.User))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
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

// loginLimiter blocks an IP after maxLoginFailures failed sign-ins within
// loginWindow, until the window passes.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
	now   func() time.Time
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
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
