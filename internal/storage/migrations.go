package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order, each in its own transaction. Never edit a
// migration that has shipped; append a new one instead.
var migrations = []string{
	// 1: buckets.
	`CREATE TABLE buckets (
		id          INTEGER PRIMARY KEY,
		name        TEXT    NOT NULL UNIQUE,
		created_at  INTEGER NOT NULL,              -- unix nanoseconds
		versioning  TEXT    NOT NULL DEFAULT '',   -- '', 'Enabled', 'Suspended' (v1.1)
		object_lock INTEGER NOT NULL DEFAULT 0,    -- reserved for v1.1
		quota_bytes INTEGER NOT NULL DEFAULT 0,    -- 0 = unlimited
		policy      TEXT    NOT NULL DEFAULT ''    -- bucket policy JSON; '' = private
	);
	CREATE TABLE bucket_tags (
		bucket_id INTEGER NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
		key       TEXT    NOT NULL,
		value     TEXT    NOT NULL,
		PRIMARY KEY (bucket_id, key)
	) WITHOUT ROWID;`,

	// 2: objects, versioning-ready. Unversioned buckets only ever hold
	// version_id 'null'; with versioning (v1.1) older versions keep
	// is_latest = 0 and deletes become delete markers.
	`CREATE TABLE objects (
		id            INTEGER PRIMARY KEY,
		bucket_id     INTEGER NOT NULL REFERENCES buckets(id),
		key           TEXT    NOT NULL,
		version_id    TEXT    NOT NULL DEFAULT 'null',
		is_latest     INTEGER NOT NULL DEFAULT 1,
		delete_marker INTEGER NOT NULL DEFAULT 0,
		blob          TEXT,                         -- NULL for delete markers
		size          INTEGER NOT NULL DEFAULT 0,
		etag          TEXT    NOT NULL DEFAULT '',  -- unquoted
		sha256        BLOB,                         -- integrity hash of the blob
		content_type  TEXT    NOT NULL DEFAULT '',
		headers       TEXT    NOT NULL DEFAULT '{}', -- Cache-Control, Content-Disposition, ...
		user_meta     TEXT    NOT NULL DEFAULT '{}', -- x-amz-meta-* without the prefix
		checksums     TEXT    NOT NULL DEFAULT '{}', -- algorithm -> base64
		checksum_type TEXT    NOT NULL DEFAULT '',
		parts         TEXT    NOT NULL DEFAULT '',   -- multipart layout (JSON), '' for single PUTs
		modified_at   INTEGER NOT NULL,
		UNIQUE (bucket_id, key, version_id)
	);
	CREATE UNIQUE INDEX objects_latest ON objects (bucket_id, key) WHERE is_latest = 1;
	CREATE INDEX objects_blob ON objects (blob);
	-- Blobs no longer referenced, queued in the same transaction that
	-- dropped them and deleted from disk after commit.
	CREATE TABLE gc_blobs (
		blob      TEXT    PRIMARY KEY,
		queued_at INTEGER NOT NULL
	) WITHOUT ROWID;`,
}

// migrate brings the schema to the latest version, tracked in user_version.
func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
