package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// serve reads an object the way the GET handlers do.
func serve(t *testing.T, s *Store, bucket, key string) (string, error) {
	t.Helper()
	_, d, err := s.OpenObject(context.Background(), bucket, key)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := io.ReadAll(d.Reader())
	return string(got), err
}

func corrupt(t *testing.T, s *Store, info ObjectInfo) {
	t.Helper()
	path := s.blobPath(info.blob)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestReadVerifyPolicies(t *testing.T) {
	sizes := map[string]int{"cached": 1000, "streamed": MaxCachedBlob + holdback + 1}
	for name, size := range sizes {
		data := strings.Repeat("a", size)
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				policy      ReadVerify
				readFirst   bool // a clean read before the damage
				wantCorrupt bool
			}{
				{VerifyOnce, false, true},
				{VerifyOnce, true, false}, // trusted after the first check
				{VerifyAlways, true, true},
				{VerifyOff, false, false},
			} {
				s := openTest(t, t.TempDir())
				s.SetReadVerify(tc.policy)
				mustCreate(t, s, "bkt")
				info := put(t, s, "bkt", "k", data, PutOptions{})
				if tc.readFirst {
					if got, err := serve(t, s, "bkt", "k"); err != nil || got != data {
						t.Fatalf("policy %d: clean read: %d bytes, %v", tc.policy, len(got), err)
					}
				}
				corrupt(t, s, info)
				got, err := serve(t, s, "bkt", "k")
				if tc.wantCorrupt {
					if !errors.Is(err, ErrCorrupt) || len(got) >= size {
						t.Errorf("policy %d: got %d bytes, %v; want ErrCorrupt before the end", tc.policy, len(got), err)
					}
				} else if err != nil || len(got) != size {
					t.Errorf("policy %d: got %d bytes, %v; want the whole object", tc.policy, len(got), err)
				}
				// Copies always verify, whatever the policy.
				if _, err := s.CopyObject(context.Background(), "bkt", "k", "bkt", "copy", CopyOptions{}); !errors.Is(err, ErrCorrupt) {
					t.Errorf("policy %d: copy of corrupt object: %v", tc.policy, err)
				}
			}
		})
	}
}

func TestReadsSeeEveryWrite(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "bkt")
	for _, v := range []string{"one", "two", strings.Repeat("3", MaxCachedBlob+1)} {
		put(t, s, "bkt", "k", v, PutOptions{})
		if got, err := serve(t, s, "bkt", "k"); err != nil || got != v {
			t.Fatalf("read after writing %d bytes: %d bytes, %v", len(v), len(got), err)
		}
		if got, err := serve(t, s, "bkt", "k"); err != nil || got != v {
			t.Fatalf("second read: %d bytes, %v", len(got), err)
		}
	}
	if err := s.DeleteObject(ctx, "bkt", "k"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenObject(ctx, "bkt", "k"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("read after delete: %v", err)
	}
	if err := s.DeleteBucket(ctx, "bkt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StatObject(ctx, "bkt", "k"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("stat in deleted bucket: %v", err)
	}
}

func TestCachedReadKeepsBlobUntilClosed(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "bkt")
	old := put(t, s, "bkt", "k", "old", PutOptions{})
	serve(t, s, "bkt", "k") // now in the read cache

	_, d, err := s.OpenObject(ctx, "bkt", "k")
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, "bkt", "k", "new", PutOptions{})
	if got, err := io.ReadAll(d.Reader()); err != nil || string(got) != "old" {
		t.Fatalf("open reader after overwrite = %q, %v", got, err)
	}
	if _, err := os.Stat(s.blobPath(old.blob)); err != nil {
		t.Fatalf("blob removed while open: %v", err)
	}
	d.Close()
	if _, err := os.Stat(s.blobPath(old.blob)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blob kept after the last reader: %v", err)
	}
	if _, ok := s.hot.get(old.blob); ok {
		t.Fatal("deleted blob still in the read cache")
	}
}

func TestBlobCacheBound(t *testing.T) {
	c := newBlobCache(10)
	c.put("a", []byte("12345"))
	c.put("b", []byte("12345"))
	c.get("a") // b is now the least recently used
	c.put("c", []byte("1"))
	if _, ok := c.get("b"); ok {
		t.Error("least recently used entry kept past the bound")
	}
	for _, id := range []string{"a", "c"} {
		if _, ok := c.get(id); !ok {
			t.Errorf("%s evicted", id)
		}
	}
	c.put("big", make([]byte, 11))
	if _, ok := c.get("big"); ok || c.size > c.max {
		t.Errorf("oversized entry cached (size %d)", c.size)
	}
}
