package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Bucket versioning states, as S3 names them. A bucket that never had
// versioning turned on reports "" and can never return to it.
const (
	VersioningEnabled   = "Enabled"
	VersioningSuspended = "Suspended"
)

// NullVersion is the version ID of objects written while a bucket has no
// versioning, or has it suspended. Such a write replaces the previous null
// version instead of adding one.
const NullVersion = "null"

var (
	ErrNoSuchVersion     = errors.New("no such version")
	ErrInvalidVersion    = errors.New("invalid version id")
	ErrInvalidVersioning = errors.New("versioning status must be Enabled or Suspended")
	// ErrDeleteMarker means the requested version is a delete marker. The
	// accompanying ObjectInfo describes the marker.
	ErrDeleteMarker = errors.New("the version is a delete marker")
)

// newVersionID returns a random UUID (version 4), the form MinIO uses too.
func newVersionID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ValidVersionID reports whether v can name a version: "null" or a UUID.
func ValidVersionID(v string) bool {
	if v == NullVersion {
		return true
	}
	if len(v) != 36 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// SetBucketVersioning turns versioning on ("Enabled") or pauses it
// ("Suspended"). Existing versions are kept either way.
func (s *Store) SetBucketVersioning(ctx context.Context, bucket, status string) error {
	if status != VersioningEnabled && status != VersioningSuspended {
		return ErrInvalidVersioning
	}
	return s.setBucketColumn(ctx, bucket, "versioning", status)
}

func bucketVersioning(ctx context.Context, q querier, bucketID int64) (string, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT versioning FROM buckets WHERE id = ?`, bucketID).Scan(&v)
	return v, err
}

// beginWrite evaluates a conditional write against the current object and
// makes room for the new version: with versioning enabled the new object gets
// a fresh version ID; otherwise it becomes the null version, replacing the
// previous one wherever it sits in the history. It returns the version ID and
// the blobs queued for deletion.
func (s *Store) beginWrite(ctx context.Context, tx *sql.Tx, bid int64, key, ifMatch, ifNoneMatch string) (string, []string, error) {
	cur, err := latest(ctx, tx, bid, key)
	if err != nil && !errors.Is(err, ErrNoSuchKey) {
		return "", nil, err
	}
	exists := err == nil
	if ifNoneMatch != "" && exists && (ifNoneMatch == "*" || etagMatches(ifNoneMatch, cur.ETag)) {
		return "", nil, ErrPrecondition
	}
	if ifMatch != "" {
		if !exists {
			return "", nil, ErrNoSuchKey
		}
		if !etagMatches(ifMatch, cur.ETag) {
			return "", nil, ErrPrecondition
		}
	}
	versioning, err := bucketVersioning(ctx, tx, bid)
	if err != nil {
		return "", nil, err
	}
	if versioning == VersioningEnabled {
		return newVersionID(), nil, nil
	}
	dropped, err := dropVersion(ctx, tx, bid, key, NullVersion, s.nowNanos())
	if errors.Is(err, ErrNoSuchKey) {
		err = nil
	}
	return NullVersion, dropped, err
}

// insertLatest stores o as the newest version of its key.
func insertLatest(ctx context.Context, tx *sql.Tx, bucketID int64, o ObjectInfo) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE objects SET is_latest = 0 WHERE bucket_id = ? AND key = ? AND is_latest = 1`,
		bucketID, o.Key); err != nil {
		return 0, err
	}
	return insertObject(ctx, tx, bucketID, o)
}

// promoteLatest makes the newest remaining version of a key its latest one,
// after the previous latest version was removed.
func promoteLatest(ctx context.Context, tx *sql.Tx, bucketID int64, key string) error {
	_, err := tx.ExecContext(ctx, `UPDATE objects SET is_latest = 1
		WHERE id = (SELECT max(id) FROM objects WHERE bucket_id = ?1 AND key = ?2)`, bucketID, key)
	return err
}

// StatVersion returns the metadata of one version of bucket/key, or of the
// latest version when versionID is "". A delete marker is returned with
// ErrDeleteMarker.
func (s *Store) StatVersion(ctx context.Context, bucket, key, versionID string) (ObjectInfo, error) {
	if versionID != "" && !ValidVersionID(versionID) {
		return ObjectInfo{}, ErrInvalidVersion
	}
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := loadVersion(ctx, s.read, bid, key, versionID)
	if err != nil {
		return ObjectInfo{}, err
	}
	info.Bucket = bucket
	if info.DeleteMarker {
		return info, ErrDeleteMarker
	}
	return info, nil
}

// DeleteResult describes what a delete did.
type DeleteResult struct {
	// VersionID is the version removed, or the delete marker created; "" when
	// the bucket has never been versioned.
	VersionID string
	// DeleteMarker is set when a delete marker was created, or when the
	// version removed was one.
	DeleteMarker bool
}

// DeleteVersion deletes bucket/key. With a versionID, that version is removed
// for good (removing a missing version succeeds, as in S3). Without one, the
// result depends on the bucket's versioning: no versioning removes the
// object; enabled adds a delete marker and keeps every version; suspended
// replaces the null version with a delete marker.
func (s *Store) DeleteVersion(ctx context.Context, bucket, key, versionID string) (DeleteResult, error) {
	if versionID != "" && !ValidVersionID(versionID) {
		return DeleteResult{}, ErrInvalidVersion
	}
	var res DeleteResult
	var dropped []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		bid, err := bucketID(ctx, tx, bucket)
		if err != nil {
			return err
		}
		res, dropped, err = s.deleteInTx(ctx, tx, bid, key, versionID)
		return err
	})
	if err != nil {
		return DeleteResult{}, err
	}
	s.collect(ctx, dropped...)
	return res, nil
}

func (s *Store) deleteInTx(ctx context.Context, tx *sql.Tx, bid int64, key, versionID string) (DeleteResult, []string, error) {
	now := s.nowNanos()
	if versionID != "" {
		var latest, marker bool
		err := tx.QueryRowContext(ctx, `SELECT is_latest, delete_marker FROM objects
			WHERE bucket_id = ? AND key = ? AND version_id = ?`, bid, key, versionID).Scan(&latest, &marker)
		if errors.Is(err, sql.ErrNoRows) {
			return DeleteResult{VersionID: versionID}, nil, nil
		}
		if err != nil {
			return DeleteResult{}, nil, err
		}
		dropped, err := dropVersion(ctx, tx, bid, key, versionID, now)
		if err != nil {
			return DeleteResult{}, nil, err
		}
		if latest {
			if err := promoteLatest(ctx, tx, bid, key); err != nil {
				return DeleteResult{}, nil, err
			}
		}
		return DeleteResult{VersionID: versionID, DeleteMarker: marker}, dropped, nil
	}

	versioning, err := bucketVersioning(ctx, tx, bid)
	if err != nil {
		return DeleteResult{}, nil, err
	}
	var dropped []string
	markerID := newVersionID()
	if versioning != VersioningEnabled {
		dropped, err = dropVersion(ctx, tx, bid, key, NullVersion, now)
		if err != nil && !errors.Is(err, ErrNoSuchKey) {
			return DeleteResult{}, nil, err
		}
		if versioning == "" {
			return DeleteResult{}, dropped, nil
		}
		markerID = NullVersion
	}
	marker := ObjectInfo{Key: key, VersionID: markerID, DeleteMarker: true, ModTime: fromNanos(now)}
	if _, err := insertLatest(ctx, tx, bid, marker); err != nil {
		return DeleteResult{}, nil, err
	}
	return DeleteResult{VersionID: markerID, DeleteMarker: true}, dropped, nil
}

// ListVersionsOptions selects a page of a version listing.
type ListVersionsOptions struct {
	Prefix    string
	Delimiter string
	// KeyMarker and VersionIDMarker resume a listing: versions of KeyMarker
	// older than VersionIDMarker, then the keys after KeyMarker. Without
	// VersionIDMarker, the listing resumes after every version of KeyMarker.
	KeyMarker       string
	VersionIDMarker string
	MaxKeys         int // versions, delete markers and common prefixes per page
}

// ListVersionsResult is one page of a version listing: keys in order, and
// each key's versions newest first.
type ListVersionsResult struct {
	Versions       []ObjectInfo // Key, VersionID, IsLatest, DeleteMarker, Size, ETag and ModTime are set
	CommonPrefixes []string
	IsTruncated    bool
	// NextKeyMarker and NextVersionIDMarker resume the listing; the version
	// marker is empty when the page ended with a common prefix.
	NextKeyMarker       string
	NextVersionIDMarker string
}

// ListVersions lists every version and delete marker in a bucket.
func (s *Store) ListVersions(ctx context.Context, bucket string, opts ListVersionsOptions) (ListVersionsResult, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return ListVersionsResult{}, err
	}
	var res ListVersionsResult
	if opts.MaxKeys <= 0 {
		return res, nil
	}

	// The scan position is (key, id) in the order key ascending, id
	// descending; each query reads what comes strictly after it.
	posKey, posID := opts.Prefix, int64(math.MaxInt64)
	if opts.KeyMarker != "" && opts.KeyMarker >= opts.Prefix {
		posKey, posID = opts.KeyMarker, 0 // after every version of the marker key
		if opts.VersionIDMarker != "" {
			var id int64
			err := s.read.QueryRowContext(ctx, `SELECT id FROM objects WHERE bucket_id = ? AND key = ? AND version_id = ?`,
				bid, opts.KeyMarker, opts.VersionIDMarker).Scan(&id)
			if err == nil {
				posID = id
			} else if !errors.Is(err, sql.ErrNoRows) {
				return ListVersionsResult{}, err
			}
		}
	}
	lastPrefix := ""
	count := 0
	for {
		rows, err := s.read.QueryContext(ctx, `SELECT id, key, version_id, is_latest, delete_marker, size, etag, modified_at
			FROM objects WHERE bucket_id = ?1 AND (key > ?2 OR (key = ?2 AND id < ?3))
			ORDER BY key, id DESC LIMIT ?4`, bid, posKey, posID, listBatch)
		if err != nil {
			return ListVersionsResult{}, err
		}
		read, jumped, done := 0, false, false
		for rows.Next() {
			var o ObjectInfo
			var id, modified int64
			if err := rows.Scan(&id, &o.Key, &o.VersionID, &o.IsLatest, &o.DeleteMarker, &o.Size, &o.ETag, &modified); err != nil {
				rows.Close()
				return ListVersionsResult{}, err
			}
			read++
			posKey, posID = o.Key, id
			if !strings.HasPrefix(o.Key, opts.Prefix) {
				done = true
				break
			}
			if opts.Delimiter != "" {
				rest := o.Key[len(opts.Prefix):]
				if i := strings.Index(rest, opts.Delimiter); i >= 0 {
					cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
					if cp == lastPrefix {
						continue
					}
					lastPrefix = cp
					if cp > opts.KeyMarker {
						if count == opts.MaxKeys {
							res.IsTruncated, done = true, true
							break
						}
						res.CommonPrefixes = append(res.CommonPrefixes, cp)
						res.NextKeyMarker, res.NextVersionIDMarker = cp, ""
						count++
					}
					posKey, posID, jumped = cp+maxRune, math.MaxInt64, true
					break
				}
			}
			if count == opts.MaxKeys {
				res.IsTruncated, done = true, true
				break
			}
			o.Bucket = bucket
			o.ModTime = fromNanos(modified)
			res.Versions = append(res.Versions, o)
			res.NextKeyMarker, res.NextVersionIDMarker = o.Key, o.VersionID
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return ListVersionsResult{}, err
		}
		if done || (read < listBatch && !jumped) {
			break
		}
	}
	if !res.IsTruncated {
		res.NextKeyMarker, res.NextVersionIDMarker = "", ""
	}
	return res, nil
}

// ObjectVersions returns up to limit versions of one key, newest first.
func (s *Store) ObjectVersions(ctx context.Context, bucket, key string, limit int) ([]ObjectInfo, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT version_id, is_latest, delete_marker, size, etag, modified_at
		FROM objects WHERE bucket_id = ? AND key = ? ORDER BY id DESC LIMIT ?`, bid, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectInfo
	for rows.Next() {
		o := ObjectInfo{Bucket: bucket, Key: key}
		var modified int64
		if err := rows.Scan(&o.VersionID, &o.IsLatest, &o.DeleteMarker, &o.Size, &o.ETag, &modified); err != nil {
			return nil, err
		}
		o.ModTime = fromNanos(modified)
		out = append(out, o)
	}
	return out, rows.Err()
}

// PurgePrefix removes every version and delete marker of the keys starting
// with prefix ("" empties the bucket for good), in batches, and returns how
// many versions were removed.
func (s *Store) PurgePrefix(ctx context.Context, bucket, prefix string) (int, error) {
	total := 0
	for {
		bid, err := bucketID(ctx, s.read, bucket)
		if err != nil {
			return total, err
		}
		rows, err := s.read.QueryContext(ctx, `SELECT key, version_id FROM objects
			WHERE bucket_id = ? AND key >= ? AND substr(key, 1, ?) = ? LIMIT 500`, bid, prefix, len(prefix), prefix)
		if err != nil {
			return total, err
		}
		type version struct{ key, id string }
		var batch []version
		for rows.Next() {
			var v version
			if err := rows.Scan(&v.key, &v.id); err != nil {
				rows.Close()
				return total, err
			}
			batch = append(batch, v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		if len(batch) == 0 {
			return total, nil
		}
		var dropped []string
		err = s.tx(ctx, func(tx *sql.Tx) error {
			keys := map[string]bool{}
			for _, v := range batch {
				blobs, err := dropVersion(ctx, tx, bid, v.key, v.id, s.nowNanos())
				if err != nil && !errors.Is(err, ErrNoSuchKey) {
					return err
				}
				dropped = append(dropped, blobs...)
				keys[v.key] = true
			}
			// A key with more versions than one batch keeps a latest version
			// until its last batch.
			for k := range keys {
				if err := promoteLatest(ctx, tx, bid, k); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		s.collect(ctx, dropped...)
		total += len(batch)
	}
}
