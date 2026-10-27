package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"bin/internal/checksum"
)

// MaxKeyLength is the longest object key S3 accepts, in bytes.
const MaxKeyLength = 1024

// ObjectInfo describes one version of an object, usually the current one.
type ObjectInfo struct {
	Bucket       string
	Key          string
	VersionID    string // NullVersion unless written with versioning enabled
	IsLatest     bool   // set by version lookups and listings
	DeleteMarker bool
	Size         int64
	ETag         string // unquoted; "<md5>-<n>" for multipart objects
	SHA256       []byte // whole-object hash; nil for multipart objects (see parts)
	ContentType  string
	Headers      map[string]string // Cache-Control, Content-Disposition, Content-Encoding, Content-Language, Expires
	UserMeta     map[string]string // lower-case names, without the x-amz-meta- prefix
	Checksums    map[checksum.Algorithm]string
	ChecksumType string
	PartsCount   int // 0 for objects stored by a single PUT
	ModTime      time.Time

	blob  string     // single-PUT objects
	parts []partData // multipart objects, in part order
}

// partData locates one stored part of a multipart object.
type partData struct {
	Number   int
	Blob     string
	Offset   int64
	Size     int64
	ETag     string
	SHA256   []byte
	Checksum string
}

// Part returns the byte range of part n (1-based). A single-PUT object has
// exactly one part, the whole object.
func (o ObjectInfo) Part(n int) (offset, size int64, ok bool) {
	if o.PartsCount == 0 {
		return 0, o.Size, n == 1
	}
	if n < 1 || n > len(o.parts) {
		return 0, 0, false
	}
	p := o.parts[n-1]
	return p.Offset, p.Size, true
}

// dataParts returns the blobs holding the object's bytes, in order.
func (o ObjectInfo) dataParts() []partData {
	if o.PartsCount == 0 {
		return []partData{{Number: 1, Blob: o.blob, Size: o.Size, SHA256: o.SHA256}}
	}
	return o.parts
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

// PutObject stores size bytes from r as bucket/key: a new version when the
// bucket has versioning enabled, otherwise a replacement of the null version.
// Nothing becomes visible until the data is durable and verified.
func (s *Store) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts PutOptions) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	// Fail fast, before reading the body, if the bucket does not exist.
	if _, err := bucketID(ctx, s.read, bucket); err != nil {
		return ObjectInfo{}, err
	}

	if err := s.precheckQuota(ctx, bucket, key, size); err != nil {
		return ObjectInfo{}, err
	}
	blob, n, sums, err := s.writeBlob(r, size, opts.Checksums)
	if err != nil {
		return ObjectInfo{}, err
	}
	if opts.Verify != nil {
		if err := opts.Verify(Sums(sums)); err != nil {
			s.removeBlob(blob)
			return ObjectInfo{}, err
		}
	}

	info := ObjectInfo{
		Bucket:      bucket,
		Key:         key,
		IsLatest:    true,
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

	var replaced []string
	err = s.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		bid, err := bucketID(ctx, tx, bucket)
		if err != nil {
			return err
		}
		if info.VersionID, replaced, err = s.beginWrite(ctx, tx, bid, key, opts.IfMatch, opts.IfNoneMatch); err != nil {
			return err
		}
		if err := checkQuota(ctx, tx, bid, info.Size); err != nil {
			return err // a replaced null version is already excluded from the usage
		}
		_, err = insertLatest(ctx, tx, bid, info)
		return err
	})
	if err != nil {
		s.removeBlob(blob)
		return ObjectInfo{}, err
	}
	s.collect(ctx, replaced...)
	return info, nil
}

// precheckQuota rejects a write of size bytes before any data is read when
// it would clearly exceed the bucket quota. The authoritative check happens
// again at commit time.
func (s *Store) precheckQuota(ctx context.Context, bucket, key string, size int64) error {
	if size <= 0 {
		return nil
	}
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return err
	}
	grow := size
	// Only the null version is replaced; with versioning enabled every
	// write adds to the usage.
	if v, err := bucketVersioning(ctx, s.read, bid); err == nil && v != VersioningEnabled {
		if cur, err := loadVersion(ctx, s.read, bid, key, NullVersion); err == nil {
			grow -= cur.Size
		}
	}
	return checkQuota(ctx, s.read, bid, grow)
}

// StatObject returns the metadata of the current version of bucket/key. A key
// whose latest version is a delete marker does not exist.
func (s *Store) StatObject(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	info, err := s.StatVersion(ctx, bucket, key, "")
	if errors.Is(err, ErrDeleteMarker) {
		return ObjectInfo{}, ErrNoSuchKey
	}
	return info, err
}

// DeleteObject deletes bucket/key as DeleteVersion does without a version ID.
// Deleting a missing key succeeds, as in S3.
func (s *Store) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := s.DeleteVersion(ctx, bucket, key, "")
	return err
}

// CopyOptions configures CopyObject.
type CopyOptions struct {
	// SourceVersionID copies that version of the source instead of its latest.
	SourceVersionID string
	// Check inspects the source before any data is copied, e.g. to evaluate
	// x-amz-copy-source-if-* conditions. An error aborts the copy.
	Check func(src ObjectInfo) error
	// ReplaceMetadata uses the fields below instead of the source's.
	ReplaceMetadata bool
	ContentType     string
	Headers         map[string]string
	UserMeta        map[string]string
	// Checksums to compute for the copy; full-object CRC algorithms of the
	// source are kept.
	Checksums []checksum.Algorithm
}

// CopyObject copies srcBucket/srcKey to dstBucket/dstKey as a single-part
// object. The source data is verified against its SHA-256 (per part for
// multipart sources) while it is copied; corrupt data is not copied.
func (s *Store) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, opts CopyOptions) (ObjectInfo, error) {
	src, data, err := s.openSource(ctx, srcBucket, srcKey, opts.SourceVersionID)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer data.Close()
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
		if !containsAlg(put.Checksums, a) && (src.ChecksumType == "FULL_OBJECT" || src.PartsCount == 0) {
			put.Checksums = append(put.Checksums, a)
		}
	}
	return s.PutObject(ctx, dstBucket, dstKey, data.Verified(), src.Size, put)
}

// openSource opens the source of a copy: a deleted object does not exist, and
// naming a delete marker by its version ID fails with ErrDeleteMarker.
func (s *Store) openSource(ctx context.Context, bucket, key, versionID string) (ObjectInfo, *ObjectData, error) {
	if versionID == "" {
		return s.OpenObject(ctx, bucket, key)
	}
	return s.OpenVersion(ctx, bucket, key, versionID)
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
	for _, v := range strings.Split(header, ",") {
		v = trimQuotes(strings.TrimSpace(v))
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

const objectColumns = `id, version_id, is_latest, delete_marker, blob, size, etag, sha256, content_type,
	headers, user_meta, checksums, checksum_type, parts_count, modified_at`

// objectColumnsQualified is objectColumns for queries joining objects as "o".
const objectColumnsQualified = `o.id, o.version_id, o.is_latest, o.delete_marker, o.blob, o.size, o.etag, o.sha256,
	o.content_type, o.headers, o.user_meta, o.checksums, o.checksum_type, o.parts_count, o.modified_at`

type rowsQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// latest returns the current version of a key; a key whose latest version is
// a delete marker does not exist.
func latest(ctx context.Context, q rowsQuerier, bucketID int64, key string) (ObjectInfo, error) {
	info, err := loadVersion(ctx, q, bucketID, key, "")
	if err == nil && info.DeleteMarker {
		return ObjectInfo{}, ErrNoSuchKey
	}
	return info, err
}

// loadVersion returns one version of a key, delete markers included: the
// given versionID, or the latest version when versionID is "".
func loadVersion(ctx context.Context, q rowsQuerier, bucketID int64, key, versionID string) (ObjectInfo, error) {
	query, args := `SELECT `+objectColumns+` FROM objects WHERE bucket_id = ? AND key = ? AND is_latest = 1`, []any{bucketID, key}
	if versionID != "" {
		query = `SELECT ` + objectColumns + ` FROM objects WHERE bucket_id = ? AND key = ? AND version_id = ?`
		args = append(args, versionID)
	}
	return scanVersion(ctx, q, q.QueryRowContext(ctx, query, args...), key, versionID)
}

// scanVersion reads a row of objectColumns (or objectColumnsQualified) and
// the object's parts.
func scanVersion(ctx context.Context, q rowsQuerier, row *sql.Row, key, versionID string) (ObjectInfo, error) {
	var (
		info                ObjectInfo
		id, modified        int64
		blob                sql.NullString
		headers, meta, sums string
	)
	err := row.Scan(&id, &info.VersionID, &info.IsLatest, &info.DeleteMarker, &blob, &info.Size, &info.ETag, &info.SHA256,
		&info.ContentType, &headers, &meta, &sums, &info.ChecksumType, &info.PartsCount, &modified)
	if errors.Is(err, sql.ErrNoRows) {
		if versionID != "" {
			return ObjectInfo{}, ErrNoSuchVersion
		}
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
		if f.src == "{}" {
			continue // the common case needs no JSON decoding
		}
		if err := json.Unmarshal([]byte(f.src), f.dst); err != nil {
			return ObjectInfo{}, err
		}
	}
	if info.PartsCount > 0 {
		if info.parts, err = objectParts(ctx, q, id); err != nil {
			return ObjectInfo{}, err
		}
	}
	return info, nil
}

func objectParts(ctx context.Context, q rowsQuerier, objectID int64) ([]partData, error) {
	rows, err := q.QueryContext(ctx, `SELECT part_number, blob, start_offset, size, etag, sha256, checksum
		FROM object_parts WHERE object_id = ? ORDER BY part_number`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var parts []partData
	for rows.Next() {
		var p partData
		if err := rows.Scan(&p.Number, &p.Blob, &p.Offset, &p.Size, &p.ETag, &p.SHA256, &p.Checksum); err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	return parts, rows.Err()
}

// insertObject stores an object row (and its parts) as the latest version
// and returns its row ID; see insertLatest.
func insertObject(ctx context.Context, tx *sql.Tx, bucketID int64, o ObjectInfo) (int64, error) {
	headers, _ := json.Marshal(o.Headers)
	meta, _ := json.Marshal(o.UserMeta)
	sums, _ := json.Marshal(o.Checksums)
	var blob any
	if o.blob != "" {
		blob = o.blob
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO objects (bucket_id, key, version_id, is_latest, delete_marker, blob,
		size, etag, sha256, content_type, headers, user_meta, checksums, checksum_type, parts_count, modified_at)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		bucketID, o.Key, o.VersionID, o.DeleteMarker, blob, o.Size, o.ETag, o.SHA256, o.ContentType,
		string(headers), string(meta), string(sums), o.ChecksumType, len(o.parts), o.ModTime.UnixNano()).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, p := range o.parts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO object_parts (object_id, part_number, blob,
			start_offset, size, etag, sha256, checksum) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, p.Number, p.Blob, p.Offset, p.Size, p.ETag, p.SHA256, p.Checksum); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// dropVersion deletes one version row (its parts go with it) and queues its
// blobs for removal, returning them.
func dropVersion(ctx context.Context, tx *sql.Tx, bucketID int64, key, versionID string, now int64) ([]string, error) {
	var id int64
	var blob sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, blob FROM objects WHERE bucket_id = ? AND key = ? AND version_id = ?`,
		bucketID, key, versionID).Scan(&id, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSuchKey
	}
	if err != nil {
		return nil, err
	}
	var blobs []string
	if blob.Valid {
		blobs = append(blobs, blob.String)
	}
	parts, err := objectParts(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		blobs = append(blobs, p.Blob)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM objects WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return blobs, queueBlobs(ctx, tx, blobs, now)
}

// queueBlobs records blobs for deletion once the transaction commits.
func queueBlobs(ctx context.Context, tx *sql.Tx, blobs []string, now int64) error {
	for _, b := range blobs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO gc_blobs (blob, queued_at) VALUES (?, ?)`, b, now); err != nil {
			return err
		}
	}
	return nil
}
