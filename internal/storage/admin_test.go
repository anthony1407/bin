package storage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAccessKeys(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := s.CreateAccessKey(ctx, AccessKey{AccessKey: "AK1", SecretEnc: "enc", Name: "laptop", Enabled: true, Expires: exp, Scope: `{"readOnly":true}`}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAccessKey(ctx, AccessKey{AccessKey: "AK1", SecretEnc: "x"}); !errors.Is(err, ErrAccessKeyExists) {
		t.Errorf("duplicate: %v", err)
	}
	s.CreateAccessKey(ctx, AccessKey{AccessKey: "SHARE", SecretEnc: "x", Internal: true, Enabled: true})

	k, err := s.GetAccessKey(ctx, "AK1")
	if err != nil || k.Name != "laptop" || !k.Expires.Equal(exp) || !k.LastUsed.IsZero() || k.Scope != `{"readOnly":true}` {
		t.Errorf("get: %+v, %v", k, err)
	}
	list, _ := s.ListAccessKeys(ctx)
	if len(list) != 1 || list[0].AccessKey != "AK1" {
		t.Errorf("internal keys must be hidden: %+v", list)
	}
	k, err = s.UpdateAccessKey(ctx, "AK1", func(k *AccessKey) error { k.Enabled = false; k.Name = "old laptop"; return nil })
	if err != nil || k.Enabled || k.Name != "old laptop" {
		t.Errorf("update: %+v, %v", k, err)
	}
	s.TouchAccessKey(ctx, "AK1", exp)
	if k, _ := s.GetAccessKey(ctx, "AK1"); !k.LastUsed.Equal(exp) || k.Enabled {
		t.Errorf("after touch: %+v", k)
	}
	if err := s.DeleteAccessKey(ctx, "AK1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccessKey(ctx, "AK1"); !errors.Is(err, ErrNoSuchAccessKey) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.DeleteAccessKey(ctx, "AK1"); !errors.Is(err, ErrNoSuchAccessKey) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestSessionsAndSettings(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	now := time.Now()
	s.CreateSession(ctx, Session{TokenHash: "live", CSRF: "c1", User: "root", Expires: now.Add(time.Hour)})
	s.CreateSession(ctx, Session{TokenHash: "old", CSRF: "c2", User: "root", Expires: now.Add(-time.Second)})
	if sess, err := s.GetSession(ctx, "live"); err != nil || sess.CSRF != "c1" {
		t.Errorf("live: %+v, %v", sess, err)
	}
	if _, err := s.GetSession(ctx, "old"); !errors.Is(err, ErrNoSession) {
		t.Errorf("expired session accepted: %v", err)
	}
	s.DeleteSession(ctx, "live")
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, ErrNoSession) {
		t.Errorf("deleted session accepted: %v", err)
	}

	if _, ok, _ := s.Setting(ctx, "share_max_expiry"); ok {
		t.Error("unset setting found")
	}
	s.SetSetting(ctx, "share_max_expiry", "3600")
	s.SetSetting(ctx, "share_max_expiry", "7200")
	if v, ok, _ := s.Setting(ctx, "share_max_expiry"); !ok || v != "7200" {
		t.Errorf("setting = %q %v", v, ok)
	}
}

func TestQuota(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "quota")
	if err := s.SetBucketQuota(ctx, "quota", 100); err != nil {
		t.Fatal(err)
	}
	put(t, s, "quota", "a", strings.Repeat("x", 60), PutOptions{})
	if _, err := s.PutObject(ctx, "quota", "b", strings.NewReader(strings.Repeat("y", 50)), 50, PutOptions{}); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("over quota: %v", err)
	}
	// Replacing an object only counts the difference.
	put(t, s, "quota", "a", strings.Repeat("z", 90), PutOptions{})
	if b, _ := s.GetBucket(ctx, "quota"); b.QuotaBytes != 100 {
		t.Errorf("quota = %d", b.QuotaBytes)
	}
	// Multipart completion is checked too.
	u, _ := s.CreateUpload(ctx, "quota", "big", UploadOptions{})
	p := putPart(t, s, "quota", "big", u.ID, 1, randomBytes(30, 20))
	if _, err := s.CompleteUpload(ctx, "quota", "big", u.ID, CompleteOptions{Parts: []CompletePart{{Number: 1, ETag: p.ETag}}, ObjectSize: -1}); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("multipart over quota: %v", err)
	}
	s.SetBucketQuota(ctx, "quota", 0)
	put(t, s, "quota", "b", strings.Repeat("y", 500), PutOptions{})
	if err := s.SetBucketQuota(ctx, "missing", 1); !errors.Is(err, ErrNoSuchBucket) {
		t.Errorf("missing bucket: %v", err)
	}
}

func TestUsageSearchAndDeletePrefix(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "photos")
	mustCreate(t, s, "empty")
	for _, k := range []string{"2024/Beach.jpg", "2024/city.png", "2025/beach-2.JPG", "notes.txt"} {
		put(t, s, "photos", k, "1234", PutOptions{})
	}
	u, err := s.BucketUsage(ctx)
	if err != nil || u["photos"] != (Usage{Objects: 4, Bytes: 16}) || u["empty"] != (Usage{}) {
		t.Errorf("usage: %+v, %v", u, err)
	}

	found, _ := s.SearchObjects(ctx, "photos", "", "beach", 10)
	if len(found) != 2 || found[0].Key != "2024/Beach.jpg" || found[1].Key != "2025/beach-2.JPG" {
		t.Errorf("search: %+v", found)
	}
	found, _ = s.SearchObjects(ctx, "photos", "2025/", "beach", 10)
	if len(found) != 1 {
		t.Errorf("search in prefix: %+v", found)
	}

	n, err := s.DeletePrefix(ctx, "photos", "2024/")
	if err != nil || n != 2 {
		t.Errorf("delete prefix: %d, %v", n, err)
	}
	n, _ = s.DeletePrefix(ctx, "photos", "")
	if n != 2 {
		t.Errorf("empty bucket: %d", n)
	}
	if err := s.DeleteBucket(ctx, "photos"); err != nil {
		t.Errorf("bucket should be empty: %v", err)
	}
}

func TestScrub(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "aaa")
	mustCreate(t, s, "bbb")
	mustCreate(t, s, "ccc")
	put(t, s, "aaa", "good", "fine data", PutOptions{})
	bad := put(t, s, "bbb", "bad", strings.Repeat("rot", 1000), PutOptions{})
	put(t, s, "ccc", "also-good", "more data", PutOptions{})
	raw, _ := os.ReadFile(s.blobPath(bad.blob))
	raw[10] ^= 1
	os.WriteFile(s.blobPath(bad.blob), raw, 0o640)

	calls := 0
	res, err := s.Scrub(ctx, nil, func(ScrubResult) { calls++ })
	if err != nil || res.Objects != 3 || calls != 3 || len(res.Corrupt) != 1 || res.Corrupt[0].Key != "bad" || res.Corrupt[0].Bucket != "bbb" {
		t.Errorf("scrub: %+v, %v", res, err)
	}
	res, _ = s.Scrub(ctx, []string{"aaa"}, nil)
	if res.Objects != 1 || len(res.Corrupt) != 0 {
		t.Errorf("scrub one bucket: %+v", res)
	}
	res, _ = s.Scrub(ctx, []string{"aaa", "bbb"}, nil)
	if res.Objects != 2 || len(res.Corrupt) != 1 {
		t.Errorf("scrub two buckets: %+v", res)
	}
}
