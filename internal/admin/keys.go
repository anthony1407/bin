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
	// Groups the key belongs to; when there are any, they decide its access
	// and Buckets/ReadOnly are not used.
	Groups []string `json:"groups"`
}

// keyGroups maps each access key to the groups it belongs to.
func (h *Handler) keyGroups(r *http.Request) (map[string][]string, error) {
	groups, err := h.opts.Store.ListGroups(r.Context())
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, g := range groups {
		for _, k := range g.Members {
			out[k] = append(out[k], g.Name)
		}
	}
	return out, nil
}

// withGroups returns the key as JSON with its groups filled in.
func (h *Handler) withGroups(r *http.Request, k storage.AccessKey) (keyJSON, error) {
	out := toKeyJSON(k)
	groups, err := h.opts.Store.KeyGroups(r.Context(), k.AccessKey)
	for _, g := range groups {
		out.Groups = append(out.Groups, g.Name)
	}
	return out, err
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
		Groups: []string{},
	}
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.opts.Store.ListAccessKeys(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	groups, err := h.keyGroups(r)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]keyJSON, 0, len(keys))
	for _, k := range keys {
		kj := toKeyJSON(k)
		if g := groups[k.AccessKey]; g != nil {
			kj.Groups = g
		}
		out = append(out, kj)
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
		Groups    []string   `json:"groups"`
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
	if !h.validateGroups(w, r, in.Groups) {
		return
	}
	ak, sk := auth.NewCredentials()
	setAuditResource(r, ak)
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
	if len(in.Groups) > 0 {
		if err := h.opts.Store.SetKeyGroups(r.Context(), ak, in.Groups); err != nil {
			h.opts.Store.DeleteAccessKey(r.Context(), ak) // do not leave a key with the wrong access
			h.storageError(w, r, err)
			return
		}
	}
	created, err := h.opts.Store.GetAccessKey(r.Context(), ak)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("access key created", "access_key", ak, "name", in.Name, "groups", len(in.Groups))
	out, err := h.withGroups(r, created)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
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
		Groups    *[]string  `json:"groups"` // replaces the key's groups
	}
	if !readJSON(w, r, &in) {
		return
	}
	ak := r.PathValue("key")
	setAuditResource(r, ak)
	if in.Buckets != nil && !h.validateBuckets(w, r, *in.Buckets) {
		return
	}
	if in.Groups != nil && !h.validateGroups(w, r, *in.Groups) {
		return
	}
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
	if err == nil && in.Groups != nil {
		err = h.opts.Store.SetKeyGroups(r.Context(), ak, *in.Groups)
	}
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("access key updated", "access_key", ak, "enabled", k.Enabled)
	out, err := h.withGroups(r, k)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// validateGroups checks that every named group exists.
func (h *Handler) validateGroups(w http.ResponseWriter, r *http.Request, groups []string) bool {
	for _, g := range groups {
		if _, err := h.opts.Store.GetGroup(r.Context(), g); err != nil {
			writeError(w, http.StatusBadRequest, "unknown_group", "No such group: "+g)
			return false
		}
	}
	return true
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	ak := r.PathValue("key")
	setAuditResource(r, ak)
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
