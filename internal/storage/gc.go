package storage

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// collect deletes a blob queued in gc_blobs and dequeues it. Failures (for
// example a file still open on Windows) leave it queued for the next sweep.
func (s *Store) collect(ctx context.Context, blob string) {
	if blob == "" {
		return
	}
	if err := s.removeBlob(blob); err != nil {
		return
	}
	s.write.ExecContext(ctx, `DELETE FROM gc_blobs WHERE blob = ?`, blob)
}

// collectQueued retries every queued blob.
func (s *Store) collectQueued(ctx context.Context) error {
	rows, err := s.read.QueryContext(ctx, `SELECT blob FROM gc_blobs`)
	if err != nil {
		return err
	}
	var queued []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		queued = append(queued, b)
	}
	rows.Close()
	for _, b := range queued {
		s.collect(ctx, b)
	}
	return rows.Err()
}

// recover runs at startup, before any request is served, and repairs what a
// crash can leave behind:
//   - files in .bin/tmp: uploads that never finished
//   - queued blobs whose deletion was interrupted
//   - blobs renamed into place whose metadata commit never happened
func (s *Store) recover(ctx context.Context, log *slog.Logger) error {
	tmp := filepath.Join(s.dir, ".bin", "tmp")
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		os.RemoveAll(filepath.Join(tmp, e.Name()))
	}
	if err := s.collectQueued(ctx); err != nil {
		return err
	}

	orphans := 0
	err = filepath.WalkDir(filepath.Join(s.dir, "blobs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		used, err := s.blobReferenced(ctx, d.Name())
		if err != nil || used {
			return err
		}
		orphans++
		return os.Remove(path)
	})
	if err != nil {
		return err
	}
	if len(entries) > 0 || orphans > 0 {
		log.Warn("recovered from an unclean shutdown", "temp_files", len(entries), "orphan_blobs", orphans)
	}
	return nil
}

// blobReferenced reports whether any metadata still points at a blob.
func (s *Store) blobReferenced(ctx context.Context, blob string) (bool, error) {
	var one int
	err := s.read.QueryRowContext(ctx, `SELECT 1 FROM objects WHERE blob = ? LIMIT 1`, blob).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// gcLoop retries queued deletions periodically until ctx ends.
func (s *Store) gcLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.collectQueued(ctx)
		}
	}
}
