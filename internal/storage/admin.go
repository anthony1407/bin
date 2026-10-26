package storage

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"time"
)

var (
	ErrNoSuchAccessKey = errors.New("no such access key")
	ErrAccessKeyExists = errors.New("access key already exists")
	ErrNoSession       = errors.New("no such session")
	ErrQuotaExceeded   = errors.New("bucket quota exceeded")
)

// --- access keys --------------------------------------------------------------

// AccessKey is a stored S3 credential. Its secret is encrypted by the caller.
type AccessKey struct {
	AccessKey string
	SecretEnc string
	Name      string
	Enabled   bool
	Internal  bool
	Created   time.Time
	Expires   time.Time // zero: never
	LastUsed  time.Time // zero: never
	Scope     string    // opaque to storage
}

func timeOrZero(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return fromNanos(n)
}

func nanosOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

const accessKeyColumns = `access_key, secret_enc, name, enabled, internal, created_at, expires_at, last_used_at, scope`

func scanAccessKey(scan func(...any) error) (AccessKey, error) {
	var k AccessKey
	var created, expires, used int64
	err := scan(&k.AccessKey, &k.SecretEnc, &k.Name, &k.Enabled, &k.Internal, &created, &expires, &used, &k.Scope)
	k.Created, k.Expires, k.LastUsed = fromNanos(created), timeOrZero(expires), timeOrZero(used)
	return k, err
}

// CreateAccessKey stores a new key.
func (s *Store) CreateAccessKey(ctx context.Context, k AccessKey) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM access_keys WHERE access_key = ?`, k.AccessKey).Scan(&one); err == nil {
			return ErrAccessKeyExists
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO access_keys (`+accessKeyColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			k.AccessKey, k.SecretEnc, k.Name, k.Enabled, k.Internal, s.nowNanos(), nanosOrZero(k.Expires), 0, k.Scope)
		return err
	})
}

// GetAccessKey returns one key.
func (s *Store) GetAccessKey(ctx context.Context, accessKey string) (AccessKey, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+accessKeyColumns+` FROM access_keys WHERE access_key = ?`, accessKey)
	k, err := scanAccessKey(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessKey{}, ErrNoSuchAccessKey
	}
	return k, err
}

// ListAccessKeys returns the user-managed keys, newest first.
func (s *Store) ListAccessKeys(ctx context.Context) ([]AccessKey, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+accessKeyColumns+` FROM access_keys
		WHERE internal = 0 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessKey
	for rows.Next() {
		k, err := scanAccessKey(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// UpdateAccessKey applies fn to a key and saves it. The access key itself
// and its creation time cannot change.
func (s *Store) UpdateAccessKey(ctx context.Context, accessKey string, fn func(*AccessKey) error) (AccessKey, error) {
	var k AccessKey
	err := s.tx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+accessKeyColumns+` FROM access_keys WHERE access_key = ?`, accessKey)
		var err error
		if k, err = scanAccessKey(row.Scan); errors.Is(err, sql.ErrNoRows) {
			return ErrNoSuchAccessKey
		} else if err != nil {
			return err
		}
		if err := fn(&k); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE access_keys SET secret_enc = ?, name = ?, enabled = ?, expires_at = ?, scope = ?
			WHERE access_key = ?`, k.SecretEnc, k.Name, k.Enabled, nanosOrZero(k.Expires), k.Scope, accessKey)
		return err
	})
	return k, err
}

// DeleteAccessKey removes a key.
func (s *Store) DeleteAccessKey(ctx context.Context, accessKey string) error {
	res, err := s.write.ExecContext(ctx, `DELETE FROM access_keys WHERE access_key = ?`, accessKey)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchAccessKey
	}
	return nil
}

// TouchAccessKey records that a key was used.
func (s *Store) TouchAccessKey(ctx context.Context, accessKey string, at time.Time) error {
	_, err := s.write.ExecContext(ctx, `UPDATE access_keys SET last_used_at = ? WHERE access_key = ?`, at.UnixNano(), accessKey)
	return err
}

// --- sessions -----------------------------------------------------------------

// Session is a console login.
type Session struct {
	TokenHash string
	CSRF      string
	User      string
	Created   time.Time
	Expires   time.Time
}

// CreateSession stores a session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO sessions (token_hash, csrf, user, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`, sess.TokenHash, sess.CSRF, sess.User, s.nowNanos(), sess.Expires.UnixNano())
	return err
}

// GetSession returns a live session; expired ones are ErrNoSession.
func (s *Store) GetSession(ctx context.Context, tokenHash string) (Session, error) {
	var sess Session
	var created, expires int64
	err := s.read.QueryRowContext(ctx, `SELECT token_hash, csrf, user, created_at, expires_at FROM sessions
		WHERE token_hash = ?`, tokenHash).Scan(&sess.TokenHash, &sess.CSRF, &sess.User, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && expires <= s.nowNanos()) {
		return Session{}, ErrNoSession
	}
	sess.Created, sess.Expires = fromNanos(created), fromNanos(expires)
	return sess, err
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.write.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessions ends every session (e.g. after the root secret changes).
func (s *Store) DeleteSessions(ctx context.Context) error {
	_, err := s.write.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

func (s *Store) deleteExpiredSessions(ctx context.Context) {
	s.write.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, s.nowNanos())
}

// --- settings -----------------------------------------------------------------

// Setting returns a stored runtime setting.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.read.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetSetting stores a runtime setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// --- bucket settings and usage -------------------------------------------------

// SetBucketQuota sets the most bytes a bucket may hold; 0 removes the limit.
func (s *Store) SetBucketQuota(ctx context.Context, bucket string, bytes int64) error {
	return s.setBucketColumn(ctx, bucket, "quota_bytes", bytes)
}

// SetBucketPolicy stores the bucket's access policy (JSON, interpreted by
// package auth); "" means private.
func (s *Store) SetBucketPolicy(ctx context.Context, bucket, policy string) error {
	return s.setBucketColumn(ctx, bucket, "policy", policy)
}

// SetBucketCORS stores the bucket's CORS rules (JSON, interpreted by package
// cors); "" removes them.
func (s *Store) SetBucketCORS(ctx context.Context, bucket, rules string) error {
	return s.setBucketColumn(ctx, bucket, "cors", rules)
}

// SetBucketTrashDays sets how many days deleted files stay in the bucket's
// trash; 0 keeps them until the trash is emptied.
func (s *Store) SetBucketTrashDays(ctx context.Context, bucket string, days int) error {
	return s.setBucketColumn(ctx, bucket, "trash_days", days)
}

func (s *Store) setBucketColumn(ctx context.Context, bucket, column string, value any) error {
	res, err := s.write.ExecContext(ctx, `UPDATE buckets SET `+column+` = ? WHERE name = ?`, value, bucket)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchBucket
	}
	return nil
}

// Usage is how much a bucket holds.
type Usage struct {
	Objects int64
	Bytes   int64
}

// BucketUsage returns the usage of every bucket, from the counters the
// objects triggers keep (migration 7).
func (s *Store) BucketUsage(ctx context.Context) (map[string]Usage, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT name, used_objects, used_bytes FROM buckets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Usage{}
	for rows.Next() {
		var name string
		var u Usage
		if err := rows.Scan(&name, &u.Objects, &u.Bytes); err != nil {
			return nil, err
		}
		out[name] = u
	}
	return out, rows.Err()
}

// checkQuota fails with ErrQuotaExceeded if adding grow bytes (net of what
// the write replaces) would take the bucket over its quota.
func checkQuota(ctx context.Context, q rowsQuerier, bucketID, grow int64) error {
	var quota, used int64
	if err := q.QueryRowContext(ctx, `SELECT quota_bytes, used_bytes FROM buckets WHERE id = ?`, bucketID).Scan(&quota, &used); err != nil {
		return err
	}
	if quota <= 0 || grow <= 0 {
		return nil
	}
	if used+grow > quota {
		return ErrQuotaExceeded
	}
	return nil
}

// --- explorer helpers -------------------------------------------------------------

// SearchObjects returns up to limit keys under prefix that contain term,
// ignoring ASCII case, in key order.
func (s *Store) SearchObjects(ctx context.Context, bucket, prefix, term string, limit int) ([]ObjectInfo, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT key, size, etag, modified_at FROM objects
		WHERE bucket_id = ? AND is_latest = 1 AND delete_marker = 0
		  AND key >= ? AND substr(key, 1, ?) = ? AND instr(lower(key), lower(?)) > 0
		ORDER BY key LIMIT ?`, bid, prefix, len(prefix), prefix, term, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectInfo
	for rows.Next() {
		o := ObjectInfo{Bucket: bucket}
		var modified int64
		if err := rows.Scan(&o.Key, &o.Size, &o.ETag, &modified); err != nil {
			return nil, err
		}
		o.ModTime = fromNanos(modified)
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeletePrefix deletes every object whose key starts with prefix, in batches,
// and returns how many were deleted. Each delete follows the bucket's
// versioning, as DeleteObject does: with versioning on, the objects get
// delete markers and their versions stay (PurgePrefix removes them).
func (s *Store) DeletePrefix(ctx context.Context, bucket, prefix string) (int, error) {
	total := 0
	for {
		page, err := s.ListObjects(ctx, bucket, ListOptions{Prefix: prefix, MaxKeys: 500})
		if err != nil {
			return total, err
		}
		if len(page.Objects) == 0 {
			return total, nil
		}
		var dropped []string
		err = s.tx(ctx, func(tx *sql.Tx) error {
			bid, err := bucketID(ctx, tx, bucket)
			if err != nil {
				return err
			}
			for _, o := range page.Objects {
				_, blobs, err := s.deleteInTx(ctx, tx, bid, o.Key, "")
				if err != nil {
					return err
				}
				dropped = append(dropped, blobs...)
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		s.collect(ctx, dropped...)
		total += len(page.Objects)
	}
}

// ScrubResult reports an integrity scan.
type ScrubResult struct {
	Objects int64 // versions read, older versions included
	Bytes   int64
	Corrupt []ObjectInfo // Bucket, Key and VersionID set
}

// Scrub reads every stored version in the given buckets (none for all
// buckets) end to end, verifying each SHA-256. progress, if set, is called
// after each version.
func (s *Store) Scrub(ctx context.Context, buckets []string, progress func(ScrubResult)) (ScrubResult, error) {
	var res ScrubResult
	if len(buckets) == 0 {
		all, err := s.ListBuckets(ctx)
		if err != nil {
			return res, err
		}
		for _, b := range all {
			buckets = append(buckets, b.Name)
		}
	}
	for _, b := range buckets {
		bid, err := bucketID(ctx, s.read, b)
		if err != nil {
			return res, err
		}
		var after int64
		for {
			page, last, err := s.storedVersions(ctx, bid, after, 500)
			if err != nil {
				return res, err
			}
			for _, o := range page {
				if err := ctx.Err(); err != nil {
					return res, err
				}
				_, data, err := s.OpenVersion(ctx, b, o.Key, o.VersionID)
				if errors.Is(err, ErrNoSuchKey) || errors.Is(err, ErrNoSuchVersion) {
					continue // deleted meanwhile
				}
				if err != nil {
					return res, err
				}
				n, err := io.Copy(io.Discard, data.Verified())
				data.Close()
				res.Objects++
				res.Bytes += n
				if err != nil {
					res.Corrupt = append(res.Corrupt, ObjectInfo{Bucket: b, Key: o.Key, VersionID: o.VersionID})
				}
				if progress != nil {
					progress(res)
				}
			}
			if len(page) < 500 {
				break
			}
			after = last
		}
	}
	return res, nil
}

// storedVersions returns up to limit versions holding data (no delete
// markers) with a row ID above after, and the last row ID returned.
func (s *Store) storedVersions(ctx context.Context, bucketID, after int64, limit int) ([]ObjectInfo, int64, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id, key, version_id FROM objects
		WHERE bucket_id = ? AND delete_marker = 0 AND id > ? ORDER BY id LIMIT ?`, bucketID, after, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ObjectInfo
	last := after
	for rows.Next() {
		var o ObjectInfo
		if err := rows.Scan(&last, &o.Key, &o.VersionID); err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, last, rows.Err()
}
