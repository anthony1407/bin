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

	// Filled by ListDropzones: what was uploaded through the link.
	Uploads       int
	UploadedBytes int64
	LastUpload    time.Time // zero: nothing yet
}

// DropzoneUpload is one file uploaded through a dropzone.
type DropzoneUpload struct {
	Key       string // full object key
	Size      int64
	Uploaded  time.Time
	Remote    string // uploader IP
	UserAgent string // uploader browser, as sent
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

// ListDropzones returns every dropzone, expired ones included, newest first,
// with a summary of what was uploaded through each.
func (s *Store) ListDropzones(ctx context.Context) ([]Dropzone, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT d.id, d.token_hash, d.token_enc, d.bucket, d.prefix, d.note,
		d.max_bytes, d.created_at, d.expires_at,
		count(u.id), coalesce(sum(u.size), 0), coalesce(max(u.uploaded_at), 0)
		FROM dropzones d LEFT JOIN dropzone_uploads u ON u.dropzone_id = d.id
		GROUP BY d.id ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dropzone
	for rows.Next() {
		var d Dropzone
		var created, expires, last int64
		if err := rows.Scan(&d.ID, &d.TokenHash, &d.TokenEnc, &d.Bucket, &d.Prefix, &d.Note, &d.MaxBytes,
			&created, &expires, &d.Uploads, &d.UploadedBytes, &last); err != nil {
			return nil, err
		}
		d.Created, d.Expires, d.LastUpload = fromNanos(created), fromNanos(expires), timeOrZero(last)
		out = append(out, d)
	}
	return out, rows.Err()
}

// RecordDropzoneUpload remembers a file uploaded through a dropzone.
func (s *Store) RecordDropzoneUpload(ctx context.Context, id string, u DropzoneUpload) error {
	_, err := s.exec(ctx, `INSERT INTO dropzone_uploads (dropzone_id, key, size, uploaded_at, remote, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)`, id, u.Key, u.Size, s.nowNanos(), u.Remote, u.UserAgent)
	return err
}

// DropzoneUploads returns up to limit uploads of a dropzone, newest first.
func (s *Store) DropzoneUploads(ctx context.Context, id string, limit int) ([]DropzoneUpload, error) {
	var one int
	if err := s.read.QueryRowContext(ctx, `SELECT 1 FROM dropzones WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSuchDropzone
	} else if err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT key, size, uploaded_at, remote, user_agent FROM dropzone_uploads
		WHERE dropzone_id = ? ORDER BY uploaded_at DESC, id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DropzoneUpload{}
	for rows.Next() {
		var u DropzoneUpload
		var at int64
		if err := rows.Scan(&u.Key, &u.Size, &at, &u.Remote, &u.UserAgent); err != nil {
			return nil, err
		}
		u.Uploaded = fromNanos(at)
		out = append(out, u)
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
	res, err := s.exec(ctx, `DELETE FROM dropzones WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchDropzone
	}
	return nil
}
