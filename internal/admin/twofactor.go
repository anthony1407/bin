package admin

import (
	"net/http"
	"sync"
	"time"

	"bin/internal/auth"
	"bin/internal/storage"
)

// A user may add a second factor to their console sign-in: an authenticator
// app's six-digit code (TOTP). Turning it on takes two steps: setup shows a
// new secret (kept in memory for a few minutes, never stored until proven),
// and enable checks a code from the app before saving it, encrypted. Sign-in
// and console activation then also need a code. Lost phones are handled by an
// admin, who can remove it.

const totpSetupTTL = 10 * time.Minute

type pendingTOTP struct {
	secret  string
	expires time.Time
}

// totpSetups holds secrets shown but not yet confirmed, per session.
type totpSetups struct {
	mu sync.Mutex
	m  map[string]pendingTOTP
}

func newTOTPSetups() *totpSetups { return &totpSetups{m: map[string]pendingTOTP{}} }

func (t *totpSetups) put(session, secret string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	t.m[session] = pendingTOTP{secret: secret, expires: now.Add(totpSetupTTL)}
}

func (t *totpSetups) get(session string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.m[session]
	if !ok || time.Now().After(p.expires) {
		return "", false
	}
	return p.secret, true
}

func (t *totpSetups) drop(session string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, session)
}

// checkSecondFactor verifies the code of a key whose user turned on a second
// factor, and remembers its time step so the code works once. It writes the
// error response and returns false when the sign-in must stop.
func (h *Handler) checkSecondFactor(w http.ResponseWriter, r *http.Request, ip string, k storage.AccessKey, code string) bool {
	if k.TOTPSecretEnc == "" {
		return true
	}
	if code == "" {
		// Not a failure: the password was right, the code comes next.
		writeError(w, http.StatusUnauthorized, "totp_required", "Enter the code from your authenticator app.")
		return false
	}
	secret, err := h.opts.Keys.Box.Decrypt(k.TOTPSecretEnc)
	if err != nil {
		h.storageError(w, r, err)
		return false
	}
	step, ok := auth.VerifyTOTP(secret, code, time.Now(), k.TOTPLastStep)
	if !ok {
		h.limiter.fail(ip)
		h.limiter.fail(accountLimit(k.AccessKey))
		writeError(w, http.StatusUnauthorized, "invalid_totp", "The authenticator code is wrong or was already used.")
		return false
	}
	if _, err := h.opts.Store.UpdateAccessKey(r.Context(), k.AccessKey, func(k *storage.AccessKey) error {
		if step > k.TOTPLastStep {
			k.TOTPLastStep = step
		}
		return nil
	}); err != nil {
		h.storageError(w, r, err)
		return false
	}
	return true
}

// setupTOTP starts turning on a second factor: a new secret, for the user's
// authenticator app.
func (h *Handler) setupTOTP(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	if sess.AccessKey == "" {
		writeError(w, http.StatusBadRequest, "not_a_user", "A second factor is for users' console sign-in.")
		return
	}
	k, err := h.opts.Store.GetAccessKey(r.Context(), sess.AccessKey)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	if k.TOTPSecretEnc != "" {
		writeError(w, http.StatusConflict, "totp_enabled", "A second factor is already on. Turn it off first to set up another.")
		return
	}
	secret := auth.NewTOTPSecret()
	h.totp.put(sess.TokenHash, secret)
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "url": auth.TOTPURL(secret, "bin", k.AccessKey)})
}

// enableTOTP saves the secret of setupTOTP once a code from the app matches:
// POST {"code"}. The user's other sessions end.
func (h *Handler) enableTOTP(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	secret, ok := h.totp.get(sess.TokenHash)
	if sess.AccessKey == "" || !ok {
		writeError(w, http.StatusBadRequest, "no_totp_setup", "Start the setup again: it expired or was not started.")
		return
	}
	step, ok := auth.VerifyTOTP(secret, in.Code, time.Now(), 0)
	if !ok {
		writeFieldError(w, "invalid_totp", "code", "The code does not match. Check the app and your device's clock.")
		return
	}
	if _, err := h.opts.Store.UpdateAccessKey(r.Context(), sess.AccessKey, func(k *storage.AccessKey) error {
		k.TOTPSecretEnc, k.TOTPLastStep = h.opts.Keys.Box.Encrypt(secret), step
		return nil
	}); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.totp.drop(sess.TokenHash)
	if err := h.opts.Store.DeleteKeySessions(r.Context(), sess.AccessKey, sess.TokenHash); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("second factor turned on", "access_key", sess.AccessKey)
	w.WriteHeader(http.StatusNoContent)
}

// disableTOTP turns a user's second factor off, proven by a current code:
// POST {"code"}.
func (h *Handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if sess.AccessKey == "" {
		writeError(w, http.StatusBadRequest, "not_a_user", "A second factor is for users' console sign-in.")
		return
	}
	k, err := h.opts.Store.GetAccessKey(r.Context(), sess.AccessKey)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	if k.TOTPSecretEnc == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.accountBlocked(w, k.AccessKey) {
		return
	}
	secret, err := h.opts.Keys.Box.Decrypt(k.TOTPSecretEnc)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	if _, ok := auth.VerifyTOTP(secret, in.Code, time.Now(), k.TOTPLastStep); !ok {
		h.limiter.fail(accountLimit(k.AccessKey))
		writeFieldError(w, "invalid_totp", "code", "The code is wrong or was already used.")
		return
	}
	if err := h.clearTOTP(r, sess.AccessKey); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("second factor turned off by its user", "access_key", sess.AccessKey)
	w.WriteHeader(http.StatusNoContent)
}

// deleteKeyTOTP removes a user's second factor (admins), e.g. after a lost phone.
func (h *Handler) deleteKeyTOTP(w http.ResponseWriter, r *http.Request) {
	ak := r.PathValue("key")
	setAuditResource(r, ak)
	if err := h.clearTOTP(r, ak); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("second factor removed by an admin", "access_key", ak, "by", sessionOf(r).User)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) clearTOTP(r *http.Request, accessKey string) error {
	_, err := h.opts.Store.UpdateAccessKey(r.Context(), accessKey, func(k *storage.AccessKey) error {
		if k.Internal {
			return storage.ErrNoSuchAccessKey
		}
		k.TOTPSecretEnc, k.TOTPLastStep = "", 0
		return nil
	})
	return err
}

// --- sessions ---------------------------------------------------------------------------

type sessionInfoJSON struct {
	ID        string    `json:"id"` // a prefix of the token hash: names it, cannot be used to sign in
	User      string    `json:"user"`
	Role      string    `json:"role"`
	Remote    string    `json:"remote,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Current   bool      `json:"current"`
}

func sessionID(tokenHash string) string { return tokenHash[:16] }

// visibleSessions lists the sessions the caller may see: a user their own,
// an admin every one (or one key's with ?accessKey=).
func (h *Handler) visibleSessions(r *http.Request) ([]storage.Session, error) {
	only := r.URL.Query().Get("accessKey")
	if sess := sessionOf(r); sess.AccessKey != "" {
		only = sess.AccessKey
	}
	return h.opts.Store.ListSessions(r.Context(), only)
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.visibleSessions(r)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	me := sessionOf(r).TokenHash
	out := make([]sessionInfoJSON, 0, len(list))
	for _, s := range list {
		role := "admin"
		if s.AccessKey != "" {
			role = "user"
		}
		out = append(out, sessionInfoJSON{
			ID: sessionID(s.TokenHash), User: s.User, Role: role, Remote: s.Remote, UserAgent: s.UserAgent,
			CreatedAt: s.Created, ExpiresAt: s.Expires, Current: s.TokenHash == me,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// deleteSession signs one session out: a user only their own.
func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	list, err := h.visibleSessions(r)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	id := r.PathValue("id")
	for _, s := range list {
		if sessionID(s.TokenHash) == id {
			setAuditResource(r, s.User)
			if err := h.opts.Store.DeleteSession(r.Context(), s.TokenHash); err != nil {
				h.storageError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no_such_session", "The session does not exist or has ended.")
}
