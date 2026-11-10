package storage

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
)

func TestRenameObjects(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "docs")
	a := put(t, s, "docs", "a.txt", "alpha", PutOptions{ContentType: "text/plain"})
	put(t, s, "docs", "b.txt", "beta", PutOptions{})
	old := put(t, s, "docs", "taken.txt", "replaced", PutOptions{})

	n, err := s.RenameObjects(ctx, "docs", []Rename{
		{"a.txt", ".trash/a.txt"},
		{"b.txt", "taken.txt"},     // replaces an existing object
		{"missing.txt", "x.txt"},   // gone already: skipped
		{"taken.txt", "taken.txt"}, // a no-op... after b.txt landed there
	})
	if err != nil || n != 2 {
		t.Fatalf("rename: %d %v", n, err)
	}

	info, data := readAll(t, s, "docs", ".trash/a.txt")
	if data != "alpha" || info.ContentType != "text/plain" || info.ETag != a.ETag {
		t.Errorf("renamed object: %q %+v", data, info)
	}
	if !info.ModTime.After(a.ModTime) {
		t.Error("a renamed object keeps its old modification time")
	}
	if _, data := readAll(t, s, "docs", "taken.txt"); data != "beta" {
		t.Errorf("replaced destination holds %q", data)
	}
	for _, k := range []string{"a.txt", "b.txt", "x.txt"} {
		if _, err := s.StatObject(ctx, "docs", k); !errors.Is(err, ErrNoSuchKey) {
			t.Errorf("%s: %v", k, err)
		}
	}
	// The replaced object's data is collected; the renamed ones' is not.
	if _, err := os.Stat(s.blobPath(old.blob)); !os.IsNotExist(err) {
		t.Errorf("replaced blob still on disk: %v", err)
	}
	usage, _ := s.BucketUsage(ctx)
	if u := usage["docs"]; u.Objects != 2 || u.Bytes != int64(len("alpha")+len("beta")) {
		t.Errorf("usage after rename: %+v", u)
	}

	if _, err := s.RenameObjects(ctx, "docs", []Rename{{"taken.txt", ""}}); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("empty destination: %v", err)
	}
	if _, err := s.RenameObjects(ctx, "nope", []Rename{{"a", "b"}}); !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("missing bucket: %v", err)
	}

	mustCreate(t, s, "versioned")
	if err := s.SetBucketVersioning(ctx, "versioned", VersioningEnabled); err != nil {
		t.Fatal(err)
	}
	put(t, s, "versioned", "v.txt", "v", PutOptions{})
	if _, err := s.RenameObjects(ctx, "versioned", []Rename{{"v.txt", "w.txt"}}); !errors.Is(err, ErrRenameVersioned) {
		t.Errorf("versioned bucket: %v", err)
	}
}

// Renames beyond one transaction's batch all apply.
func TestRenameObjectsBatches(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "many")
	var renames []Rename
	for i := range renameBatch + 7 {
		k := string(rune('a'+i%26)) + "/" + strconv.Itoa(i)
		put(t, s, "many", k, "x", PutOptions{})
		renames = append(renames, Rename{k, "moved/" + k})
	}
	if n, err := s.RenameObjects(ctx, "many", renames); err != nil || n != len(renames) {
		t.Fatalf("rename: %d %v", n, err)
	}
	if u, err := s.PrefixUsage(ctx, "many", []string{"moved/"}); err != nil || u["moved/"].Objects != int64(len(renames)) {
		t.Errorf("moved/: %+v %v", u, err)
	}
}
