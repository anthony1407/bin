package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// AuditEntry is one recorded action.
type AuditEntry struct {
	ID        int64
	At        time.Time
	Source    string // "console" or "s3"
	Actor     string
	Action    string
	Bucket    string
	Resource  string
	Status    int
	Error     string
	Remote    string
	RequestID string
	Size      int64 // bytes the upload or download moved; 0 when not a transfer
}

// AuditFilter selects audit entries. Zero fields match everything.
type AuditFilter struct {
	Source string
	Bucket string
	Actor  string // exactly this actor (one user's activity)
	Failed bool   // only responses with status >= 400
	Search string // substring of actor, action, bucket or resource, ignoring ASCII case
	Before int64  // entries with a smaller ID (pagination); 0: the newest
	Limit  int
}

const auditColumns = `id, at, source, actor, action, bucket, resource, status, error, remote, request_id, size`

// AddAudit stores entries in one transaction.
func (s *Store) AddAudit(ctx context.Context, entries ...AuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO audit_log (at, source, actor, action, bucket, resource, status, error, remote, request_id, size)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range entries {
			at := e.At
			if at.IsZero() {
				at = s.now()
			}
			if _, err := stmt.ExecContext(ctx, at.UnixNano(), e.Source, e.Actor, e.Action, e.Bucket, e.Resource,
				e.Status, e.Error, e.Remote, e.RequestID, e.Size); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListAudit returns matching entries, newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	var where []string
	var args []any
	if f.Source != "" {
		where, args = append(where, "source = ?"), append(args, f.Source)
	}
	if f.Bucket != "" {
		where, args = append(where, "bucket = ?"), append(args, f.Bucket)
	}
	if f.Actor != "" {
		where, args = append(where, "actor = ?"), append(args, f.Actor)
	}
	if f.Failed {
		where = append(where, "status >= 400")
	}
	if f.Search != "" {
		where = append(where, "(instr(lower(actor), lower(?)) > 0 OR instr(lower(action), lower(?)) > 0"+
			" OR instr(lower(bucket), lower(?)) > 0 OR instr(lower(resource), lower(?)) > 0)")
		args = append(args, f.Search, f.Search, f.Search, f.Search)
	}
	if f.Before > 0 {
		where, args = append(where, "id < ?"), append(args, f.Before)
	}
	q := `SELECT ` + auditColumns + ` FROM audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Source, &e.Actor, &e.Action, &e.Bucket, &e.Resource,
			&e.Status, &e.Error, &e.Remote, &e.RequestID, &e.Size); err != nil {
			return nil, err
		}
		e.At = fromNanos(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ActorTotals sums one actor's successful uploads and downloads since a time,
// as recorded in the log (only what the retention kept).
type ActorTotals struct {
	Uploads, Downloads         int64
	UploadBytes, DownloadBytes int64
	LastActivity               time.Time // zero: nothing recorded
}

// AuditTotals returns ActorTotals for actor. Uploads are the audited writes
// that store an object; downloads, the reads recorded when that is on.
func (s *Store) AuditTotals(ctx context.Context, actor string) (ActorTotals, error) {
	var t ActorTotals
	var last int64
	err := s.read.QueryRowContext(ctx, `SELECT
		coalesce(sum(CASE WHEN action IN ('PutObject','CopyObject','CompleteMultipartUpload','DropzoneUpload') THEN 1 END), 0),
		coalesce(sum(CASE WHEN action IN ('PutObject','CopyObject','CompleteMultipartUpload','DropzoneUpload') THEN size END), 0),
		coalesce(sum(CASE WHEN action IN ('GetObject','Download','DownloadZip') THEN 1 END), 0),
		coalesce(sum(CASE WHEN action IN ('GetObject','Download','DownloadZip') THEN size END), 0),
		coalesce(max(at), 0)
		FROM audit_log WHERE actor = ? AND status < 400`, actor).Scan(&t.Uploads, &t.UploadBytes, &t.Downloads, &t.DownloadBytes, &last)
	if last > 0 {
		t.LastActivity = fromNanos(last)
	}
	return t, err
}

// PruneAudit deletes the entries recorded before cutoff and returns how many.
func (s *Store) PruneAudit(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.exec(ctx, `DELETE FROM audit_log WHERE at < ?`, cutoff.UnixNano())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
