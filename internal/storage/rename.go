package storage

import (
	"context"
	"database/sql"
	"errors"
)

// TrashPrefix is where the console's trash keeps deleted objects in each
// bucket, under their original path. It is never public (see package s3).
const TrashPrefix = ".trash/"

// ErrRenameVersioned is returned by RenameObjects for a bucket that has (or
// once had) versioning: there, a move must leave the old key's history in
// place, so callers copy and delete instead.
var ErrRenameVersioned = errors.New("objects of a versioned bucket cannot be renamed in place")

// Rename names an object's current and new key.
type Rename struct{ From, To string }

// renameBatch bounds how many renames share one transaction, so a large move
// never holds the write lock for long.
const renameBatch = 500

// RenameObjects gives objects of a never-versioned bucket new keys. Only
// metadata changes: blobs are immutable and stay where they are, so a move
// costs the same whatever the objects' size. An object already at a new key
// is replaced, as an overwrite would. Renamed objects take the current time
// as their modification time (the trash's retention goes by it).
//
// Sources that no longer exist are skipped. Renames apply in order, in
// batches; on error, the batches before it stay applied. It returns how many
// objects were renamed.
func (s *Store) RenameObjects(ctx context.Context, bucket string, renames []Rename) (int, error) {
	for _, r := range renames {
		if err := ValidateKey(r.To); err != nil {
			return 0, err
		}
	}
	total := 0
	for len(renames) > 0 {
		chunk := renames[:min(renameBatch, len(renames))]
		renames = renames[len(chunk):]
		var dropped []string
		n := 0
		err := s.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
			dropped, n = nil, 0 // a retried round starts over
			bid, err := bucketID(ctx, tx, bucket)
			if err != nil {
				return err
			}
			if v, err := bucketVersioning(ctx, tx, bid); err != nil {
				return err
			} else if v != "" {
				return ErrRenameVersioned
			}
			now := s.nowNanos()
			for _, r := range chunk {
				if r.From == r.To {
					continue
				}
				var id int64
				err := tx.QueryRowContext(ctx, `SELECT id FROM objects
					WHERE bucket_id = ? AND key = ? AND is_latest = 1 AND delete_marker = 0`, bid, r.From).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				d, err := dropVersion(ctx, tx, bid, r.To, NullVersion, now)
				if err != nil && !errors.Is(err, ErrNoSuchKey) {
					return err
				}
				dropped = append(dropped, d...)
				if _, err := tx.ExecContext(ctx, `UPDATE objects SET key = ?, modified_at = ? WHERE id = ?`,
					r.To, now, id); err != nil {
					return err
				}
				n++
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		s.collect(ctx, dropped...)
	}
	return total, nil
}
