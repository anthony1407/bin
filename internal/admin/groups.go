package admin

import (
	"net/http"
	"regexp"
	"slices"
	"time"

	"bin/internal/auth"
	"bin/internal/storage"
)

type groupJSON struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	Buckets     []string  `json:"buckets"`
	ReadOnly    bool      `json:"readOnly"`
	Members     []string  `json:"members"`
}

func toGroupJSON(g storage.Group) groupJSON {
	scope, _ := auth.ParseScope(g.Scope)
	out := groupJSON{
		Name: g.Name, Description: g.Description, Enabled: g.Enabled, CreatedAt: g.Created,
		Buckets: scope.Buckets, ReadOnly: scope.ReadOnly, Members: g.Members,
	}
	if out.Buckets == nil {
		out.Buckets = []string{}
	}
	if out.Members == nil {
		out.Members = []string{}
	}
	return out
}

var groupName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.opts.Store.ListGroups(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]groupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, toGroupJSON(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Buckets     []string `json:"buckets"`
		ReadOnly    bool     `json:"readOnly"`
		Members     []string `json:"members"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	setAuditResource(r, in.Name)
	if !groupName.MatchString(in.Name) {
		writeError(w, http.StatusBadRequest, "invalid_group_name",
			"Group names use 1-64 letters, digits, dots, underscores and hyphens, starting with a letter or digit.")
		return
	}
	if !h.validateBuckets(w, r, in.Buckets) {
		return
	}
	g := storage.Group{
		Name: in.Name, Description: in.Description, Enabled: true,
		Scope:   auth.Scope{Buckets: in.Buckets, ReadOnly: in.ReadOnly}.String(),
		Members: in.Members,
	}
	if err := h.opts.Store.CreateGroup(r.Context(), g); err != nil {
		h.storageError(w, r, err)
		return
	}
	created, err := h.opts.Store.GetGroup(r.Context(), in.Name)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("group created", "group", in.Name, "members", len(created.Members))
	writeJSON(w, http.StatusCreated, toGroupJSON(created))
}

// updateGroup changes the fields present in the body; "members" replaces
// the member list.
func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Description *string   `json:"description"`
		Enabled     *bool     `json:"enabled"`
		Buckets     *[]string `json:"buckets"`
		ReadOnly    *bool     `json:"readOnly"`
		Members     *[]string `json:"members"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := r.PathValue("name")
	setAuditResource(r, name)
	if in.Buckets != nil && !h.validateBuckets(w, r, *in.Buckets) {
		return
	}
	g, err := h.opts.Store.UpdateGroup(r.Context(), name, func(g *storage.Group) error {
		scope, _ := auth.ParseScope(g.Scope)
		if in.Description != nil {
			g.Description = *in.Description
		}
		if in.Enabled != nil {
			g.Enabled = *in.Enabled
		}
		if in.Buckets != nil {
			scope.Buckets = *in.Buckets
		}
		if in.ReadOnly != nil {
			scope.ReadOnly = *in.ReadOnly
		}
		if in.Members != nil {
			g.Members = slices.Clone(*in.Members)
		}
		g.Scope = scope.String()
		return nil
	})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("group updated", "group", name, "enabled", g.Enabled, "members", len(g.Members))
	writeJSON(w, http.StatusOK, toGroupJSON(g))
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	setAuditResource(r, name)
	if err := h.opts.Store.DeleteGroup(r.Context(), name); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("group deleted", "group", name)
	w.WriteHeader(http.StatusNoContent)
}
