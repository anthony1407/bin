package storage

import (
	"context"
	"strings"
	"time"
)

// PrefixUsage sums the current objects under a prefix (a folder).
type PrefixUsage struct {
	Objects  int64
	Bytes    int64
	Modified time.Time // latest change of any object under it; zero when empty
}

// PrefixUsage returns the usage of each prefix, read as a key range so a
// folder costs one index scan of what it holds.
func (s *Store) PrefixUsage(ctx context.Context, bucket string, prefixes []string) (map[string]PrefixUsage, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return nil, err
	}
	out := make(map[string]PrefixUsage, len(prefixes))
	for _, p := range prefixes {
		var u PrefixUsage
		var modified int64
		if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0), COALESCE(MAX(modified_at), 0) FROM objects
			WHERE bucket_id = ? AND is_latest = 1 AND delete_marker = 0 AND key >= ? AND key < ?`,
			bid, p, p+maxRune).Scan(&u.Objects, &u.Bytes, &modified); err != nil {
			return nil, err
		}
		if modified > 0 {
			u.Modified = fromNanos(modified)
		}
		out[p] = u
	}
	return out, nil
}

// ListOptions selects a page of a bucket listing.
type ListOptions struct {
	Prefix    string // only keys starting with Prefix
	Delimiter string // group keys sharing Prefix + ... + Delimiter into common prefixes
	After     string // only entries (keys or common prefixes) greater than After
	MaxKeys   int    // keys plus common prefixes per page
	// Deleted lists the keys whose latest version is a delete marker instead
	// of the current objects.
	Deleted bool
}

// ListResult is one page of a listing, in key order.
type ListResult struct {
	Objects        []ObjectInfo // Key, VersionID, Size, ETag and ModTime are set
	CommonPrefixes []string
	IsTruncated    bool
	// NextMarker is the last entry returned (key or common prefix); pass it
	// as After to get the next page.
	NextMarker string
}

// maxRune is the greatest valid code point. A key that starts with p and is
// followed by anything other than maxRune sorts below p+maxRune, so it is
// where a scan can jump to skip everything under a common prefix.
const maxRune = "\U0010FFFF"

// listBatch is how many rows one query reads before the scan re-queries.
// Tests lower it to exercise batch boundaries.
var listBatch = 1000

// ListObjects lists the current objects of a bucket. Keys compare as bytes
// (SQLite's BINARY collation), which is S3's UTF-8 binary order.
func (s *Store) ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error) {
	bid, err := bucketID(ctx, s.read, bucket)
	if err != nil {
		return ListResult{}, err
	}
	var res ListResult
	if opts.MaxKeys <= 0 {
		return res, nil
	}

	// The scan resumes from a position: either "> pos" or ">= pos".
	pos, inclusive := opts.Prefix, true
	if opts.After >= opts.Prefix {
		pos, inclusive = opts.After, false
	}
	lastPrefix := ""
	count := 0
	for {
		op := ">"
		if inclusive {
			op = ">="
		}
		rows, err := s.read.QueryContext(ctx, `SELECT key, version_id, size, etag, modified_at FROM objects
			WHERE bucket_id = ? AND is_latest = 1 AND delete_marker = ? AND key `+op+` ?
			ORDER BY key LIMIT ?`, bid, opts.Deleted, pos, listBatch)
		if err != nil {
			return ListResult{}, err
		}

		read, jump, done := 0, "", false
		for rows.Next() {
			var o ObjectInfo
			var modified int64
			if err := rows.Scan(&o.Key, &o.VersionID, &o.Size, &o.ETag, &modified); err != nil {
				rows.Close()
				return ListResult{}, err
			}
			read++
			pos, inclusive = o.Key, false
			if !strings.HasPrefix(o.Key, opts.Prefix) {
				done = true // sorted: nothing after this can match the prefix
				break
			}
			if opts.Delimiter != "" {
				rest := o.Key[len(opts.Prefix):]
				if i := strings.Index(rest, opts.Delimiter); i >= 0 {
					cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
					if cp == lastPrefix {
						continue // keys beyond the jump target that still belong to it
					}
					lastPrefix = cp
					if cp > opts.After { // a prefix equal to the resume point was already returned
						if count == opts.MaxKeys {
							res.IsTruncated, done = true, true
							break
						}
						res.CommonPrefixes = append(res.CommonPrefixes, cp)
						res.NextMarker = cp
						count++
					}
					jump = cp + maxRune // skip the rest of this common prefix
					break
				}
			}
			if count == opts.MaxKeys {
				res.IsTruncated, done = true, true
				break
			}
			o.Bucket = bucket
			o.ModTime = fromNanos(modified)
			res.Objects = append(res.Objects, o)
			res.NextMarker = o.Key
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return ListResult{}, err
		}
		if done || (read < listBatch && jump == "") {
			break
		}
		if jump != "" {
			pos, inclusive = jump, true
		}
	}
	if !res.IsTruncated {
		res.NextMarker = ""
	}
	return res, nil
}
