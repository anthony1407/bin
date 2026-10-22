package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func openTest(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBucketLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	fixed := time.Date(2026, 10, 22, 14, 17, 27, 303_000_000, time.UTC)
	s.now = func() time.Time { return fixed }

	for _, name := range []string{"photos", "backups", "a.b-c"} {
		if err := s.CreateBucket(ctx, name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := s.CreateBucket(ctx, "photos"); !errors.Is(err, ErrBucketExists) {
		t.Errorf("duplicate create: %v", err)
	}

	list, err := s.ListBuckets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range list {
		names = append(names, b.Name)
	}
	if !reflect.DeepEqual(names, []string{"a.b-c", "backups", "photos"}) {
		t.Errorf("list = %v", names)
	}

	b, err := s.GetBucket(ctx, "photos")
	if err != nil || !b.CreatedAt.Equal(fixed) || b.Versioning != "" {
		t.Errorf("get = %+v, %v", b, err)
	}
	if _, err := s.GetBucket(ctx, "missing"); !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("get missing: %v", err)
	}

	if err := s.DeleteBucket(ctx, "photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket(ctx, "photos"); !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestBucketTags(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	s.CreateBucket(ctx, "photos")

	if _, err := s.BucketTags(ctx, "photos"); !errors.Is(err, ErrNoTags) {
		t.Errorf("no tags yet: %v", err)
	}
	want := map[string]string{"team": "home", "env": ""}
	if err := s.SetBucketTags(ctx, "photos", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.BucketTags(ctx, "photos")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %v, %v", got, err)
	}
	if err := s.SetBucketTags(ctx, "photos", map[string]string{"only": "one"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.BucketTags(ctx, "photos"); !reflect.DeepEqual(got, map[string]string{"only": "one"}) {
		t.Errorf("replace: %v", got)
	}
	if err := s.SetBucketTags(ctx, "photos", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BucketTags(ctx, "photos"); !errors.Is(err, ErrNoTags) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.SetBucketTags(ctx, "missing", want); !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("missing bucket: %v", err)
	}

	// Tags go away with their bucket.
	s.SetBucketTags(ctx, "photos", want)
	s.DeleteBucket(ctx, "photos")
	s.CreateBucket(ctx, "photos")
	if _, err := s.BucketTags(ctx, "photos"); !errors.Is(err, ErrNoTags) {
		t.Errorf("recreated bucket inherited tags: %v", err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.CreateBucket(ctx, "survives")
	s.Close()

	s2 := openTest(t, dir)
	if _, err := s2.GetBucket(ctx, "survives"); err != nil {
		t.Fatalf("bucket lost after reopen: %v", err)
	}
}

func TestDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	openTest(t, dir)
	if _, err := Open(dir); !errors.Is(err, ErrDirLocked) {
		t.Fatalf("second Open = %v, want ErrDirLocked", err)
	}
}

func TestLayout(t *testing.T) {
	dir := t.TempDir()
	openTest(t, dir)
	for _, p := range []string{".bin/meta.db", ".bin/lock", ".bin/tmp", "blobs"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, ".bin"))
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf(".bin mode = %v, %v", fi.Mode().Perm(), err)
		}
	}
}

func TestValidBucketName(t *testing.T) {
	valid := []string{"abc", "my-bucket", "my.bucket", "123", "a1-b2.c3"}
	invalid := []string{
		"", "ab", "UPPER", "under_score", "-start", "end-", ".start", "end.",
		"two..dots", "192.168.5.4", "xn--punycode", "sthree-x", "amzn-s3-demo-x",
		"name-s3alias", "name--ol-s3", "name.mrap", "name--x-s3", "name--table-s3",
		"spa ce", "ñandu", string(make([]byte, 64)),
	}
	long := make([]byte, 63)
	for i := range long {
		long[i] = 'a'
	}
	valid = append(valid, string(long))
	invalid = append(invalid, string(long)+"a")
	for _, n := range valid {
		if !ValidBucketName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range invalid {
		if ValidBucketName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}
