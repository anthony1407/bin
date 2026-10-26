package admin

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"bin/internal/server"
	"bin/internal/storage"
)

// The console's trash: deleting from the explorer moves objects under
// .trash/ in the same bucket, keeping their path, so they can be restored.
// The moves happen here, server side, rather than through the browser.
// A trashed object's modification time is when it was trashed, which the
// retention purge goes by.

const trashPrefix = ".trash/"

// trashPurgeTick is how often trashes are checked for expired files.
const trashPurgeTick = time.Hour

// maxTrashDays bounds the retention setting (about ten years).
const maxTrashDays = 3650

// listUnder returns every current object under prefix.
func (h *Handler) listUnder(ctx context.Context, bucket, prefix string) ([]storage.ObjectInfo, error) {
	var out []storage.ObjectInfo
	after := ""
	for {
		page, err := h.opts.Store.ListObjects(ctx, bucket, storage.ListOptions{Prefix: prefix, After: after, MaxKeys: 1000})
		if err != nil {
			return nil, err
		}
		out = append(out, page.Objects...)
		if !page.IsTruncated {
			return out, nil
		}
		after = page.NextMarker
	}
}

// expandKeys lists the objects named by keys and everything under prefixes.
func (h *Handler) expandKeys(ctx context.Context, bucket string, keys, prefixes []string) ([]string, error) {
	out := append([]string(nil), keys...)
	for _, p := range prefixes {
		objs, err := h.listUnder(ctx, bucket, p)
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			out = append(out, o.Key)
		}
	}
	return out, nil
}

// moveObject copies an object to its new key (verifying its data) and then
// deletes the old one, so a failure never loses it.
func (h *Handler) moveObject(ctx context.Context, bucket, from, to string) error {
	if _, err := h.opts.Store.CopyObject(ctx, bucket, from, bucket, to, storage.CopyOptions{}); err != nil {
		return err
	}
	return h.opts.Store.DeleteObject(ctx, bucket, from)
}

type trashRequest struct {
	Keys     []string `json:"keys"`
	Prefixes []string `json:"prefixes"` // folders, moved with everything inside
}

// trashObjects moves objects and folders to the bucket's trash.
func (h *Handler) trashObjects(w http.ResponseWriter, r *http.Request) {
	h.moveTrash(w, r, func(key string) (string, bool) {
		return trashPrefix + key, key != "" && !strings.HasPrefix(key, trashPrefix)
	}, "Items already in the trash are deleted for good, not trashed again.")
}

// restoreObjects moves objects and folders out of the trash, back to where
// they were deleted from. A newer object at that path is replaced.
func (h *Handler) restoreObjects(w http.ResponseWriter, r *http.Request) {
	h.moveTrash(w, r, func(key string) (string, bool) {
		return strings.TrimPrefix(key, trashPrefix), strings.HasPrefix(key, trashPrefix) && key != trashPrefix
	}, "Only items in the trash can be restored.")
}

func (h *Handler) moveTrash(w http.ResponseWriter, r *http.Request, target func(string) (string, bool), invalid string) {
	var in trashRequest
	if !readJSON(w, r, &in) {
		return
	}
	bucket := r.PathValue("bucket")
	for _, k := range append(append([]string{}, in.Keys...), in.Prefixes...) {
		if _, ok := target(k); !ok {
			writeError(w, http.StatusBadRequest, "invalid_key", invalid)
			return
		}
	}
	keys, err := h.expandKeys(r.Context(), bucket, in.Keys, in.Prefixes)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	moved := 0
	for _, k := range keys {
		to, _ := target(k)
		if err := h.moveObject(r.Context(), bucket, k, to); err != nil {
			server.Annotate(r.Context(), slog.Int("moved", moved))
			h.storageError(w, r, err)
			return
		}
		moved++
	}
	server.Annotate(r.Context(), slog.Int("moved", moved))
	writeJSON(w, http.StatusOK, map[string]int{"moved": moved})
}

// putTrashRetention sets how many days trashed files are kept; 0 keeps them
// until the trash is emptied.
func (h *Handler) putTrashRetention(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days int `json:"days"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Days < 0 || in.Days > maxTrashDays {
		writeError(w, http.StatusBadRequest, "invalid_retention", "Keep trashed files 1 to 3650 days, or 0 to keep them until the trash is emptied.")
		return
	}
	name := r.PathValue("bucket")
	if err := h.opts.Store.SetBucketTrashDays(r.Context(), name, in.Days); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("trash retention changed from the console", "bucket", name, "days", in.Days)
	h.getBucket(w, r)
}

func (h *Handler) runTrashPurge() {
	t := time.NewTicker(trashPurgeTick)
	defer t.Stop()
	for ; ; <-t.C {
		if _, err := h.purgeTrash(context.Background(), time.Now()); err != nil {
			h.opts.Log.Error("trash: purging expired files", "err", err)
		}
	}
}

// purgeTrash deletes, in every bucket with a retention, the trashed files
// older than it. It returns how many were deleted.
func (h *Handler) purgeTrash(ctx context.Context, now time.Time) (int, error) {
	buckets, err := h.opts.Store.ListBuckets(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, b := range buckets {
		if b.TrashDays <= 0 {
			continue
		}
		cutoff := now.Add(-time.Duration(b.TrashDays) * 24 * time.Hour)
		objs, err := h.listUnder(ctx, b.Name, trashPrefix)
		if err != nil {
			return total, err
		}
		n := 0
		for _, o := range objs {
			if o.ModTime.After(cutoff) {
				continue
			}
			if err := h.opts.Store.DeleteObject(ctx, b.Name, o.Key); err != nil {
				return total, err
			}
			n++
		}
		if n > 0 {
			h.opts.Log.Info("trash: expired files deleted", "bucket", b.Name, "deleted", n, "days", b.TrashDays)
		}
		total += n
	}
	return total, nil
}
