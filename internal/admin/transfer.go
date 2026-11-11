package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"bin/internal/server"
	"bin/internal/storage"
)

// Copying and moving from the explorer happens server side: the data never
// passes through the browser.
//
// Within a never-versioned bucket a move is a rename: only metadata changes,
// so the trash and same-bucket moves cost the same whatever the size. Other
// copies are verified against the source's SHA-256 and run several at a
// time, so their commits share fsyncs. Moving to another bucket copies first
// and only then sends the originals to the source bucket's trash, so a
// failure never loses anything.

// transferWorkers is how many objects are copied at once.
const transferWorkers = 8

// parallel runs fn on every item, transferWorkers at a time, and stops
// handing out items at the first error. It reports which items succeeded.
func parallel[T any](ctx context.Context, items []T, fn func(context.Context, T) error) ([]bool, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	ok := make([]bool, len(items))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(transferWorkers, len(items)) {
		wg.Go(func() {
			for i := range next {
				if err := fn(ctx, items[i]); err != nil {
					cancel(err)
					continue
				}
				ok[i] = true
			}
		})
	}
feed:
	for i := range items {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if ctx.Err() != nil {
		return ok, context.Cause(ctx)
	}
	return ok, nil
}

func countTrue(ok []bool) int {
	n := 0
	for _, b := range ok {
		if b {
			n++
		}
	}
	return n
}

// relocate moves objects to new keys within bucket: renamed in place when
// the bucket was never versioned, otherwise copied (verified) and then
// deleted, which leaves the history of versioned keys where it was. Sources
// already gone are skipped. It returns how many objects moved.
func (h *Handler) relocate(ctx context.Context, bucket string, moves []storage.Rename) (int, error) {
	if len(moves) == 0 {
		return 0, nil
	}
	n, err := h.opts.Store.RenameObjects(ctx, bucket, moves)
	if !errors.Is(err, storage.ErrRenameVersioned) {
		return n, err
	}
	var moved atomic.Int64 // sources already gone are skipped, not counted
	_, err = parallel(ctx, moves, func(ctx context.Context, m storage.Rename) error {
		if m.From == m.To {
			return nil
		}
		err := h.moveObject(ctx, bucket, m.From, m.To)
		switch {
		case err == nil:
			moved.Add(1)
		case !errors.Is(err, storage.ErrNoSuchKey):
			return err
		}
		return nil
	})
	return int(moved.Load()), err
}

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
	ctx := r.Context()
	bucket := r.PathValue("bucket")
	target := in.TargetBucket
	if target == "" {
		target = bucket
	}
	// The route lets users through with read access to the source: a copy
	// also writes the target, and a move takes the originals away.
	if !may(r, target, true) || (in.Move && !may(r, bucket, true)) {
		denyUser(w)
		return
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

	var pairs []storage.Rename
	add := func(from, to string) {
		if target != bucket || from != to { // already there otherwise
			pairs = append(pairs, storage.Rename{From: from, To: to})
		}
	}
	for _, k := range in.Keys {
		add(k, dst+lastSegment(k))
	}
	for _, p := range in.Prefixes {
		objs, err := h.listUnder(ctx, bucket, p)
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		to := dst + lastSegment(p) + "/"
		for _, o := range objs {
			add(o.Key, to+strings.TrimPrefix(o.Key, p))
		}
	}

	done, trashed := 0, 0
	var err error
	if in.Move && target == bucket {
		done, err = h.relocate(ctx, bucket, pairs)
	} else {
		var ok []bool
		ok, err = parallel(ctx, pairs, func(ctx context.Context, p storage.Rename) error {
			_, err := h.opts.Store.CopyObject(ctx, bucket, p.From, target, p.To, storage.CopyOptions{})
			return err
		})
		done = countTrue(ok)
		if in.Move {
			// Only originals whose copy is in place leave, even after a failure.
			var originals []storage.Rename
			for i, p := range pairs {
				if ok[i] {
					originals = append(originals, storage.Rename{From: p.From, To: trashPrefix + p.From})
				}
			}
			var terr error
			trashed, terr = h.relocate(context.WithoutCancel(ctx), bucket, originals)
			if err == nil {
				err = terr
			}
		}
	}
	server.Annotate(ctx, slog.Int("copied", done), slog.Int("trashed", trashed), slog.Bool("move", in.Move))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"copied": done, "trashed": trashed})
}
