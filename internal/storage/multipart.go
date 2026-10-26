package storage

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"bin/internal/checksum"
)

const (
	// MinPartSize is the smallest size allowed for every part but the last.
	MinPartSize = 5 << 20
	// MaxPartNumber is the highest part number S3 accepts.
	MaxPartNumber = 10000
)

var (
	ErrNoSuchUpload        = errors.New("no such upload")
	ErrInvalidPart         = errors.New("a listed part is missing or its ETag or checksum differs")
	ErrInvalidPartOrder    = errors.New("parts are not in ascending order")
	ErrEntityTooSmall      = errors.New("a part other than the last is smaller than the minimum")
	ErrSizeMismatch        = errors.New("object size differs from x-amz-mp-object-size")
	ErrBadDigest           = errors.New("checksum mismatch")
	ErrInvalidChecksumType = errors.New("checksum type not supported for this algorithm")
)

// Upload is an in-progress multipart upload.
type Upload struct {
	ID                string
	Bucket            string
	Key               string
	Created           time.Time
	ChecksumAlgorithm checksum.Algorithm // "" if none was requested
	ChecksumType      string             // "FULL_OBJECT" or "COMPOSITE"
}

// UploadOptions configures CreateUpload. Metadata is applied to the object
// when the upload completes.
type UploadOptions struct {
	ContentType       string
	Headers           map[string]string
	UserMeta          map[string]string
	ChecksumAlgorithm checksum.Algorithm
	ChecksumType      string // "", "FULL_OBJECT" or "COMPOSITE"
}

// PartInfo describes an uploaded part.
type PartInfo struct {
	Number    int
	Size      int64
	ETag      string // unquoted MD5
	Checksums map[checksum.Algorithm]string
	ModTime   time.Time
}

// CreateUpload starts a multipart upload.
func (s *Store) CreateUpload(ctx context.Context, bucket, key string, opts UploadOptions) (Upload, error) {
	if err := ValidateKey(key); err != nil {
		return Upload{}, err
	}
	u := Upload{Bucket: bucket, Key: key, ChecksumAlgorithm: opts.ChecksumAlgorithm, Created: fromNanos(s.nowNanos())}
	if a := opts.ChecksumAlgorithm; a != "" {
		// S3 defaults: CRC64NVME is always full-object; the other CRCs are
		// composite unless asked otherwise; SHA digests cannot be combined.
		switch {
		case opts.ChecksumType == "" && a == checksum.CRC64NVME, opts.ChecksumType == "FULL_OBJECT" && a.IsCRC():
			u.ChecksumType = "FULL_OBJECT"
		case opts.ChecksumType == "" || opts.ChecksumType == "COMPOSITE" && a != checksum.CRC64NVME:
			u.ChecksumType = "COMPOSITE"
		default:
			return Upload{}, ErrInvalidChecksumType
		}
	}
	var id [24]byte
	rand.Read(id[:])
	u.ID = base64.RawURLEncoding.EncodeToString(id[:])
	headers, _ := json.Marshal(nonNil(opts.Headers))
	meta, _ := json.Marshal(nonNil(opts.UserMeta))
	err := s.tx(ctx, func(tx *sql.Tx) error {
		bid, err := bucketID(ctx, tx, bucket)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO uploads (id, bucket_id, key, created_at, content_type,
			headers, user_meta, checksum_algorithm, checksum_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.ID, bid, key, u.Created.UnixNano(), opts.ContentType, string(headers), string(meta),
			string(u.ChecksumAlgorithm), u.ChecksumType)
		return err
	})
	return u, err
}

// uploadRow is an upload with the metadata to apply on completion.
type uploadRow struct {
	Upload
	contentType   string
	headers, meta map[string]string
}

func loadUpload(ctx context.Context, q rowsQuerier, bucket, key, uploadID string) (uploadRow, error) {
	var u uploadRow
	var created int64
	var alg, headers, meta string
	err := q.QueryRowContext(ctx, `SELECT u.id, u.created_at, u.content_type, u.headers, u.user_meta,
		u.checksum_algorithm, u.checksum_type FROM uploads u JOIN buckets b ON b.id = u.bucket_id
		WHERE u.id = ? AND b.name = ? AND u.key = ?`, uploadID, bucket, key).
		Scan(&u.ID, &created, &u.contentType, &headers, &meta, &alg, &u.ChecksumType)
	if errors.Is(err, sql.ErrNoRows) {
		if _, berr := bucketID(ctx, q, bucket); berr != nil {
			return u, berr
		}
		return u, ErrNoSuchUpload
	}
	if err != nil {
		return u, err
	}
	u.Bucket, u.Key, u.Created = bucket, key, fromNanos(created)
	u.ChecksumAlgorithm = checksum.Algorithm(alg)
	if err := json.Unmarshal([]byte(headers), &u.headers); err != nil {
		return u, err
	}
	return u, json.Unmarshal([]byte(meta), &u.meta)
}

// PartOptions configures PutPart.
type PartOptions struct {
	Checksums []checksum.Algorithm // requested by the client, verified via Verify
	Verify    func(Sums) error
}

// PutPart stores part number n of an upload, replacing any previous upload
// of the same part.
func (s *Store) PutPart(ctx context.Context, bucket, key, uploadID string, n int, r io.Reader, size int64, opts PartOptions) (PartInfo, error) {
	u, err := loadUpload(ctx, s.read, bucket, key, uploadID)
	if err != nil {
		return PartInfo{}, err
	}
	algs := opts.Checksums
	if a := u.ChecksumAlgorithm; a != "" && !containsAlg(algs, a) {
		algs = append(algs, a)
	}
	blob, written, sums, err := s.writeBlob(r, size, algs)
	if err != nil {
		return PartInfo{}, err
	}
	if opts.Verify != nil {
		if err := opts.Verify(Sums(sums)); err != nil {
			s.removeBlob(blob)
			return PartInfo{}, err
		}
	}
	part := PartInfo{
		Number:    n,
		Size:      written,
		ETag:      hex.EncodeToString(sums.MD5),
		Checksums: map[checksum.Algorithm]string{},
		ModTime:   fromNanos(s.nowNanos()),
	}
	for a, v := range sums.Checksums {
		part.Checksums[a] = checksum.Encode(v)
	}
	var replaced []string
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := loadUpload(ctx, tx, bucket, key, uploadID); err != nil {
			return err // aborted or completed meanwhile
		}
		var old string
		err := tx.QueryRowContext(ctx, `DELETE FROM upload_parts WHERE upload_id = ? AND part_number = ? RETURNING blob`,
			uploadID, n).Scan(&old)
		if err == nil {
			replaced = []string{old}
			if err := queueBlobs(ctx, tx, replaced, s.nowNanos()); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO upload_parts (upload_id, part_number, blob, size, etag,
			sha256, checksum, modified_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uploadID, n, blob, part.Size, part.ETag, sums.SHA256, part.Checksums[u.ChecksumAlgorithm], part.ModTime.UnixNano())
		return err
	})
	if err != nil {
		s.removeBlob(blob)
		return PartInfo{}, err
	}
	s.collect(ctx, replaced...)
	return part, nil
}

// CopyPartOptions configures CopyPart.
type CopyPartOptions struct {
	SourceVersionID string                     // copy that version instead of the latest
	Check           func(src ObjectInfo) error // e.g. x-amz-copy-source-if-* conditions
	// Range selects bytes of the source; Length < 0 copies the whole object,
	// which is then verified against its SHA-256.
	Offset, Length int64
}

// CopyPart stores part n of an upload from (a range of) an existing object.
func (s *Store) CopyPart(ctx context.Context, srcBucket, srcKey, bucket, key, uploadID string, n int, opts CopyPartOptions) (PartInfo, error) {
	src, data, err := s.openSource(ctx, srcBucket, srcKey, opts.SourceVersionID)
	if err != nil {
		return PartInfo{}, err
	}
	defer data.Close()
	if opts.Check != nil {
		if err := opts.Check(src); err != nil {
			return PartInfo{}, err
		}
	}
	if opts.Length < 0 {
		return s.PutPart(ctx, bucket, key, uploadID, n, data.Verified(), src.Size, PartOptions{})
	}
	if opts.Offset < 0 || opts.Offset+opts.Length > src.Size {
		return PartInfo{}, ErrInvalidRange
	}
	return s.PutPart(ctx, bucket, key, uploadID, n, data.Range(opts.Offset, opts.Length), opts.Length, PartOptions{})
}

// ErrInvalidRange means a requested byte range lies outside the object.
var ErrInvalidRange = errors.New("range outside the object")

// ListParts returns up to max parts numbered above after, and whether more follow.
func (s *Store) ListParts(ctx context.Context, bucket, key, uploadID string, after, max int) (Upload, []PartInfo, bool, error) {
	u, err := loadUpload(ctx, s.read, bucket, key, uploadID)
	if err != nil {
		return Upload{}, nil, false, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT part_number, size, etag, checksum, modified_at FROM upload_parts
		WHERE upload_id = ? AND part_number > ? ORDER BY part_number LIMIT ?`, uploadID, after, max+1)
	if err != nil {
		return Upload{}, nil, false, err
	}
	defer rows.Close()
	var parts []PartInfo
	for rows.Next() {
		var p PartInfo
		var sum string
		var modified int64
		if err := rows.Scan(&p.Number, &p.Size, &p.ETag, &sum, &modified); err != nil {
			return Upload{}, nil, false, err
		}
		p.ModTime = fromNanos(modified)
		p.Checksums = map[checksum.Algorithm]string{}
		if sum != "" {
			p.Checksums[u.ChecksumAlgorithm] = sum
		}
		parts = append(parts, p)
	}
	truncated := len(parts) > max
	if truncated {
		parts = parts[:max]
	}
	return u.Upload, parts, truncated, rows.Err()
}

// UploadListOptions selects in-progress uploads, like ListOptions for objects.
type UploadListOptions struct {
	Prefix, Delimiter         string
	KeyMarker, UploadIDMarker string
	MaxUploads                int
}

// UploadList is one page of in-progress uploads.
type UploadList struct {
	Uploads                           []Upload
	CommonPrefixes                    []string
	IsTruncated                       bool
	NextKeyMarker, NextUploadIDMarker string
}

// ListUploads lists in-progress uploads ordered by key, then by start time.
// In-progress uploads are few, so they are filtered in memory.
func (s *Store) ListUploads(ctx context.Context, bucket string, opts UploadListOptions) (UploadList, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return UploadList{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT id, key, created_at, checksum_algorithm, checksum_type
		FROM uploads WHERE bucket_id = ? AND key >= ? ORDER BY key, created_at, id`, bid, opts.Prefix)
	if err != nil {
		return UploadList{}, err
	}
	defer rows.Close()
	var res UploadList
	seen := map[string]bool{}
	count := 0
	for rows.Next() {
		var u Upload
		var created int64
		var alg string
		if err := rows.Scan(&u.ID, &u.Key, &created, &alg, &u.ChecksumType); err != nil {
			return UploadList{}, err
		}
		if !strings.HasPrefix(u.Key, opts.Prefix) {
			break
		}
		u.Bucket, u.Created, u.ChecksumAlgorithm = bucket, fromNanos(created), checksum.Algorithm(alg)
		if opts.KeyMarker != "" && (u.Key < opts.KeyMarker ||
			u.Key == opts.KeyMarker && (opts.UploadIDMarker == "" || u.ID <= opts.UploadIDMarker)) {
			continue
		}
		if opts.Delimiter != "" {
			rest := u.Key[len(opts.Prefix):]
			if i := strings.Index(rest, opts.Delimiter); i >= 0 {
				cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
				if seen[cp] || cp <= opts.KeyMarker {
					continue
				}
				if count == opts.MaxUploads {
					res.IsTruncated = true
					break
				}
				seen[cp] = true
				res.CommonPrefixes = append(res.CommonPrefixes, cp)
				res.NextKeyMarker, res.NextUploadIDMarker = cp, ""
				count++
				continue
			}
		}
		if count == opts.MaxUploads {
			res.IsTruncated = true
			break
		}
		res.Uploads = append(res.Uploads, u)
		res.NextKeyMarker, res.NextUploadIDMarker = u.Key, u.ID
		count++
	}
	if !res.IsTruncated {
		res.NextKeyMarker, res.NextUploadIDMarker = "", ""
	}
	return res, rows.Err()
}

// AbortUpload discards an upload and its parts.
func (s *Store) AbortUpload(ctx context.Context, bucket, key, uploadID string) error {
	var dropped []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := loadUpload(ctx, tx, bucket, key, uploadID); err != nil {
			return err
		}
		var err error
		dropped, err = s.dropUpload(ctx, tx, uploadID)
		return err
	})
	if err != nil {
		return err
	}
	s.collect(ctx, dropped...)
	return nil
}

// dropUpload deletes an upload row (parts cascade) and queues the part blobs.
func (s *Store) dropUpload(ctx context.Context, tx *sql.Tx, uploadID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT blob FROM upload_parts WHERE upload_id = ?`, uploadID)
	if err != nil {
		return nil, err
	}
	var blobs []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
		blobs = append(blobs, b)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, uploadID); err != nil {
		return nil, err
	}
	return blobs, queueBlobs(ctx, tx, blobs, s.nowNanos())
}

// expireUploads aborts uploads started before cutoff and returns how many.
func (s *Store) expireUploads(ctx context.Context, cutoff time.Time) (int, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id FROM uploads WHERE created_at < ?`, cutoff.UnixNano())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		var dropped []string
		if err := s.tx(ctx, func(tx *sql.Tx) error {
			var err error
			dropped, err = s.dropUpload(ctx, tx, id)
			return err
		}); err != nil {
			return 0, err
		}
		s.collect(ctx, dropped...)
	}
	return len(ids), nil
}

// CompletePart is one entry of a CompleteMultipartUpload request.
type CompletePart struct {
	Number int
	ETag   string // quoted or not
	// Checksums sent for the part, if any; only the upload's algorithm is checked.
	Checksums map[checksum.Algorithm]string
}

// CompleteOptions configures CompleteUpload.
type CompleteOptions struct {
	Parts []CompletePart
	// ObjectSize, if >= 0, must equal the completed size (x-amz-mp-object-size).
	ObjectSize int64
	// FullChecksum, if set, must equal the computed object checksum.
	FullChecksum string
	IfMatch      string
	IfNoneMatch  string
}

// CompleteUpload assembles the listed parts into the object bucket/key. The
// parts' blobs become the object's data without being copied; parts that were
// uploaded but not listed are deleted.
func (s *Store) CompleteUpload(ctx context.Context, bucket, key, uploadID string, opts CompleteOptions) (ObjectInfo, error) {
	var info ObjectInfo
	var dropped []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		u, err := loadUpload(ctx, tx, bucket, key, uploadID)
		if err != nil {
			return err
		}
		stored, err := uploadParts(ctx, tx, uploadID)
		if err != nil {
			return err
		}
		info, err = assemble(u, stored, opts)
		if err != nil {
			return err
		}
		info.ModTime = fromNanos(s.nowNanos())

		bid, err := bucketID(ctx, tx, bucket)
		if err != nil {
			return err
		}
		versionID, replaced, err := s.beginWrite(ctx, tx, bid, key, opts.IfMatch, opts.IfNoneMatch)
		if err != nil {
			return err
		}
		info.VersionID = versionID
		if err := checkQuota(ctx, tx, bid, info.Size); err != nil {
			return err
		}
		dropped = replaced
		// Parts uploaded but not listed are discarded with the upload.
		used := map[int]bool{}
		for _, p := range info.parts {
			used[p.Number] = true
		}
		var unused []string
		for n, p := range stored {
			if !used[n] {
				unused = append(unused, p.Blob)
			}
		}
		if err := queueBlobs(ctx, tx, unused, s.nowNanos()); err != nil {
			return err
		}
		dropped = append(dropped, unused...)
		if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, uploadID); err != nil {
			return err
		}
		_, err = insertLatest(ctx, tx, bid, info)
		return err
	})
	if err != nil {
		return ObjectInfo{}, err
	}
	s.collect(ctx, dropped...)
	return info, nil
}

func uploadParts(ctx context.Context, tx *sql.Tx, uploadID string) (map[int]partData, error) {
	rows, err := tx.QueryContext(ctx, `SELECT part_number, blob, size, etag, sha256, checksum
		FROM upload_parts WHERE upload_id = ?`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := map[int]partData{}
	for rows.Next() {
		var p partData
		if err := rows.Scan(&p.Number, &p.Blob, &p.Size, &p.ETag, &p.SHA256, &p.Checksum); err != nil {
			return nil, err
		}
		parts[p.Number] = p
	}
	return parts, rows.Err()
}

// assemble validates the requested part list against the stored parts and
// computes the object's layout, ETag and checksum.
func assemble(u uploadRow, stored map[int]partData, opts CompleteOptions) (ObjectInfo, error) {
	if len(opts.Parts) == 0 {
		return ObjectInfo{}, ErrInvalidPart
	}
	info := ObjectInfo{
		Bucket:      u.Bucket,
		Key:         u.Key,
		IsLatest:    true,
		ContentType: u.contentType,
		Headers:     nonNil(u.headers),
		UserMeta:    nonNil(u.meta),
		Checksums:   map[checksum.Algorithm]string{},
		PartsCount:  len(opts.Parts),
	}
	md5s := md5.New()
	var full []byte    // full-object CRC, combined part by part
	var comp hash.Hash // composite checksum: digest of the part digests
	if u.ChecksumAlgorithm != "" && u.ChecksumType == "COMPOSITE" {
		comp = u.ChecksumAlgorithm.New()
	}

	// Order is checked over the whole list first, as S3 does.
	for i := 1; i < len(opts.Parts); i++ {
		if opts.Parts[i].Number <= opts.Parts[i-1].Number {
			return ObjectInfo{}, ErrInvalidPartOrder
		}
	}
	for i, cp := range opts.Parts {
		p, ok := stored[cp.Number]
		sent := cp.Checksums[u.ChecksumAlgorithm]
		if !ok || trimQuotes(cp.ETag) != p.ETag || (sent != "" && sent != p.Checksum) {
			return ObjectInfo{}, ErrInvalidPart
		}
		if i < len(opts.Parts)-1 && p.Size < MinPartSize {
			return ObjectInfo{}, ErrEntityTooSmall
		}
		raw, _ := hex.DecodeString(p.ETag)
		md5s.Write(raw)
		p.Offset = info.Size
		info.Size += p.Size
		info.parts = append(info.parts, p)

		if u.ChecksumAlgorithm == "" || p.Checksum == "" {
			continue
		}
		sum, err := base64.StdEncoding.DecodeString(p.Checksum)
		if err != nil {
			return ObjectInfo{}, err
		}
		switch {
		case comp != nil:
			comp.Write(sum)
		case full == nil:
			full = sum
		default:
			if full, err = checksum.Combine(u.ChecksumAlgorithm, full, sum, p.Size); err != nil {
				return ObjectInfo{}, err
			}
		}
	}
	if opts.ObjectSize >= 0 && opts.ObjectSize != info.Size {
		return ObjectInfo{}, ErrSizeMismatch
	}
	info.ETag = fmt.Sprintf("%s-%d", hex.EncodeToString(md5s.Sum(nil)), len(opts.Parts))

	if a := u.ChecksumAlgorithm; a != "" {
		var value string
		switch {
		case comp != nil:
			value = fmt.Sprintf("%s-%d", checksum.Encode(comp.Sum(nil)), len(opts.Parts))
		case full != nil:
			value = checksum.Encode(full)
		}
		if value != "" {
			if opts.FullChecksum != "" && opts.FullChecksum != value && opts.FullChecksum+fmt.Sprintf("-%d", len(opts.Parts)) != value {
				return ObjectInfo{}, ErrBadDigest
			}
			info.Checksums[a] = value
			info.ChecksumType = u.ChecksumType
		}
	}
	return info, nil
}
