package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"time"
)

// Bucket describes a bucket.
type Bucket struct {
	Name       string
	CreatedAt  time.Time
	Versioning string // "", "Enabled" or "Suspended"
	QuotaBytes int64  // 0: unlimited
	Policy     string // access policy JSON (package auth); "": private
}

const bucketColumns = `name, created_at, versioning, quota_bytes, policy`

func scanBucket(scan func(...any) error) (Bucket, error) {
	var b Bucket
	var created int64
	err := scan(&b.Name, &created, &b.Versioning, &b.QuotaBytes, &b.Policy)
	b.CreatedAt = fromNanos(created)
	return b, err
}

// ValidBucketName applies the AWS naming rules for general purpose buckets.
func ValidBucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	first, last := name[0], name[len(name)-1]
	if first == '.' || first == '-' || last == '.' || last == '-' {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	for _, p := range []string{"xn--", "sthree-", "amzn-s3-demo-"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	for _, s := range []string{"-s3alias", "--ol-s3", ".mrap", "--x-s3", "--table-s3"} {
		if strings.HasSuffix(name, s) {
			return false
		}
	}
	return true
}

// CreateBucket creates an empty bucket.
func (s *Store) CreateBucket(ctx context.Context, name string) error {
	if !ValidBucketName(name) {
		return ErrInvalidBucketName
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := bucketID(ctx, tx, name); err == nil {
			return ErrBucketExists
		} else if !errors.Is(err, ErrNoSuchBucket) {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO buckets (name, created_at) VALUES (?, ?)`, name, s.nowNanos())
		return err
	})
}

// GetBucket returns a bucket's settings.
func (s *Store) GetBucket(ctx context.Context, name string) (Bucket, error) {
	b, err := scanBucket(s.read.QueryRowContext(ctx, `SELECT `+bucketColumns+` FROM buckets WHERE name = ?`, name).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Bucket{}, ErrNoSuchBucket
	}
	return b, err
}

// ListBuckets returns every bucket, ordered by name.
func (s *Store) ListBuckets(ctx context.Context) ([]Bucket, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+bucketColumns+` FROM buckets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		b, err := scanBucket(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBucket removes an empty bucket. Any object version, including delete
// markers, makes it non-empty; in-progress multipart uploads are aborted.
func (s *Store) DeleteBucket(ctx context.Context, name string) error {
	var dropped []string
	defer func() { s.collect(context.Background(), dropped...) }()
	return s.tx(ctx, func(tx *sql.Tx) error {
		id, err := bucketID(ctx, tx, name)
		if err != nil {
			return err
		}
		var one int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM objects WHERE bucket_id = ? LIMIT 1`, id).Scan(&one)
		if err == nil {
			return ErrBucketNotEmpty
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM uploads WHERE bucket_id = ?`, id)
		if err != nil {
			return err
		}
		var uploads []string
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			uploads = append(uploads, u)
		}
		rows.Close()
		for _, u := range uploads {
			blobs, err := s.dropUpload(ctx, tx, u)
			if err != nil {
				return err
			}
			dropped = append(dropped, blobs...)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM bucket_tags WHERE bucket_id = ?`, id); err != nil {
			return err
		}
		// A bucket created later with the same name must not inherit its upload links.
		if _, err := tx.ExecContext(ctx, `DELETE FROM dropzones WHERE bucket = ?`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM share_links WHERE bucket = ?`, name); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM buckets WHERE id = ?`, id)
		return err
	})
}

// BucketTags returns the bucket's tag set, or ErrNoTags if it has none.
func (s *Store) BucketTags(ctx context.Context, name string) (map[string]string, error) {
	id, err := bucketID(ctx, s.read, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT key, value FROM bucket_tags WHERE bucket_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		tags[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, ErrNoTags
	}
	return tags, nil
}

// SetBucketTags replaces the bucket's tag set. An empty set removes all tags.
func (s *Store) SetBucketTags(ctx context.Context, name string, tags map[string]string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		id, err := bucketID(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM bucket_tags WHERE bucket_id = ?`, id); err != nil {
			return err
		}
		for k, v := range tags {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO bucket_tags (bucket_id, key, value) VALUES (?, ?, ?)`, id, k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func bucketID(ctx context.Context, q querier, name string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM buckets WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoSuchBucket
	}
	return id, err
}
