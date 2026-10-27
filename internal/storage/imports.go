package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrNoSuchImportSource = errors.New("no such import source")

// ImportSource is a saved import: a remote S3 server, the buckets to copy
// from it, and what else comes along. Running it again brings what is new.
type ImportSource struct {
	ID        string
	Name      string
	Endpoint  string
	AccessKey string
	SecretEnc string // encrypted by the caller
	Region    string
	Buckets   []string
	Settings  bool // bucket settings
	IAM       bool // users, groups, policies and access keys
	Created   time.Time
	LastRun   time.Time // zero: never
}

const importSourceColumns = `id, name, endpoint, access_key, secret_enc, region, buckets, settings, iam, created_at, last_run_at`

func scanImportSource(scan func(...any) error) (ImportSource, error) {
	var s ImportSource
	var buckets string
	var created, lastRun int64
	if err := scan(&s.ID, &s.Name, &s.Endpoint, &s.AccessKey, &s.SecretEnc, &s.Region, &buckets,
		&s.Settings, &s.IAM, &created, &lastRun); err != nil {
		return s, err
	}
	s.Created, s.LastRun = fromNanos(created), timeOrZero(lastRun)
	return s, json.Unmarshal([]byte(buckets), &s.Buckets)
}

func encodeBuckets(b []string) string {
	if b == nil {
		b = []string{}
	}
	out, _ := json.Marshal(b)
	return string(out)
}

// CreateImportSource stores a new saved import.
func (s *Store) CreateImportSource(ctx context.Context, src ImportSource) error {
	_, err := s.exec(ctx, `INSERT INTO import_sources (`+importSourceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		src.ID, src.Name, src.Endpoint, src.AccessKey, src.SecretEnc, src.Region, encodeBuckets(src.Buckets),
		src.Settings, src.IAM, s.nowNanos())
	return err
}

// UpdateImportSource replaces a saved import's settings, keeping its history.
func (s *Store) UpdateImportSource(ctx context.Context, src ImportSource) error {
	res, err := s.exec(ctx, `UPDATE import_sources SET name = ?, endpoint = ?, access_key = ?,
		secret_enc = ?, region = ?, buckets = ?, settings = ?, iam = ? WHERE id = ?`,
		src.Name, src.Endpoint, src.AccessKey, src.SecretEnc, src.Region, encodeBuckets(src.Buckets), src.Settings, src.IAM, src.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchImportSource
	}
	return nil
}

// ImportSource returns one saved import.
func (s *Store) ImportSource(ctx context.Context, id string) (ImportSource, error) {
	src, err := scanImportSource(s.read.QueryRowContext(ctx, `SELECT `+importSourceColumns+` FROM import_sources WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportSource{}, ErrNoSuchImportSource
	}
	return src, err
}

// ListImportSources returns the saved imports, by name.
func (s *Store) ListImportSources(ctx context.Context) ([]ImportSource, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+importSourceColumns+` FROM import_sources ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportSource
	for rows.Next() {
		src, err := scanImportSource(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// DeleteImportSource removes a saved import; what it imported stays.
func (s *Store) DeleteImportSource(ctx context.Context, id string) error {
	res, err := s.exec(ctx, `DELETE FROM import_sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchImportSource
	}
	return nil
}

// MarkImportSourceRun records that a saved import was just run.
func (s *Store) MarkImportSourceRun(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `UPDATE import_sources SET last_run_at = ? WHERE id = ?`, s.nowNanos(), id)
	return err
}
