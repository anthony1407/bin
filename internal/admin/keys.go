package admin

import (
	"net/http"
	"time"

	"bin/internal/auth"
	"bin/internal/storage"
)

type keyJSON struct {
	AccessKey string     `json:"accessKey"`
	SecretKey string     `json:"secretKey,omitempty"` // only in the creation response
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	LastUsed  *time.Time `json:"lastUsedAt"`
	Expired   bool       `json:"expired"`
	Buckets   []string   `json:"buckets"`
	ReadOnly  bool       `json:"readOnly"`
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func toKeyJSON(k storage.AccessKey) keyJSON {
	scope, _ := auth.ParseScope(k.Scope)
	buckets := scope.Buckets
	if buckets == nil {
		buckets = []string{}
	}
	return keyJSON{
		AccessKey: k.AccessKey, Name: k.Name, Enabled: k.Enabled, CreatedAt: k.Created,
		ExpiresAt: optionalTime(k.Expires), LastUsed: optionalTime(k.LastUsed),
		Expired: !k.Expires.IsZero() && time.Now().After(k.Expires), Buckets: buckets, ReadOnly: scope.ReadOnly,
	}
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.opts.Store.ListAccessKeys(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]keyJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, toKeyJSON(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accessKeys": out})
}

// validateBuckets checks that a scope only names existing buckets.
func (h *Handler) validateBuckets(w http.ResponseWriter, r *http.Request, buckets []string) bool {
	for _, b := range buckets {
		if _, err := h.opts.Store.GetBucket(r.Context(), b); err != nil {
			writeError(w, http.StatusBadRequest, "unknown_bucket", "The scope names a bucket that does not exist: "+b)
			return false
		}
	}
	return true
}

// createKey generates a new access key. The secret appears only in this
// response; bin stores it encrypted and never shows it again.
func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expiresAt"`
		Buckets   []string   `json:"buckets"`
		ReadOnly  bool       `json:"readOnly"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid_expiry", "The expiry date must be in the future.")
		return
	}
	if !h.validateBuckets(w, r, in.Buckets) {
		return
	}
	ak, sk := auth.NewCredentials()
	k := storage.AccessKey{
		AccessKey: ak,
		SecretEnc: h.opts.Keys.Box.Encrypt(sk),
		Name:      in.Name,
		Enabled:   true,
		Scope:     auth.Scope{Buckets: in.Buckets, ReadOnly: in.ReadOnly}.String(),
	}
	if in.ExpiresAt != nil {
		k.Expires = *in.ExpiresAt
	}
	if err := h.opts.Store.CreateAccessKey(r.Context(), k); err != nil {
		h.storageError(w, r, err)
		return
	}
	created, err := h.opts.Store.GetAccessKey(r.Context(), ak)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("access key created", "access_key", ak, "name", in.Name)
	out := toKeyJSON(created)
	out.SecretKey = sk
	writeJSON(w, http.StatusCreated, out)
}

// updateKey changes the fields present in the body. "expiresAt": null
// removes the expiry.
func (h *Handler) updateKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      *string    `json:"name"`
		Enabled   *bool      `json:"enabled"`
		ExpiresAt *time.Time `json:"expiresAt"`
		NoExpiry  bool       `json:"noExpiry"`
		Buckets   *[]string  `json:"buckets"`
		ReadOnly  *bool      `json:"readOnly"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Buckets != nil && !h.validateBuckets(w, r, *in.Buckets) {
		return
	}
	ak := r.PathValue("key")
	k, err := h.opts.Store.UpdateAccessKey(r.Context(), ak, func(k *storage.AccessKey) error {
		if k.Internal {
			return storage.ErrNoSuchAccessKey // managed by bin, not editable
		}
		scope, _ := auth.ParseScope(k.Scope)
		if in.Name != nil {
			k.Name = *in.Name
		}
		if in.Enabled != nil {
			k.Enabled = *in.Enabled
		}
		if in.NoExpiry {
			k.Expires = time.Time{}
		} else if in.ExpiresAt != nil {
			k.Expires = *in.ExpiresAt
		}
		if in.Buckets != nil {
			scope.Buckets = *in.Buckets
		}
		if in.ReadOnly != nil {
			scope.ReadOnly = *in.ReadOnly
		}
		k.Scope = scope.String()
		return nil
	})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("access key updated", "access_key", ak, "enabled", k.Enabled)
	writeJSON(w, http.StatusOK, toKeyJSON(k))
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	ak := r.PathValue("key")
	k, err := h.opts.Store.GetAccessKey(r.Context(), ak)
	if err == nil && k.Internal {
		err = storage.ErrNoSuchAccessKey
	}
	if err == nil {
		err = h.opts.Store.DeleteAccessKey(r.Context(), ak)
	}
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("access key deleted", "access_key", ak)
	w.WriteHeader(http.StatusNoContent)
}
