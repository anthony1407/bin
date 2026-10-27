package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrNoSuchShareLink = errors.New("no such share link")

// ShareLink is a presigned download link issued from the console.
type ShareLink struct {
	ID            string
	Bucket        string
	Key           string
	SignatureHash string
	URLEnc        string // encrypted by the caller
	Download      bool
	CreatedBy     string
	Created       time.Time
	Expires       time.Time
	Revoked       time.Time // zero: not revoked
}

// ShareSummary is how an object is shared right now.
type ShareSummary struct {
	Active      int
	LastExpires time.Time // the latest expiry among active links
}

const shareLinkColumns = `id, bucket, key, signature_hash, url_enc, download, created_by, created_at, expires_at, revoked_at`

func scanShareLink(scan func(...any) error) (ShareLink, error) {
	var l ShareLink
	var created, expires, revoked int64
	err := scan(&l.ID, &l.Bucket, &l.Key, &l.SignatureHash, &l.URLEnc, &l.Download, &l.CreatedBy, &created, &expires, &revoked)
	l.Created, l.Expires, l.Revoked = fromNanos(created), fromNanos(expires), timeOrZero(revoked)
	return l, err
}

// CreateShareLink records an issued link.
func (s *Store) CreateShareLink(ctx context.Context, l ShareLink) error {
	_, err := s.exec(ctx, `INSERT INTO share_links (`+shareLinkColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		l.ID, l.Bucket, l.Key, l.SignatureHash, l.URLEnc, l.Download, l.CreatedBy, s.nowNanos(), l.Expires.UnixNano())
	return err
}

// ActiveShareLinks returns the unexpired, unrevoked links of an object, the
// ones expiring last first.
func (s *Store) ActiveShareLinks(ctx context.Context, bucket, key string) ([]ShareLink, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+shareLinkColumns+` FROM share_links
		WHERE bucket = ? AND key = ? AND revoked_at = 0 AND expires_at > ? ORDER BY expires_at DESC`,
		bucket, key, s.nowNanos())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShareLink{}
	for rows.Next() {
		l, err := scanShareLink(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ShareSummaries returns, for every object of bucket that has active links,
// how many and until when.
func (s *Store) ShareSummaries(ctx context.Context, bucket string) (map[string]ShareSummary, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT key, count(*), max(expires_at) FROM share_links
		WHERE bucket = ? AND revoked_at = 0 AND expires_at > ? GROUP BY key`, bucket, s.nowNanos())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ShareSummary{}
	for rows.Next() {
		var key string
		var sum ShareSummary
		var last int64
		if err := rows.Scan(&key, &sum.Active, &last); err != nil {
			return nil, err
		}
		sum.LastExpires = fromNanos(last)
		out[key] = sum
	}
	return out, rows.Err()
}

// RevokeShareLink stops one link; the S3 API refuses it from then on.
func (s *Store) RevokeShareLink(ctx context.Context, id string) (ShareLink, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+shareLinkColumns+` FROM share_links WHERE id = ?`, id)
	l, err := scanShareLink(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ShareLink{}, ErrNoSuchShareLink
	} else if err != nil {
		return ShareLink{}, err
	}
	if _, err := s.exec(ctx, `UPDATE share_links SET revoked_at = ? WHERE id = ? AND revoked_at = 0`,
		s.nowNanos(), id); err != nil {
		return ShareLink{}, err
	}
	return l, nil
}

// RevokeAllShareLinks marks every link revoked, when the share key rotates.
func (s *Store) RevokeAllShareLinks(ctx context.Context) error {
	_, err := s.exec(ctx, `UPDATE share_links SET revoked_at = ? WHERE revoked_at = 0`, s.nowNanos())
	return err
}

// ShareSignatureRevoked reports whether a presigned request carries the
// signature of a revoked share link. Unknown signatures (other presigned
// URLs, links issued before links were recorded) are not revoked.
func (s *Store) ShareSignatureRevoked(ctx context.Context, signatureHash string) (bool, error) {
	epoch := s.Epoch()
	if v, ok := s.revoked.Get(signatureHash, epoch); ok {
		return v, nil
	}
	var revoked int64
	err := s.read.QueryRowContext(ctx, `SELECT revoked_at FROM share_links WHERE signature_hash = ?`, signatureHash).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return false, err
	}
	s.revoked.Put(signatureHash, revoked != 0, epoch)
	return revoked != 0, nil
}

// pruneShareLinks forgets links that ended (expired or revoked) over a week
// ago; they can no longer be used, and the console only shows live ones.
func (s *Store) pruneShareLinks(ctx context.Context) {
	cutoff := s.now().Add(-7 * 24 * time.Hour).UnixNano()
	s.exec(ctx, `DELETE FROM share_links WHERE expires_at < ? OR (revoked_at != 0 AND revoked_at < ?)`, cutoff, cutoff)
}
