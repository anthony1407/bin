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

	// 3: multipart uploads. A completed multipart object keeps its parts as
	// separate blobs (no concatenation), each with its own SHA-256; objects
	// stored by a single PUT keep using objects.blob. The never-used JSON
	// "parts" column is replaced by the object_parts table.
	`ALTER TABLE objects DROP COLUMN parts;
	ALTER TABLE objects ADD COLUMN parts_count INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE object_parts (
		object_id    INTEGER NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
		part_number  INTEGER NOT NULL,
		blob         TEXT    NOT NULL,
		start_offset INTEGER NOT NULL,             -- position of the part in the object
		size         INTEGER NOT NULL,
		etag         TEXT    NOT NULL,
		sha256       BLOB    NOT NULL,
		checksum     TEXT    NOT NULL DEFAULT '',  -- the upload's algorithm, base64
		PRIMARY KEY (object_id, part_number)
	) WITHOUT ROWID;
	CREATE INDEX object_parts_blob ON object_parts (blob);
	CREATE TABLE uploads (
		id                 TEXT    PRIMARY KEY,
		bucket_id          INTEGER NOT NULL REFERENCES buckets(id),
		key                TEXT    NOT NULL,
		created_at         INTEGER NOT NULL,
		content_type       TEXT    NOT NULL DEFAULT '',
		headers            TEXT    NOT NULL DEFAULT '{}',
		user_meta          TEXT    NOT NULL DEFAULT '{}',
		checksum_algorithm TEXT    NOT NULL DEFAULT '',
		checksum_type      TEXT    NOT NULL DEFAULT ''  -- FULL_OBJECT or COMPOSITE
	) WITHOUT ROWID;
	CREATE INDEX uploads_key ON uploads (bucket_id, key, created_at);
	CREATE TABLE upload_parts (
		upload_id   TEXT    NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
		part_number INTEGER NOT NULL,
		blob        TEXT    NOT NULL,
		size        INTEGER NOT NULL,
		etag        TEXT    NOT NULL,
		sha256      BLOB    NOT NULL,
		checksum    TEXT    NOT NULL DEFAULT '',
		modified_at INTEGER NOT NULL,
		PRIMARY KEY (upload_id, part_number)
	) WITHOUT ROWID;
	CREATE INDEX upload_parts_blob ON upload_parts (blob);`,

	// 4: administration. Access key secrets are stored encrypted with the
	// master key; sessions are stored by the hash of their token, never the
	// token itself.
	`CREATE TABLE access_keys (
		access_key   TEXT    PRIMARY KEY,
		secret_enc   TEXT    NOT NULL,
		name         TEXT    NOT NULL DEFAULT '',
		enabled      INTEGER NOT NULL DEFAULT 1,
		internal     INTEGER NOT NULL DEFAULT 0,  -- managed by bin itself, hidden from listings
		created_at   INTEGER NOT NULL,
		expires_at   INTEGER NOT NULL DEFAULT 0,  -- 0 = never
		last_used_at INTEGER NOT NULL DEFAULT 0,
		scope        TEXT    NOT NULL DEFAULT ''  -- JSON, interpreted by package auth; '' = full access
	) WITHOUT ROWID;
	CREATE TABLE sessions (
		token_hash TEXT    PRIMARY KEY,
		csrf       TEXT    NOT NULL,
		user       TEXT    NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	) WITHOUT ROWID;
	CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
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
