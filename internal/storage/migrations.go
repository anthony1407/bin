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
