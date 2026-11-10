package admin

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"bin/internal/storage"
)

func TestTrashAndRestore(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "docs"}, nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=a.txt", "a", nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=dir%2Fb.txt", "b", nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=dir%2Fsub%2Fc.txt", "c", nil)
	exists := func(key string) bool {
		return e.call("GET", "/api/v1/buckets/docs/object?key="+key, nil, nil) == 200
	}

	var out struct{ Moved int }
	if code := e.call("POST", "/api/v1/buckets/docs/trash", map[string]any{"keys": []string{"a.txt"}, "prefixes": []string{"dir/"}}, &out); code != 200 || out.Moved != 3 {
		t.Fatalf("trash: %d %+v", code, out)
	}
	for _, k := range []string{".trash/a.txt", ".trash/dir/b.txt", ".trash/dir/sub/c.txt"} {
		if !exists(k) {
			t.Errorf("%s not in the trash", k)
		}
	}
	if exists("a.txt") || exists("dir/b.txt") {
		t.Error("trashed objects are still in place")
	}
	if code := e.call("POST", "/api/v1/buckets/docs/trash", map[string]any{"keys": []string{".trash/a.txt"}}, nil); code != 400 {
		t.Errorf("trashing from the trash: %d", code)
	}

	if code := e.call("POST", "/api/v1/buckets/docs/trash/restore", map[string]any{"keys": []string{".trash/a.txt"}, "prefixes": []string{".trash/dir/"}}, &out); code != 200 || out.Moved != 3 {
		t.Fatalf("restore: %d %+v", code, out)
	}
	for _, k := range []string{"a.txt", "dir/b.txt", "dir/sub/c.txt"} {
		if !exists(k) {
			t.Errorf("%s not restored", k)
		}
	}
	if code := e.call("POST", "/api/v1/buckets/docs/trash/restore", map[string]any{"keys": []string{"a.txt"}}, nil); code != 400 {
		t.Errorf("restoring outside the trash: %d", code)
	}

	var b bucketJSON
	if code := e.call("PUT", "/api/v1/buckets/docs/trash/retention", map[string]int{"days": 30}, &b); code != 200 || b.TrashDays != 30 {
		t.Errorf("retention: %d %+v", code, b)
	}
	if code := e.call("PUT", "/api/v1/buckets/docs/trash/retention", map[string]int{"days": -1}, nil); code != 400 {
		t.Errorf("negative retention: %d", code)
	}
}

// In a versioned bucket, emptying the trash, deleting from it, restoring from
// it and its retention must free the space of the trashed copies, while the
// original key keeps its history.
func TestVersionedTrashFreesSpace(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "ver"}, nil)
	e.call("PUT", "/api/v1/buckets/ver/versioning", map[string]string{"status": "Enabled"}, nil)
	data := strings.Repeat("x", 1000)
	for _, k := range []string{"a.bin", "b.bin", "c.bin"} {
		e.call("PUT", "/api/v1/buckets/ver/upload?key="+k, data, nil)
	}
	size := func() int64 {
		var b struct{ Size int64 }
		e.call("GET", "/api/v1/buckets/ver", nil, &b)
		return b.Size
	}
	e.call("POST", "/api/v1/buckets/ver/trash", map[string]any{"keys": []string{"a.bin", "b.bin", "c.bin"}}, nil)
	if got := size(); got != 6000 {
		t.Fatalf("after trashing: %d bytes, want the originals' history plus the trashed copies", got)
	}

	e.call("DELETE", "/api/v1/buckets/ver/objects", map[string]any{"keys": []string{".trash/a.bin"}, "prefixes": []string{}}, nil)
	if got := size(); got != 5000 {
		t.Errorf("after deleting one from the trash: %d", got)
	}
	e.call("POST", "/api/v1/buckets/ver/trash/restore", map[string]any{"keys": []string{".trash/b.bin"}}, nil)
	if got := size(); got != 5000 { // b.bin: old version + restored one; its trashed copy is gone
		t.Errorf("after restoring one: %d", got)
	}
	e.call("DELETE", "/api/v1/buckets/ver/objects", map[string]any{"keys": []string{}, "prefixes": []string{".trash/"}}, nil)
	if got := size(); got != 4000 {
		t.Errorf("after emptying the trash: %d", got)
	}
	var versions struct{ Versions []struct{ DeleteMarker bool } }
	e.call("GET", "/api/v1/buckets/ver/versions?key=a.bin", nil, &versions)
	if len(versions.Versions) != 2 {
		t.Errorf("the original key lost its history: %+v", versions.Versions)
	}
}

func TestPurgeTrash(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, b := range []string{"kept", "expiring"} {
		store.CreateBucket(ctx, b)
		store.SetBucketVersioning(ctx, b, storage.VersioningEnabled) // purges must not leave versions behind
		store.PutObject(ctx, b, ".trash/old.txt", strings.NewReader("x"), 1, storage.PutOptions{})
		store.PutObject(ctx, b, "live.txt", strings.NewReader("x"), 1, storage.PutOptions{})
	}
	store.SetBucketTrashDays(ctx, "expiring", 7)
	h := &Handler{opts: Options{Store: store, Log: log}}

	if n, err := h.purgeTrash(ctx, time.Now().Add(6*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("before the retention: %d %v", n, err)
	}
	if n, err := h.purgeTrash(ctx, time.Now().Add(8*24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("after the retention: %d %v", n, err)
	}
	if _, err := store.StatObject(ctx, "expiring", ".trash/old.txt"); err == nil {
		t.Error("expired file still in the trash")
	}
	if usage, _ := store.BucketUsage(ctx); usage["expiring"].Bytes != 1 {
		t.Errorf("expired file still takes space: %+v", usage["expiring"])
	}
	for _, c := range [][2]string{{"expiring", "live.txt"}, {"kept", ".trash/old.txt"}} {
		if _, err := store.StatObject(ctx, c[0], c[1]); err != nil {
			t.Errorf("%s/%s was deleted", c[0], c[1])
		}
	}
}
