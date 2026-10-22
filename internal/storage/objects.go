package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"bin/internal/checksum"
)

// MaxKeyLength is the longest object key S3 accepts, in bytes.
const MaxKeyLength = 1024

// ObjectInfo describes the current version of an object.
type ObjectInfo struct {
	Bucket       string
	Key          string
	VersionID    string
	Size         int64
	ETag         string // unquoted
	SHA256       []byte
	ContentType  string
	Headers      map[string]string // Cache-Control, Content-Disposition, Content-Encoding, Content-Language, Expires
	UserMeta     map[string]string // lower-case names, without the x-amz-meta- prefix
	Checksums    map[checksum.Algorithm]string
	ChecksumType string
	PartsCount   int
	ModTime      time.Time

	blob string
}

// Sums are the digests of uploaded data, handed to PutOptions.Verify.
type Sums struct {
	MD5       []byte
	SHA256    []byte
	Checksums map[checksum.Algorithm][]byte
}

// PutOptions configures PutObject.
type PutOptions struct {
	ContentType string
	Headers     map[string]string
	UserMeta    map[string]string
	// Checksums are computed while the data is written and stored.
	Checksums []checksum.Algorithm
	// Verify runs once the data is durable on disk but before it becomes
	// visible. An error discards the upload. Use it to compare digests the
	// client sent, including ones that arrive in trailers after the body.
	Verify func(Sums) error
	// Conditional write: IfNoneMatch "*" fails if the key exists; IfMatch
	// fails unless the current ETag matches.
	IfMatch     string
	IfNoneMatch string
}

// ValidateKey checks S3's key constraints.
func ValidateKey(key string) error {
	if len(key) > MaxKeyLength {
		return ErrKeyTooLong
	}
	if key == "" || !utf8.ValidString(key) {
		return ErrInvalidKey
	}
	return nil
}

// PutObject stores size bytes from r as bucket/key, replacing the current
// object. Nothing becomes visible until the data is durable and verified.
func (s *Store) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts PutOptions) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	// Fail fast, before reading the body, if the bucket does not exist.
	if _, err := bucketID(ctx, s.read, bucket); err != nil {
		return ObjectInfo{}, err
	}

	blob, n, sums, err := s.writeBlob(r, size, opts.Checksums)
	if err != nil {
		return ObjectInfo{}, err
	}
	public := Sums{MD5: sums.MD5, SHA256: sums.SHA256, Checksums: sums.Checksums}
	if opts.Verify != nil {
		if err := opts.Verify(public); err != nil {
			s.removeBlob(blob)
			return ObjectInfo{}, err
		}
	}

	info := ObjectInfo{
		Bucket:      bucket,
		Key:         key,
		VersionID:   "null",
		Size:        n,
		ETag:        hex.EncodeToString(sums.MD5),
		SHA256:      sums.SHA256,
		ContentType: opts.ContentType,
		Headers:     nonNil(opts.Headers),
		UserMeta:    nonNil(opts.UserMeta),
		Checksums:   map[checksum.Algorithm]string{},
		ModTime:     time.Unix(0, s.nowNanos()).UTC(),
		blob:        blob,
	}
	for a, sum := range sums.Checksums {
		info.Checksums[a] = checksum.Encode(sum)
	}
	if len(info.Checksums) > 0 {
		info.ChecksumType = "FULL_OBJECT"
	}

	var replaced string
	err = s.tx(ctx, func(tx *sql.Tx) error {
		bid, err := bucketID(ctx, tx, bucket)
		if err != nil {
			return err
		}
		cur, err := latest(ctx, tx, bid, key)
		if err != nil && !errors.Is(err, ErrNoSuchKey) {
			return err
		}
		exists := err == nil
		if opts.IfNoneMatch != "" && exists && (opts.IfNoneMatch == "*" || etagMatches(opts.IfNoneMatch, cur.ETag)) {
			return ErrPrecondition
		}
		if opts.IfMatch != "" {
			if !exists {
				return ErrNoSuchKey
			}
			if !etagMatches(opts.IfMatch, cur.ETag) {
				return ErrPrecondition
			}
		}
		if exists {
			if replaced, err = dropVersion(ctx, tx, bid, key, "null", s.nowNanos()); err != nil {
				return err
			}
		}
		return insertObject(ctx, tx, bid, info)
	})
	if err != nil {
		s.removeBlob(blob)
		return ObjectInfo{}, err
	}
	s.collect(ctx, replaced)
	return info, nil
}

// StatObject returns the metadata of bucket/key.
func (s *Store) StatObject(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := latest(ctx, s.read, bid, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info.Bucket = bucket
	return info, nil
}

// OpenObject returns the metadata of bucket/key and its data, positioned at
// the start. The caller must close the file. Use NewVerifiedReader to check
// the data's integrity while reading it whole.
func (s *Store) OpenObject(ctx context.Context, bucket, key string) (ObjectInfo, *os.File, error) {
	// The blob may be replaced and deleted between reading the metadata and
	// opening the file; a fresh lookup then finds the new one.
	for attempt := 0; ; attempt++ {
		info, err := s.StatObject(ctx, bucket, key)
		if err != nil {
			return ObjectInfo{}, nil, err
		}
		f, err := os.Open(s.blobPath(info.blob))
		if err == nil {
			return info, f, nil
		}
		if !errors.Is(err, os.ErrNotExist) || attempt == 2 {
			return ObjectInfo{}, nil, err
		}
	}
}

// DeleteObject removes bucket/key. Deleting a missing key succeeds, as in S3.
func (s *Store) DeleteObject(ctx context.Context, bucket, key string) error {
	var dropped string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		bid, err := bucketID(ctx, tx, bucket)
		if err != nil {
			return err
		}
		dropped, err = dropVersion(ctx, tx, bid, key, "null", s.nowNanos())
		if errors.Is(err, ErrNoSuchKey) {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	s.collect(ctx, dropped)
	return nil
}

// CopyOptions configures CopyObject.
type CopyOptions struct {
	// Check inspects the source before any data is copied, e.g. to evaluate
	// x-amz-copy-source-if-* conditions. An error aborts the copy.
	Check func(src ObjectInfo) error
	// ReplaceMetadata uses the fields below instead of the source's.
	ReplaceMetadata bool
	ContentType     string
	Headers         map[string]string
	UserMeta        map[string]string
	// Checksums to compute for the copy; the source's algorithms are always kept.
	Checksums []checksum.Algorithm
}

// CopyObject copies srcBucket/srcKey to dstBucket/dstKey. The source data is
// verified against its SHA-256 while it is copied; corrupt data is not copied.
func (s *Store) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, opts CopyOptions) (ObjectInfo, error) {
	src, f, err := s.OpenObject(ctx, srcBucket, srcKey)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer f.Close()
	if opts.Check != nil {
		if err := opts.Check(src); err != nil {
			return ObjectInfo{}, err
		}
	}
	put := PutOptions{
		ContentType: src.ContentType,
		Headers:     src.Headers,
		UserMeta:    src.UserMeta,
		Checksums:   opts.Checksums,
	}
	if opts.ReplaceMetadata {
		put.ContentType, put.Headers, put.UserMeta = opts.ContentType, opts.Headers, opts.UserMeta
	}
	for a := range src.Checksums {
		if !containsAlg(put.Checksums, a) {
			put.Checksums = append(put.Checksums, a)
		}
	}
	return s.PutObject(ctx, dstBucket, dstKey, NewVerifiedReader(f, src.SHA256), src.Size, put)
}

func containsAlg(list []checksum.Algorithm, a checksum.Algorithm) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

// etagMatches compares an If-Match/If-None-Match value (possibly quoted, or a
// comma-separated list) with an unquoted ETag.
func etagMatches(header, etag string) bool {
	for _, part := range bytes.Split([]byte(header), []byte(",")) {
		v := string(bytes.TrimSpace(part))
		v = trimQuotes(v)
		if v == "*" || v == etag {
			return true
		}
	}
	return false
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// --- rows -------------------------------------------------------------------

const objectColumns = `version_id, blob, size, etag, sha256, content_type, headers, user_meta,
	checksums, checksum_type, parts, modified_at`

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// latest returns the current, non-deleted version of a key.
func latest(ctx context.Context, q rowQuerier, bucketID int64, key string) (ObjectInfo, error) {
	var (
		info                       ObjectInfo
		blob                       sql.NullString
		headers, meta, sums, parts string
		modified                   int64
	)
	err := q.QueryRowContext(ctx, `SELECT `+objectColumns+` FROM objects
		WHERE bucket_id = ? AND key = ? AND is_latest = 1 AND delete_marker = 0`, bucketID, key).
		Scan(&info.VersionID, &blob, &info.Size, &info.ETag, &info.SHA256, &info.ContentType,
			&headers, &meta, &sums, &info.ChecksumType, &parts, &modified)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectInfo{}, ErrNoSuchKey
	}
	if err != nil {
		return ObjectInfo{}, err
	}
	info.Key = key
	info.blob = blob.String
	info.ModTime = fromNanos(modified)
	info.Headers, info.UserMeta = map[string]string{}, map[string]string{}
	info.Checksums = map[checksum.Algorithm]string{}
	for _, f := range []struct {
		src string
		dst any
	}{{headers, &info.Headers}, {meta, &info.UserMeta}, {sums, &info.Checksums}} {
		if err := json.Unmarshal([]byte(f.src), f.dst); err != nil {
			return ObjectInfo{}, err
		}
	}
	if parts != "" {
		var p []json.RawMessage
		if err := json.Unmarshal([]byte(parts), &p); err != nil {
			return ObjectInfo{}, err
		}
		info.PartsCount = len(p)
	}
	return info, nil
}

func insertObject(ctx context.Context, tx *sql.Tx, bucketID int64, o ObjectInfo) error {
	headers, _ := json.Marshal(o.Headers)
	meta, _ := json.Marshal(o.UserMeta)
	sums, _ := json.Marshal(o.Checksums)
	_, err := tx.ExecContext(ctx, `INSERT INTO objects (bucket_id, key, version_id, is_latest, blob,
		size, etag, sha256, content_type, headers, user_meta, checksums, checksum_type, modified_at)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bucketID, o.Key, o.VersionID, o.blob, o.Size, o.ETag, o.SHA256, o.ContentType,
		string(headers), string(meta), string(sums), o.ChecksumType, o.ModTime.UnixNano())
	return err
}

// dropVersion deletes one version row and queues its blob for removal. It
// returns the queued blob ID.
func dropVersion(ctx context.Context, tx *sql.Tx, bucketID int64, key, versionID string, now int64) (string, error) {
	var blob sql.NullString
	err := tx.QueryRowContext(ctx, `DELETE FROM objects WHERE bucket_id = ? AND key = ? AND version_id = ?
		RETURNING blob`, bucketID, key, versionID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoSuchKey
	}
	if err != nil || !blob.Valid {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO gc_blobs (blob, queued_at) VALUES (?, ?)`, blob.String, now)
	return blob.String, err
}

// --- integrity ---------------------------------------------------------------

// holdback is how much verified-but-unreleased data NewVerifiedReader keeps
// until the whole blob has been checked.
const holdback = 64 << 10

// verifiedReader hashes data as it reads and withholds the last holdback
// bytes until EOF, so a reader of corrupt data never receives the complete
// object: it gets ErrCorrupt before the final bytes instead.
type verifiedReader struct {
	r    io.Reader
	h    hash.Hash
	want []byte
	buf  []byte // read and hashed, not yet released
	tmp  []byte
	eof  bool
	err  error
}

// NewVerifiedReader wraps r, whose full content must hash to sha256sum.
func NewVerifiedReader(r io.Reader, sha256sum []byte) io.Reader {
	return &verifiedReader{r: r, h: sha256.New(), want: sha256sum, tmp: make([]byte, 128<<10)}
}

func (v *verifiedReader) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	for !v.eof && len(v.buf) <= holdback+len(p) {
		n, err := v.r.Read(v.tmp)
		v.h.Write(v.tmp[:n])
		v.buf = append(v.buf, v.tmp[:n]...)
		if err == io.EOF {
			v.eof = true
			if !bytes.Equal(v.h.Sum(nil), v.want) {
				v.err = ErrCorrupt
				return 0, v.err
			}
		} else if err != nil {
			v.err = err
			return 0, err
		}
	}
	avail := len(v.buf)
	if !v.eof {
		avail -= holdback
	}
	if avail <= 0 {
		if v.eof {
			return 0, io.EOF
		}
		return 0, nil
	}
	n := copy(p, v.buf[:avail])
	v.buf = v.buf[n:]
	if cap(v.buf) > 4*holdback && len(v.buf) < holdback {
		v.buf = append([]byte(nil), v.buf...) // let the old backing array go
	}
	return n, nil
}
