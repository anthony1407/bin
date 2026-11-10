package admin

import (
	"log/slog"
	"net/http"
	"strings"

	"bin/internal/server"
	"bin/internal/storage"
)

// Copying and moving from the explorer happens server side: the data never
// passes through the browser, and every copy is verified against its SHA-256.
// Moving copies first and only then sends the originals to the source
// bucket's trash, so a failure never loses anything and a mistaken move can
// be undone from there.

type transferRequest struct {
	Keys         []string `json:"keys"`
	Prefixes     []string `json:"prefixes"` // folders, transferred with everything inside
	TargetBucket string   `json:"targetBucket"`
	TargetPrefix string   `json:"targetPrefix"` // folder the items land in; "" is the bucket root
	Move         bool     `json:"move"`
}

// lastSegment is the name of a key or folder within its parent.
func lastSegment(key string) string {
	k := strings.TrimSuffix(key, "/")
	return k[strings.LastIndex(k, "/")+1:]
}

// copyObjects copies (or moves) objects and folders into a folder of the
// same or another bucket. Each item keeps its name; folders keep their
// content's layout. Existing objects at the destination are replaced.
func (h *Handler) copyObjects(w http.ResponseWriter, r *http.Request) {
	var in transferRequest
	if !readJSON(w, r, &in) {
		return
	}
	bucket := r.PathValue("bucket")
	target := in.TargetBucket
	if target == "" {
		target = bucket
	}
	dst := in.TargetPrefix
	if dst != "" && !strings.HasSuffix(dst, "/") {
		dst += "/"
	}
	if strings.HasPrefix(dst, trashPrefix) {
		writeError(w, http.StatusBadRequest, "invalid_target", "Items cannot be copied or moved into the trash.")
		return
	}
	for _, k := range in.Keys {
		if k == "" || strings.HasPrefix(k, trashPrefix) {
			writeError(w, http.StatusBadRequest, "invalid_key", "Items in the trash are restored, not copied or moved.")
			return
		}
	}
	for _, p := range in.Prefixes {
		if !strings.HasSuffix(p, "/") || p == "/" || strings.HasPrefix(p, trashPrefix) {
			writeError(w, http.StatusBadRequest, "invalid_prefix", "Folders must end with / and be outside the trash.")
			return
		}
		if to := dst + lastSegment(p) + "/"; target == bucket && to != p && strings.HasPrefix(to, p) {
			writeError(w, http.StatusBadRequest, "invalid_target", "A folder cannot be copied or moved into itself.")
			return
		}
	}

	type pair struct{ from, to string }
	var pairs []pair
	for _, k := range in.Keys {
		pairs = append(pairs, pair{k, dst + lastSegment(k)})
	}
	for _, p := range in.Prefixes {
		objs, err := h.listUnder(r.Context(), bucket, p)
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		to := dst + lastSegment(p) + "/"
		for _, o := range objs {
			pairs = append(pairs, pair{o.Key, to + strings.TrimPrefix(o.Key, p)})
		}
	}

	done := 0
	for _, pr := range pairs {
		if target == bucket && pr.from == pr.to {
			continue // already there
		}
		if _, err := h.opts.Store.CopyObject(r.Context(), bucket, pr.from, target, pr.to, storage.CopyOptions{}); err != nil {
			server.Annotate(r.Context(), slog.Int("copied", done))
			h.storageError(w, r, err)
			return
		}
		if in.Move {
			if err := h.moveObject(r.Context(), bucket, pr.from, trashPrefix+pr.from); err != nil {
				server.Annotate(r.Context(), slog.Int("copied", done))
				h.storageError(w, r, err)
				return
			}
		}
		done++
	}
	server.Annotate(r.Context(), slog.Int("copied", done), slog.Bool("move", in.Move))
	writeJSON(w, http.StatusOK, map[string]int{"copied": done})
}
