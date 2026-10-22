package storage

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bin/internal/checksum"
)

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func put(t *testing.T, s *Store, bucket, key, data string, opts PutOptions) ObjectInfo {
	t.Helper()
	info, err := s.PutObject(context.Background(), bucket, key, strings.NewReader(data), int64(len(data)), opts)
	if err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
	return info
}

func readAll(t *testing.T, s *Store, bucket, key string) (ObjectInfo, string) {
	t.Helper()
	info, f, err := s.OpenObject(context.Background(), bucket, key)
	if err != nil {
		t.Fatalf("open %s/%s: %v", bucket, key, err)
	}
	defer f.Close()
	data, err := io.ReadAll(NewVerifiedReader(f, info.SHA256))
	if err != nil {
		t.Fatalf("read %s/%s: %v", bucket, key, err)
	}
	return info, string(data)
}

func TestPutGetDelete(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "photos")
	blobs := filepath.Join(dir, "blobs")

	info := put(t, s, "photos", "dir/ñandú +1.txt", "hello bin\n", PutOptions{
		ContentType: "text/plain",
		UserMeta:    map[string]string{"md5chksum": "DHDwCX81EAWKYyE9ga0Dbg=="},
		Headers:     map[string]string{"Cache-Control": "no-cache"},
		Checksums:   []checksum.Algorithm{checksum.CRC64NVME, checksum.SHA256},
	})
	sum := md5.Sum([]byte("hello bin\n"))
	if info.ETag != hex.EncodeToString(sum[:]) || info.Size != 10 {
		t.Errorf("etag %s size %d", info.ETag, info.Size)
	}
	if info.Checksums[checksum.CRC64NVME] != "YF6KHApiEII=" || info.ChecksumType != "FULL_OBJECT" {
		t.Errorf("checksums %v %s", info.Checksums, info.ChecksumType) // value sent by aws-cli for this body
	}

	got, data := readAll(t, s, "photos", "dir/ñandú +1.txt")
	if data != "hello bin\n" || got.ContentType != "text/plain" || got.UserMeta["md5chksum"] != "DHDwCX81EAWKYyE9ga0Dbg==" ||
		got.Headers["Cache-Control"] != "no-cache" || got.Checksums[checksum.SHA256] == "" {
		t.Errorf("read back %+v %q", got, data)
	}

	// Overwriting replaces the blob; the old one is removed from disk.
	put(t, s, "photos", "dir/ñandú +1.txt", "second version", PutOptions{})
	if _, data := readAll(t, s, "photos", "dir/ñandú +1.txt"); data != "second version" {
		t.Errorf("after overwrite: %q", data)
	}
	if n := countFiles(t, blobs); n != 1 {
		t.Errorf("%d blob files after overwrite, want 1", n)
	}

	if err := s.DeleteObject(ctx, "photos", "dir/ñandú +1.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StatObject(ctx, "photos", "dir/ñandú +1.txt"); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("stat after delete: %v", err)
	}
	if n := countFiles(t, blobs); n != 0 {
		t.Errorf("%d blob files after delete, want 0", n)
	}
	if err := s.DeleteObject(ctx, "photos", "never-existed"); err != nil {
		t.Errorf("deleting a missing key must succeed: %v", err)
	}
	if err := s.DeleteObject(ctx, "nobucket", "k"); !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("missing bucket: %v", err)
	}
}

func TestPutFailuresLeaveNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "bkt")

	verifyErr := errors.New("checksum mismatch")
	_, err := s.PutObject(ctx, "bkt", "k", strings.NewReader("data"), 4, PutOptions{
		Verify: func(Sums) error { return verifyErr },
	})
	if !errors.Is(err, verifyErr) {
		t.Errorf("verify failure: %v", err)
	}
	_, err = s.PutObject(ctx, "bkt", "k", strings.NewReader("short"), 100, PutOptions{})
	if !errors.Is(err, ErrIncompleteBody) {
		t.Errorf("short body: %v", err)
	}
	_, err = s.PutObject(ctx, "bkt", "k", strings.NewReader("too long"), 3, PutOptions{})
	if !errors.Is(err, ErrIncompleteBody) {
		t.Errorf("long body: %v", err)
	}
	_, err = s.PutObject(ctx, "nobucket", "k", strings.NewReader("x"), 1, PutOptions{})
	if !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("missing bucket: %v", err)
	}
	if _, err := s.StatObject(ctx, "bkt", "k"); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("failed puts must not create the object: %v", err)
	}
	if n := countFiles(t, filepath.Join(dir, "blobs")) + countFiles(t, filepath.Join(dir, ".bin", "tmp")); n != 0 {
		t.Errorf("%d files left behind by failed puts", n)
	}
}

func TestConditionalPut(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "bkt")
	first := put(t, s, "bkt", "k", "one", PutOptions{IfNoneMatch: "*"})

	_, err := s.PutObject(ctx, "bkt", "k", strings.NewReader("two"), 3, PutOptions{IfNoneMatch: "*"})
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("If-None-Match * on existing key: %v", err)
	}
	_, err = s.PutObject(ctx, "bkt", "k", strings.NewReader("two"), 3, PutOptions{IfMatch: `"nomatch"`})
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("If-Match mismatch: %v", err)
	}
	put(t, s, "bkt", "k", "two", PutOptions{IfMatch: `"` + first.ETag + `"`})
	_, err = s.PutObject(ctx, "bkt", "missing", strings.NewReader("x"), 1, PutOptions{IfMatch: "*"})
	if !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("If-Match on missing key: %v", err)
	}
}

func TestCorruptionIsDetected(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "bkt")
	data := strings.Repeat("0123456789", 30000) // 300 KB, larger than the holdback
	info := put(t, s, "bkt", "k", data, PutOptions{})

	// Flip one byte on disk, as a failing disk would.
	path := s.blobPath(info.blob)
	raw, _ := os.ReadFile(path)
	raw[1000] ^= 0xff
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}

	_, f, err := s.OpenObject(ctx, "bkt", "k")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(NewVerifiedReader(f, info.SHA256))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
	if len(got) >= len(data) {
		t.Errorf("reader released all %d bytes of corrupt data", len(got))
	}

	// Copying corrupt data fails and creates nothing.
	if _, err := s.CopyObject(ctx, "bkt", "k", "bkt", "copy", CopyOptions{}); !errors.Is(err, ErrCorrupt) {
		t.Errorf("copy of corrupt object: %v", err)
	}
	if _, err := s.StatObject(ctx, "bkt", "copy"); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("corrupt copy was created: %v", err)
	}
}

func TestVerifiedReaderSmallAndEmpty(t *testing.T) {
	for _, data := range []string{"", "x", strings.Repeat("y", holdback), strings.Repeat("z", holdback+1)} {
		sum := sha256Sum([]byte(data))
		got, err := io.ReadAll(NewVerifiedReader(strings.NewReader(data), sum))
		if err != nil || string(got) != data {
			t.Errorf("len %d: got %d bytes, %v", len(data), len(got), err)
		}
	}
}

func TestCopyObject(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "src")
	mustCreate(t, s, "dst")
	src := put(t, s, "src", "a.txt", "copy me", PutOptions{
		ContentType: "text/plain",
		UserMeta:    map[string]string{"author": "bin"},
		Checksums:   []checksum.Algorithm{checksum.CRC32C},
	})

	cp, err := s.CopyObject(ctx, "src", "a.txt", "dst", "b.txt", CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cp.ETag != src.ETag || cp.Checksums[checksum.CRC32C] != src.Checksums[checksum.CRC32C] {
		t.Errorf("copy etag/checksum differ: %+v vs %+v", cp, src)
	}
	got, data := readAll(t, s, "dst", "b.txt")
	if data != "copy me" || got.UserMeta["author"] != "bin" || got.ContentType != "text/plain" {
		t.Errorf("copied object %+v %q", got, data)
	}

	_, err = s.CopyObject(ctx, "src", "a.txt", "dst", "c.txt", CopyOptions{
		ReplaceMetadata: true, ContentType: "application/x-bin", UserMeta: map[string]string{"fresh": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = readAll(t, s, "dst", "c.txt")
	if got.ContentType != "application/x-bin" || got.UserMeta["fresh"] != "yes" || got.UserMeta["author"] != "" {
		t.Errorf("replaced metadata %+v", got)
	}

	checkErr := errors.New("condition failed")
	_, err = s.CopyObject(ctx, "src", "a.txt", "dst", "d.txt", CopyOptions{Check: func(ObjectInfo) error { return checkErr }})
	if !errors.Is(err, checkErr) {
		t.Errorf("check: %v", err)
	}
	if _, err := s.CopyObject(ctx, "src", "missing", "dst", "e.txt", CopyOptions{}); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("missing source: %v", err)
	}
}

func TestDeleteNonEmptyBucket(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "bkt")
	put(t, s, "bkt", "k", "x", PutOptions{})
	if err := s.DeleteBucket(ctx, "bkt"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Fatalf("delete non-empty: %v", err)
	}
	s.DeleteObject(ctx, "bkt", "k")
	if err := s.DeleteBucket(ctx, "bkt"); err != nil {
		t.Fatalf("delete emptied bucket: %v", err)
	}
}

func TestValidateKey(t *testing.T) {
	if err := ValidateKey(strings.Repeat("k", MaxKeyLength)); err != nil {
		t.Error(err)
	}
	if err := ValidateKey(strings.Repeat("k", MaxKeyLength+1)); !errors.Is(err, ErrKeyTooLong) {
		t.Error(err)
	}
	if err := ValidateKey("bad\xffutf8"); !errors.Is(err, ErrInvalidKey) {
		t.Error(err)
	}
}

// TestRecoverAfterCrash builds the state a crash can leave behind and checks
// that reopening the store repairs it without touching live data.
func TestRecoverAfterCrash(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLog)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			s.Close()
		}
	})
	mustCreate(t, s, "bkt")
	live := put(t, s, "bkt", "live", "keep me", PutOptions{})

	// 1. an upload interrupted mid-write
	os.WriteFile(filepath.Join(dir, ".bin", "tmp", "half-written"), []byte("partial"), 0o640)
	// 2. a blob renamed into place whose metadata commit never happened
	orphan := newBlobID()
	s.mkdirDurable(filepath.Dir(s.blobPath(orphan)))
	os.WriteFile(s.blobPath(orphan), []byte("orphan"), 0o640)
	// 3. a replaced blob queued for deletion but never deleted
	queued := newBlobID()
	s.mkdirDurable(filepath.Dir(s.blobPath(queued)))
	os.WriteFile(s.blobPath(queued), []byte("old"), 0o640)
	s.write.Exec(`INSERT INTO gc_blobs (blob, queued_at) VALUES (?, 0)`, queued)
	s.Close()
	closed = true

	s2 := openTest(t, dir)
	for _, id := range []string{orphan, queued} {
		if _, err := os.Stat(s2.blobPath(id)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("blob %s survived recovery", id)
		}
	}
	if n := countFiles(t, filepath.Join(dir, ".bin", "tmp")); n != 0 {
		t.Errorf("%d temp files survived recovery", n)
	}
	var queuedRows int
	s2.read.QueryRow(`SELECT count(*) FROM gc_blobs`).Scan(&queuedRows)
	if queuedRows != 0 {
		t.Errorf("%d gc rows left", queuedRows)
	}
	if _, data := readAll(t, s2, "bkt", "live"); data != "keep me" {
		t.Errorf("live object damaged: %q", data)
	}
	if _, err := os.Stat(s2.blobPath(live.blob)); err != nil {
		t.Errorf("live blob removed: %v", err)
	}
}

func sha256Sum(b []byte) []byte {
	h := checksum.SHA256.New()
	h.Write(b)
	return h.Sum(nil)
}

func mustCreate(t *testing.T, s *Store, bucket string) {
	t.Helper()
	if err := s.CreateBucket(context.Background(), bucket); err != nil {
		t.Fatalf("create bucket %s: %v", bucket, err)
	}
}
