package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrNoSuchExportTarget = errors.New("no such export target")

// ExportTarget is a saved export: a remote S3 server and which buckets of
// bin to copy there. Running it again sends what is new or changed.
type ExportTarget struct {
	ID         string
	Name       string
	Endpoint   string
	AccessKey  string
	SecretEnc  string // encrypted by the caller
	Region     string
	Buckets    []string
	AllBuckets bool // every local bucket, as they are at each run
	Settings   bool // versioning, tags and anonymous access too
	Created    time.Time
	LastRun    time.Time // zero: never
}

const exportTargetColumns = `id, name, endpoint, access_key, secret_enc, region, buckets, all_buckets, settings, created_at, last_run_at`

func scanExportTarget(scan func(...any) error) (ExportTarget, error) {
	var t ExportTarget
	var buckets string
	var created, lastRun int64
	if err := scan(&t.ID, &t.Name, &t.Endpoint, &t.AccessKey, &t.SecretEnc, &t.Region, &buckets,
		&t.AllBuckets, &t.Settings, &created, &lastRun); err != nil {
		return t, err
	}
	t.Created, t.LastRun = fromNanos(created), timeOrZero(lastRun)
	return t, json.Unmarshal([]byte(buckets), &t.Buckets)
}

// CreateExportTarget stores a new saved export.
func (s *Store) CreateExportTarget(ctx context.Context, t ExportTarget) error {
	_, err := s.exec(ctx, `INSERT INTO export_targets (`+exportTargetColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		t.ID, t.Name, t.Endpoint, t.AccessKey, t.SecretEnc, t.Region, encodeBuckets(t.Buckets),
		t.AllBuckets, t.Settings, s.nowNanos())
	return err
}

// UpdateExportTarget replaces a saved export's settings, keeping its history.
func (s *Store) UpdateExportTarget(ctx context.Context, t ExportTarget) error {
	res, err := s.exec(ctx, `UPDATE export_targets SET name = ?, endpoint = ?, access_key = ?,
		secret_enc = ?, region = ?, buckets = ?, all_buckets = ?, settings = ? WHERE id = ?`,
		t.Name, t.Endpoint, t.AccessKey, t.SecretEnc, t.Region, encodeBuckets(t.Buckets), t.AllBuckets, t.Settings, t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchExportTarget
	}
	return nil
}

// ExportTarget returns one saved export.
func (s *Store) ExportTarget(ctx context.Context, id string) (ExportTarget, error) {
	t, err := scanExportTarget(s.read.QueryRowContext(ctx, `SELECT `+exportTargetColumns+` FROM export_targets WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ExportTarget{}, ErrNoSuchExportTarget
	}
	return t, err
}

// ListExportTargets returns the saved exports, by name.
func (s *Store) ListExportTargets(ctx context.Context) ([]ExportTarget, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+exportTargetColumns+` FROM export_targets ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportTarget
	for rows.Next() {
		t, err := scanExportTarget(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteExportTarget removes a saved export; what it sent stays on the remote.
func (s *Store) DeleteExportTarget(ctx context.Context, id string) error {
	res, err := s.exec(ctx, `DELETE FROM export_targets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchExportTarget
	}
	return nil
}

// MarkExportTargetRun records that a saved export was just run.
func (s *Store) MarkExportTargetRun(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `UPDATE export_targets SET last_run_at = ? WHERE id = ?`, s.nowNanos(), id)
	return err
}
