package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bin/internal/checksum"
)

func randomBytes(seed uint64, n int) []byte {
	rng := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

func putPart(t *testing.T, s *Store, bucket, key, id string, n int, data []byte) PartInfo {
	t.Helper()
	p, err := s.PutPart(context.Background(), bucket, key, id, n, bytes.NewReader(data), int64(len(data)), PartOptions{})
	if err != nil {
		t.Fatalf("part %d: %v", n, err)
	}
	return p
}

func completeAll(t *testing.T, s *Store, bucket, key, id string, parts ...PartInfo) ObjectInfo {
	t.Helper()
	var list []CompletePart
	for _, p := range parts {
		list = append(list, CompletePart{Number: p.Number, ETag: `"` + p.ETag + `"`})
	}
	info, err := s.CompleteUpload(context.Background(), bucket, key, id, CompleteOptions{Parts: list, ObjectSize: -1})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	return info
}

func blobCount(t *testing.T, dir string) int { return countFiles(t, filepath.Join(dir, "blobs")) }

func TestMultipartRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "mpu")
	p1, p2, p3 := randomBytes(1, MinPartSize+123), randomBytes(2, MinPartSize), randomBytes(3, 1000)
	whole := append(append(append([]byte{}, p1...), p2...), p3...)

	u, err := s.CreateUpload(ctx, "mpu", "big.bin", UploadOptions{
		ContentType: "application/x-big", UserMeta: map[string]string{"md5chksum": "abc"},
		ChecksumAlgorithm: checksum.CRC64NVME,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.ChecksumType != "FULL_OBJECT" {
		t.Errorf("CRC64NVME default type = %q", u.ChecksumType)
	}
	parts := []PartInfo{putPart(t, s, "mpu", "big.bin", u.ID, 1, p1), putPart(t, s, "mpu", "big.bin", u.ID, 2, p2), putPart(t, s, "mpu", "big.bin", u.ID, 3, p3)}
	info := completeAll(t, s, "mpu", "big.bin", u.ID, parts...)

	// ETag: MD5 of the concatenated binary part MD5s, then "-<count>".
	h := md5.New()
	for _, p := range [][]byte{p1, p2, p3} {
		sum := md5.Sum(p)
		h.Write(sum[:])
	}
	if want := hex.EncodeToString(h.Sum(nil)) + "-3"; info.ETag != want {
		t.Errorf("etag %s, want %s", info.ETag, want)
	}
	// Full-object CRC64NVME combined from the parts equals the CRC of the data.
	direct := checksum.CRC64NVME.New()
	direct.Write(whole)
	if got, want := info.Checksums[checksum.CRC64NVME], checksum.Encode(direct.Sum(nil)); got != want || info.ChecksumType != "FULL_OBJECT" {
		t.Errorf("full-object CRC64NVME %s (%s), want %s", got, info.ChecksumType, want)
	}

	got, d, err := s.OpenObject(ctx, "mpu", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(d.Verified())
	if err != nil || !bytes.Equal(data, whole) {
		t.Fatalf("read back %d bytes, %v", len(data), err)
	}
	if got.PartsCount != 3 || got.Size != int64(len(whole)) || got.ContentType != "application/x-big" || got.UserMeta["md5chksum"] != "abc" {
		t.Errorf("stat %+v", got)
	}
	off, size, ok := got.Part(2)
	if !ok || off != int64(len(p1)) || size != int64(len(p2)) {
		t.Errorf("part 2 at %d+%d (%v)", off, size, ok)
	}
	// A range spanning two parts.
	r, _ := io.ReadAll(d.Range(int64(len(p1))-10, 20))
	if !bytes.Equal(r, whole[len(p1)-10:len(p1)+10]) {
		t.Error("cross-part range mismatch")
	}
	d.Close()

	if n := blobCount(t, dir); n != 3 {
		t.Errorf("%d blobs, want 3 (parts are not copied)", n)
	}
	if err := s.DeleteObject(ctx, "mpu", "big.bin"); err != nil {
		t.Fatal(err)
	}
	if n := blobCount(t, dir); n != 0 {
		t.Errorf("%d blobs left after deleting a multipart object", n)
	}
}

func TestMultipartChecksumTypes(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "sums")
	p1, p2 := randomBytes(4, MinPartSize), randomBytes(5, 777)
	for _, tc := range []struct {
		alg     checksum.Algorithm
		typ     string
		want    string
		wantErr error
	}{
		{checksum.CRC32, "", "COMPOSITE", nil},
		{checksum.CRC32, "FULL_OBJECT", "FULL_OBJECT", nil},
		{checksum.CRC32C, "FULL_OBJECT", "FULL_OBJECT", nil},
		{checksum.SHA256, "", "COMPOSITE", nil},
		{checksum.SHA1, "FULL_OBJECT", "", ErrInvalidChecksumType},
		{checksum.CRC64NVME, "COMPOSITE", "", ErrInvalidChecksumType},
	} {
		name := fmt.Sprintf("%s/%s", tc.alg, tc.typ)
		u, err := s.CreateUpload(ctx, "sums", name, UploadOptions{ChecksumAlgorithm: tc.alg, ChecksumType: tc.typ})
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: create err %v", name, err)
		}
		if err != nil {
			continue
		}
		a, b := putPart(t, s, "sums", name, u.ID, 1, p1), putPart(t, s, "sums", name, u.ID, 2, p2)
		info := completeAll(t, s, "sums", name, u.ID, a, b)
		var want string
		if tc.want == "FULL_OBJECT" {
			h := tc.alg.New()
			h.Write(p1)
			h.Write(p2)
			want = checksum.Encode(h.Sum(nil))
		} else {
			h := tc.alg.New()
			for _, p := range [][]byte{p1, p2} {
				ph := tc.alg.New()
				ph.Write(p)
				h.Write(ph.Sum(nil))
			}
			want = checksum.Encode(h.Sum(nil)) + "-2"
		}
		if info.ChecksumType != tc.want || info.Checksums[tc.alg] != want {
			t.Errorf("%s: got %s %q, want %s %q", name, info.ChecksumType, info.Checksums[tc.alg], tc.want, want)
		}
	}
}

func TestCompleteErrors(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "errs")
	u, _ := s.CreateUpload(ctx, "errs", "k", UploadOptions{ChecksumAlgorithm: checksum.CRC64NVME})
	small := putPart(t, s, "errs", "k", u.ID, 1, randomBytes(6, 100))
	big := putPart(t, s, "errs", "k", u.ID, 2, randomBytes(7, MinPartSize))
	q := func(p PartInfo) CompletePart { return CompletePart{Number: p.Number, ETag: p.ETag} }

	for name, tc := range map[string]struct {
		opts CompleteOptions
		want error
	}{
		"no parts":        {CompleteOptions{ObjectSize: -1}, ErrInvalidPart},
		"unknown part":    {CompleteOptions{Parts: []CompletePart{{Number: 9, ETag: big.ETag}}, ObjectSize: -1}, ErrInvalidPart},
		"wrong etag":      {CompleteOptions{Parts: []CompletePart{{Number: 2, ETag: "deadbeef"}}, ObjectSize: -1}, ErrInvalidPart},
		"wrong checksum":  {CompleteOptions{Parts: []CompletePart{{Number: 2, ETag: big.ETag, Checksums: map[checksum.Algorithm]string{checksum.CRC64NVME: "AAAAAAAAAAA="}}}, ObjectSize: -1}, ErrInvalidPart},
		"descending":      {CompleteOptions{Parts: []CompletePart{q(big), q(small)}, ObjectSize: -1}, ErrInvalidPartOrder},
		"small non-last":  {CompleteOptions{Parts: []CompletePart{q(small), q(big)}, ObjectSize: -1}, ErrEntityTooSmall},
		"size mismatch":   {CompleteOptions{Parts: []CompletePart{q(big)}, ObjectSize: 1}, ErrSizeMismatch},
		"bad full digest": {CompleteOptions{Parts: []CompletePart{q(big)}, ObjectSize: -1, FullChecksum: "AAAAAAAAAAA="}, ErrBadDigest},
		"missing upload":  {CompleteOptions{Parts: []CompletePart{q(big)}, ObjectSize: -1}, ErrNoSuchUpload},
	} {
		id := u.ID
		if name == "missing upload" {
			id = "nope"
		}
		if _, err := s.CompleteUpload(ctx, "errs", "k", id, tc.opts); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	// A failed completion leaves the upload intact.
	if _, parts, _, err := s.ListParts(ctx, "errs", "k", u.ID, 0, 100); err != nil || len(parts) != 2 {
		t.Errorf("upload damaged by failed completions: %d parts, %v", len(parts), err)
	}
	// The last part may be small; the right part list completes.
	if _, err := s.CompleteUpload(ctx, "errs", "k", u.ID, CompleteOptions{Parts: []CompletePart{q(big)}, ObjectSize: MinPartSize}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutPart(ctx, "errs", "k", u.ID, 3, bytes.NewReader(nil), 0, PartOptions{}); !errors.Is(err, ErrNoSuchUpload) {
		t.Errorf("part after completion: %v", err)
	}
}

func TestMultipartNoLeakedBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "leaks")

	// Abort removes every part.
	u, _ := s.CreateUpload(ctx, "leaks", "a", UploadOptions{})
	putPart(t, s, "leaks", "a", u.ID, 1, randomBytes(8, 10))
	putPart(t, s, "leaks", "a", u.ID, 2, randomBytes(9, 10))
	if err := s.AbortUpload(ctx, "leaks", "a", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AbortUpload(ctx, "leaks", "a", u.ID); !errors.Is(err, ErrNoSuchUpload) {
		t.Errorf("abort twice: %v", err)
	}
	if n := blobCount(t, dir); n != 0 {
		t.Errorf("abort left %d blobs", n)
	}

	// Re-uploading a part replaces its blob; unlisted parts go on completion.
	u, _ = s.CreateUpload(ctx, "leaks", "b", UploadOptions{})
	putPart(t, s, "leaks", "b", u.ID, 1, randomBytes(10, 10))
	p1 := putPart(t, s, "leaks", "b", u.ID, 1, randomBytes(11, 10))
	putPart(t, s, "leaks", "b", u.ID, 2, randomBytes(12, 10))
	completeAll(t, s, "leaks", "b", u.ID, p1)
	if n := blobCount(t, dir); n != 1 {
		t.Errorf("%d blobs after completing with one part, want 1", n)
	}

	// Overwriting a multipart object with a single PUT drops its parts.
	put(t, s, "leaks", "b", "small now", PutOptions{})
	if n := blobCount(t, dir); n != 1 {
		t.Errorf("%d blobs after overwrite, want 1", n)
	}

	// Deleting a bucket aborts its uploads.
	u, _ = s.CreateUpload(ctx, "leaks", "c", UploadOptions{})
	putPart(t, s, "leaks", "c", u.ID, 1, randomBytes(13, 10))
	s.DeleteObject(ctx, "leaks", "b")
	if err := s.DeleteBucket(ctx, "leaks"); err != nil {
		t.Fatal(err)
	}
	if n := blobCount(t, dir); n != 0 {
		t.Errorf("%d blobs after deleting the bucket", n)
	}

	// Expiry aborts old uploads only.
	mustCreate(t, s, "expiry")
	old, _ := s.CreateUpload(ctx, "expiry", "old", UploadOptions{})
	putPart(t, s, "expiry", "old", old.ID, 1, randomBytes(14, 10))
	n, err := s.expireUploads(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 1 {
		t.Errorf("expired %d, %v", n, err)
	}
	if n := blobCount(t, dir); n != 0 {
		t.Errorf("%d blobs after expiry", n)
	}
}

// A reader keeps its data even if the object is overwritten mid-read; the old
// blobs are deleted once it closes.
func TestReaderSurvivesOverwrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "race")
	u, _ := s.CreateUpload(ctx, "race", "k", UploadOptions{})
	p1, p2 := randomBytes(15, MinPartSize), randomBytes(16, 1234)
	completeAll(t, s, "race", "k", u.ID, putPart(t, s, "race", "k", u.ID, 1, p1), putPart(t, s, "race", "k", u.ID, 2, p2))

	_, d, err := s.OpenObject(ctx, "race", "k")
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, "race", "k", "replacement", PutOptions{})
	data, err := io.ReadAll(d.Verified())
	if err != nil || !bytes.Equal(data, append(append([]byte{}, p1...), p2...)) {
		t.Fatalf("reader lost its data: %d bytes, %v", len(data), err)
	}
	if n := blobCount(t, dir); n != 3 {
		t.Errorf("%d blobs while the reader is open, want 3", n)
	}
	d.Close()
	if n := blobCount(t, dir); n != 1 {
		t.Errorf("%d blobs after the reader closed, want 1", n)
	}
}

// The orphan sweep at startup must recognise blobs held by in-progress
// uploads and by completed multipart objects.
func TestRecoverKeepsPartBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, testLog)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "keep")
	done, _ := s.CreateUpload(ctx, "keep", "done", UploadOptions{})
	p1, p2 := randomBytes(20, MinPartSize), randomBytes(21, 10)
	completeAll(t, s, "keep", "done", done.ID, putPart(t, s, "keep", "done", done.ID, 1, p1), putPart(t, s, "keep", "done", done.ID, 2, p2))
	pending, _ := s.CreateUpload(ctx, "keep", "pending", UploadOptions{})
	pendingPart := putPart(t, s, "keep", "pending", pending.ID, 1, randomBytes(22, 10))
	s.Close()

	s2 := openTest(t, dir)
	if n := blobCount(t, dir); n != 3 {
		t.Fatalf("%d blobs after restart, want 3", n)
	}
	_, d, err := s2.OpenObject(ctx, "keep", "done")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if data, err := io.ReadAll(d.Verified()); err != nil || !bytes.Equal(data, append(append([]byte{}, p1...), p2...)) {
		t.Errorf("multipart object damaged by recovery: %v", err)
	}
	q := CompletePart{Number: 1, ETag: pendingPart.ETag}
	if _, err := s2.CompleteUpload(ctx, "keep", "pending", pending.ID, CompleteOptions{Parts: []CompletePart{q}, ObjectSize: -1}); err != nil {
		t.Errorf("pending upload damaged by recovery: %v", err)
	}
}

func TestCorruptPartIsDetected(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "rot")
	u, _ := s.CreateUpload(ctx, "rot", "k", UploadOptions{})
	p1 := randomBytes(17, MinPartSize)
	info := completeAll(t, s, "rot", "k", u.ID, putPart(t, s, "rot", "k", u.ID, 1, p1), putPart(t, s, "rot", "k", u.ID, 2, randomBytes(18, 500)))

	path := s.blobPath(info.parts[0].Blob)
	raw, _ := os.ReadFile(path)
	raw[42] ^= 1
	os.WriteFile(path, raw, 0o640)

	_, d, _ := s.OpenObject(ctx, "rot", "k")
	defer d.Close()
	got, err := io.ReadAll(d.Verified())
	if !errors.Is(err, ErrCorrupt) || len(got) >= len(p1) {
		t.Fatalf("corrupt part: read %d bytes, err %v", len(got), err)
	}
}

func TestListPartsAndUploads(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "lst")
	u, _ := s.CreateUpload(ctx, "lst", "k", UploadOptions{ChecksumAlgorithm: checksum.CRC32})
	for n := 1; n <= 5; n++ {
		putPart(t, s, "lst", "k", u.ID, n, randomBytes(uint64(n), 10))
	}
	_, parts, more, err := s.ListParts(ctx, "lst", "k", u.ID, 2, 2)
	if err != nil || len(parts) != 2 || parts[0].Number != 3 || !more || parts[0].Checksums[checksum.CRC32] == "" {
		t.Errorf("parts page: %+v more=%v err=%v", parts, more, err)
	}

	for _, k := range []string{"a/1", "a/2", "b"} {
		s.CreateUpload(ctx, "lst", k, UploadOptions{})
	}
	l, err := s.ListUploads(ctx, "lst", UploadListOptions{Delimiter: "/", MaxUploads: 100})
	if err != nil || len(l.CommonPrefixes) != 1 || l.CommonPrefixes[0] != "a/" || len(l.Uploads) != 2 {
		t.Errorf("uploads with delimiter: %+v, %v", l, err)
	}
	l, _ = s.ListUploads(ctx, "lst", UploadListOptions{MaxUploads: 2})
	if len(l.Uploads) != 2 || !l.IsTruncated || l.NextKeyMarker != "a/2" {
		t.Errorf("first page: %+v", l)
	}
	l, _ = s.ListUploads(ctx, "lst", UploadListOptions{MaxUploads: 2, KeyMarker: l.NextKeyMarker, UploadIDMarker: l.NextUploadIDMarker})
	if len(l.Uploads) != 2 || l.Uploads[0].Key != "b" || l.IsTruncated {
		t.Errorf("second page: %+v", l)
	}
}

func TestCopyPart(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "cpy")
	src := randomBytes(19, MinPartSize+50)
	put(t, s, "cpy", "src", string(src), PutOptions{})
	u, _ := s.CreateUpload(ctx, "cpy", "dst", UploadOptions{})
	a, err := s.CopyPart(ctx, "cpy", "src", "cpy", "dst", u.ID, 1, CopyPartOptions{Length: -1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CopyPart(ctx, "cpy", "src", "cpy", "dst", u.ID, 2, CopyPartOptions{Offset: 10, Length: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CopyPart(ctx, "cpy", "src", "cpy", "dst", u.ID, 3, CopyPartOptions{Offset: int64(len(src)), Length: 1}); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("range past the end: %v", err)
	}
	completeAll(t, s, "cpy", "dst", u.ID, a, b)
	_, d, _ := s.OpenObject(ctx, "cpy", "dst")
	defer d.Close()
	got, _ := io.ReadAll(d.Verified())
	if !bytes.Equal(got, append(append([]byte{}, src...), src[10:30]...)) {
		t.Error("copied parts mismatch")
	}
}
