package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The usage counters kept by triggers must always equal a recount of the
// objects table, whatever path changed it.
func TestUsageCountersMatchRecount(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	for _, b := range []string{"one", "two"} {
		if err := s.CreateBucket(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string) {
		t.Helper()
		usage, err := s.BucketUsage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for name, got := range usage {
			var want Usage
			s.read.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(size), 0) FROM objects o
				JOIN buckets b ON b.id = o.bucket_id WHERE b.name = ? AND o.delete_marker = 0`, name).Scan(&want.Objects, &want.Bytes)
			if got != want {
				t.Errorf("%s: %s usage %+v, recount %+v", step, name, got, want)
			}
		}
	}
	put := func(bucket, key string, size int) {
		t.Helper()
		if _, err := s.PutObject(ctx, bucket, key, strings.NewReader(strings.Repeat("x", size)), int64(size), PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	put("one", "a", 10)
	put("one", "b", 20)
	put("two", "c", 5)
	check("put")
	put("one", "a", 3) // replace: the old size goes away
	check("replace")
	if _, err := s.CopyObject(ctx, "one", "b", "two", "b-copy", CopyOptions{}); err != nil {
		t.Fatal(err)
	}
	check("copy")
	if err := s.DeleteObject(ctx, "one", "a"); err != nil {
		t.Fatal(err)
	}
	check("delete")

	u, err := s.CreateUpload(ctx, "two", "big", UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	part := bytes.Repeat([]byte("y"), 5<<20)
	p1, err := s.PutPart(ctx, "two", "big", u.ID, 1, bytes.NewReader(part), int64(len(part)), PartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.PutPart(ctx, "two", "big", u.ID, 2, strings.NewReader("tail"), 4, PartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	check("parts uploaded") // pending parts are not objects yet
	if _, err := s.CompleteUpload(ctx, "two", "big", u.ID, CompleteOptions{ObjectSize: -1,
		Parts: []CompletePart{{Number: 1, ETag: p1.ETag}, {Number: 2, ETag: p2.ETag}}}); err != nil {
		t.Fatal(err)
	}
	check("multipart")
	if _, err := s.DeletePrefix(ctx, "two", ""); err != nil {
		t.Fatal(err)
	}
	check("delete prefix")
	if err := s.DeleteBucket(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	check("delete bucket")
}

func TestQuotaUsesCounters(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	s.CreateBucket(ctx, "quota")
	if err := s.SetBucketQuota(ctx, "quota", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutObject(ctx, "quota", "a", strings.NewReader("12345678"), 8, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutObject(ctx, "quota", "b", strings.NewReader("123"), 3, PutOptions{}); err != ErrQuotaExceeded {
		t.Errorf("over quota: %v", err)
	}
	// Replacing "a" frees its 8 bytes first.
	if _, err := s.PutObject(ctx, "quota", "a", strings.NewReader("1234567890"), 10, PutOptions{}); err != nil {
		t.Errorf("replace within quota: %v", err)
	}
}
