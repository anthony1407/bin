package admin

import (
	"crypto/subtle"
	"net/http"
	"strconv"

	"bin/internal/auth"
	"bin/internal/storage"
)

// Console passwords of users. A user turns on console access by proving
// their access key and secret key once and choosing a password; after that
// they sign in with the access key and that password. They may change it
// (giving the current one); an admin may set or remove it. The S3 secret key
// never changes with it. Every change ends the key's other console sessions.

func writePasswordError(w http.ResponseWriter, err error) {
	writeFieldError(w, "invalid_password", "password", "The password is too short or too long: "+err.Error()+".")
}

// setPassword stores a new console password for a key and ends its console
// sessions but keepTokenHash. An empty password removes console access.
func (h *Handler) setPassword(r *http.Request, accessKey, password, keepTokenHash string) error {
	hash := ""
	if password != "" {
		var err error
		if hash, err = auth.HashPassword(password); err != nil {
			return err
		}
	}
	if _, err := h.opts.Store.UpdateAccessKey(r.Context(), accessKey, func(k *storage.AccessKey) error {
		if k.Internal {
			return storage.ErrNoSuchAccessKey // managed by bin, never signs in
		}
		k.PasswordHash = hash
		return nil
	}); err != nil {
		return err
	}
	return h.opts.Store.DeleteKeySessions(r.Context(), accessKey, keepTokenHash)
}

// activateConsole lets a key's user choose a console password, proving the
// key with its secret: POST {"accessKey","secretKey","password"}. It signs
// them in. With a password already set it replaces it (the secret key is the
// stronger credential), which also serves as "forgot my password".
func (h *Handler) activateConsole(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
		Password  string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	k, ok := h.consoleKey(r.Context(), in.AccessKey)
	secret := ""
	if ok {
		secret, ok = h.opts.Keys.Secret(k.AccessKey)
	}
	if !ok || subtle.ConstantTimeCompare([]byte(secret), []byte(in.SecretKey)) != 1 {
		setAuditActor(r, "(unknown)")
		h.limiter.fail(ip)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong access key or secret key.")
		return
	}
	setAuditActor(r, k.AccessKey)
	if err := auth.CheckPasswordPolicy(in.Password); err != nil {
		writePasswordError(w, err)
		return
	}
	h.limiter.reset(ip)
	if err := h.setPassword(r, k.AccessKey, in.Password, ""); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("console access activated", "access_key", k.AccessKey)
	sess, err := h.startSession(w, r, k.AccessKey, k.AccessKey)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// changeOwnPassword lets a signed-in user change their console password:
// POST {"currentPassword","password"}. Their other sessions end; this one
// stays.
func (h *Handler) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	if sess.AccessKey == "" {
		writeError(w, http.StatusBadRequest, "not_a_user",
			"Administrators sign in with root's credentials, LDAP or OpenID: their password is not kept here.")
		return
	}
	ip := clientIP(r)
	if wait := h.limiter.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many failed attempts. Try again later.")
		return
	}
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		Password        string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	k, err := h.opts.Store.GetAccessKey(r.Context(), sess.AccessKey)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	if !auth.CheckPassword(k.PasswordHash, in.CurrentPassword) {
		h.limiter.fail(ip)
		writeFieldError(w, "invalid_credentials", "currentPassword", "The current password is wrong.")
		return
	}
	h.limiter.reset(ip)
	if err := auth.CheckPasswordPolicy(in.Password); err != nil {
		writePasswordError(w, err)
		return
	}
	if err := h.setPassword(r, k.AccessKey, in.Password, sess.TokenHash); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("console password changed by its user", "access_key", k.AccessKey)
	w.WriteHeader(http.StatusNoContent)
}

// putKeyPassword sets a user's console password (admins): PUT {"password"}.
func (h *Handler) putKeyPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ak := r.PathValue("key")
	setAuditResource(r, ak)
	if err := auth.CheckPasswordPolicy(in.Password); err != nil {
		writePasswordError(w, err)
		return
	}
	if err := h.setPassword(r, ak, in.Password, ""); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("console password set by an admin", "access_key", ak, "by", sessionOf(r).User)
	w.WriteHeader(http.StatusNoContent)
}

// deleteKeyPassword removes a user's console password (admins): the user can
// no longer sign in to the console, and their S3 access is untouched.
func (h *Handler) deleteKeyPassword(w http.ResponseWriter, r *http.Request) {
	ak := r.PathValue("key")
	setAuditResource(r, ak)
	if err := h.setPassword(r, ak, "", ""); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("console password removed by an admin", "access_key", ak, "by", sessionOf(r).User)
	w.WriteHeader(http.StatusNoContent)
}
