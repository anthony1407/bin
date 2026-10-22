package audit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bin/internal/storage"
)

func TestRecorder(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec := New(store, log)
	rec.Record(storage.AuditEntry{Source: S3, Actor: "AK1", Action: "PutObject", Bucket: "photos", Resource: "a.jpg", Status: 200})
	rec.Record(storage.AuditEntry{Source: Console, Actor: "root", Action: "DeleteBucket", Bucket: "old", Status: 409, Error: "bucket_not_empty"})
	rec.Record(storage.AuditEntry{Source: S3, Actor: Anonymous, Action: "GetObject", Bucket: "photos", Status: 403, Error: "AccessDenied"})
	rec.Flush()

	all, err := store.ListAudit(ctx, storage.AuditFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %d entries, %v", len(all), err)
	}
	if all[0].Actor != Anonymous || all[2].Action != "PutObject" {
		t.Errorf("not newest first: %+v", all)
	}
	for _, tc := range []struct {
		f    storage.AuditFilter
		want int
	}{
		{storage.AuditFilter{Source: Console}, 1},
		{storage.AuditFilter{Failed: true}, 2},
		{storage.AuditFilter{Bucket: "photos"}, 2},
		{storage.AuditFilter{Search: "a.JPG"}, 1},
		{storage.AuditFilter{Before: all[0].ID}, 2},
		{storage.AuditFilter{Limit: 1}, 1},
	} {
		if got, _ := store.ListAudit(ctx, tc.f); len(got) != tc.want {
			t.Errorf("%+v: %d entries, want %d", tc.f, len(got), tc.want)
		}
	}

	// Entries older than the retention are pruned.
	store.AddAudit(ctx, storage.AuditEntry{At: time.Now().AddDate(0, 0, -40), Source: S3, Actor: "AK1", Action: "PutObject", Status: 200})
	store.SetSetting(ctx, RetentionSetting, "30")
	rec.prune()
	if got, _ := store.ListAudit(ctx, storage.AuditFilter{}); len(got) != 3 {
		t.Errorf("after pruning: %d entries, want 3", len(got))
	}
	rec.Close()
	rec.Record(storage.AuditEntry{}) // after Close: dropped, never blocks
	var none *Recorder
	none.Record(storage.AuditEntry{})
	none.Flush()
	none.Close()
}
