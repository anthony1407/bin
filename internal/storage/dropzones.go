package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrNoSuchDropzone = errors.New("no such dropzone")

// Dropzone is a public upload link: whoever holds its token may upload files
// of up to MaxBytes under Bucket/Prefix until Expires.
type Dropzone struct {
	ID        string
	TokenHash string
	TokenEnc  string // encrypted by the caller
	Bucket    string
	Prefix    string
	Note      string
	MaxBytes  int64
	Created   time.Time
	Expires   time.Time
}

const dropzoneColumns = `id, token_hash, token_enc, bucket, prefix, note, max_bytes, created_at, expires_at`

func scanDropzone(scan func(...any) error) (Dropzone, error) {
	var d Dropzone
	var created, expires int64
	err := scan(&d.ID, &d.TokenHash, &d.TokenEnc, &d.Bucket, &d.Prefix, &d.Note, &d.MaxBytes, &created, &expires)
	d.Created, d.Expires = fromNanos(created), fromNanos(expires)
	return d, err
}

// CreateDropzone stores a new dropzone in an existing bucket.
func (s *Store) CreateDropzone(ctx context.Context, d Dropzone) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM buckets WHERE name = ?`, d.Bucket).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrNoSuchBucket
		} else if err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO dropzones (`+dropzoneColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.TokenHash, d.TokenEnc, d.Bucket, d.Prefix, d.Note, d.MaxBytes, s.nowNanos(), d.Expires.UnixNano())
		return err
	})
}

// ListDropzones returns every dropzone, expired ones included, newest first.
func (s *Store) ListDropzones(ctx context.Context) ([]Dropzone, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+dropzoneColumns+` FROM dropzones ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dropzone
	for rows.Next() {
		d, err := scanDropzone(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DropzoneByToken returns the live dropzone with this token hash; expired
// ones are ErrNoSuchDropzone.
func (s *Store) DropzoneByToken(ctx context.Context, tokenHash string) (Dropzone, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+dropzoneColumns+` FROM dropzones WHERE token_hash = ?`, tokenHash)
	d, err := scanDropzone(row.Scan)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !d.Expires.After(fromNanos(s.nowNanos()))) {
		return Dropzone{}, ErrNoSuchDropzone
	}
	return d, err
}

// DeleteDropzone removes a dropzone; its link stops working at once.
func (s *Store) DeleteDropzone(ctx context.Context, id string) error {
	res, err := s.write.ExecContext(ctx, `DELETE FROM dropzones WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchDropzone
	}
	return nil
}
