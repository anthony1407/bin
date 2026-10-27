// Package storage is bin's storage core: buckets, objects and multipart
// uploads on top of a data directory. It knows nothing about HTTP.
//
// Layout of the data directory:
//
//	.bin/lock      exclusive lock held while a process owns the directory
//	.bin/meta.db   SQLite metadata (WAL mode, synchronous=FULL)
//	.bin/tmp/      in-flight writes, renamed into blobs/ once durable
//	blobs/         object data, addressed by opaque blob IDs
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Errors returned by Store methods. Callers map them to their own API errors.
var (
	ErrInvalidBucketName = errors.New("invalid bucket name")
	ErrBucketExists      = errors.New("bucket already exists")
	ErrNoSuchBucket      = errors.New("no such bucket")
	ErrBucketNotEmpty    = errors.New("bucket not empty")
	ErrNoTags            = errors.New("no tags")
	ErrDirLocked         = errors.New("data directory is in use by another process")
	ErrNoSuchKey         = errors.New("no such key")
	ErrKeyTooLong        = errors.New("key too long")
	ErrInvalidKey        = errors.New("invalid key")
	ErrPrecondition      = errors.New("precondition failed")
	ErrIncompleteBody    = errors.New("incomplete body")
	// ErrCorrupt means stored data no longer matches its SHA-256.
	ErrCorrupt = errors.New("stored data is corrupt")
)

// Store is an open data directory.
type Store struct {
	dir    string
	lock   *os.File
	write  *sql.DB // single connection: SQLite allows one writer at a time
	read   *sql.DB
	now    func() time.Time
	stopGC context.CancelFunc

	mu       sync.Mutex
	inUse    map[string]int  // blobs being read; never deleted meanwhile
	deferred map[string]bool // queued blobs whose removal waits for readers

	// Read fast path; see cache.go.
	epoch       atomic.Uint64
	objects     *EpochCache[objectCacheKey, ObjectInfo]
	buckets     *EpochCache[string, Bucket]
	revoked     *EpochCache[string, bool]
	stmtLatest  *sql.Stmt
	stmtVersion *sql.Stmt

	readVerify ReadVerify
	verified   verifiedSet
	hot        *blobCache // small blobs in memory; nil: disabled
}

// DefaultReadCache is how much memory small blobs may use by default.
const DefaultReadCache = 128 << 20

type objectCacheKey struct{ bucket, key, versionID string }

// Open takes exclusive ownership of dir, creating it if needed, brings the
// metadata schema up to date and repairs anything an unclean shutdown left.
func Open(dir string, log *slog.Logger) (*Store, error) {
	for _, d := range []string{filepath.Join(dir, ".bin", "tmp"), filepath.Join(dir, "blobs")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, err
		}
	}
	// .bin holds the metadata database (and, later, encrypted secrets): only
	// the service user may enter it, whatever the umask made of the files.
	if err := os.Chmod(filepath.Join(dir, ".bin"), 0o700); err != nil {
		return nil, err
	}
	lock, err := lockDir(filepath.Join(dir, ".bin", "lock"))
	if err != nil {
		return nil, err
	}
	s := &Store{
		dir: dir, lock: lock, now: time.Now,
		inUse:    map[string]int{},
		deferred: map[string]bool{},
		objects:  NewEpochCache[objectCacheKey, ObjectInfo](64 << 10),
		buckets:  NewEpochCache[string, Bucket](4 << 10),
		revoked:  NewEpochCache[string, bool](16 << 10),
		hot:      newBlobCache(DefaultReadCache),
	}
	if err := s.openDB(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.recover(context.Background(), log); err != nil {
		s.Close()
		return nil, fmt.Errorf("recovering data directory: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stopGC = cancel
	go s.gcLoop(ctx, log)
	return s, nil
}

func (s *Store) openDB() error {
	path := filepath.Join(s.dir, ".bin", "meta.db")
	dsn := "file:" + filepath.ToSlash(path) + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"synchronous(FULL)", // every commit is on disk before it returns
			"foreign_keys(ON)",
			"busy_timeout(10000)",
		},
		"_txlock": {"immediate"}, // writers take the lock up front: no upgrade deadlocks
	}.Encode()

	var err error
	if s.write, err = sql.Open("sqlite", dsn); err != nil {
		return err
	}
	s.write.SetMaxOpenConns(1)
	if s.read, err = sql.Open("sqlite", dsn); err != nil {
		return err
	}
	// Readers never block each other in WAL mode: size the pool for the
	// machine, and keep every connection open, since opening one (and
	// re-reading the schema) costs far more than the queries it serves.
	readers := max(8, 2*runtime.GOMAXPROCS(0))
	s.read.SetMaxOpenConns(readers)
	s.read.SetMaxIdleConns(readers)
	if err := migrate(context.Background(), s.write); err != nil {
		return fmt.Errorf("migrating %s: %w", path, err)
	}
	// Object lookups are prepared once per connection instead of being
	// parsed on every request.
	ctx := context.Background()
	if s.stmtLatest, err = s.read.PrepareContext(ctx, `SELECT `+objectColumnsQualified+`
		FROM objects o JOIN buckets b ON b.id = o.bucket_id
		WHERE b.name = ? AND o.key = ? AND o.is_latest = 1`); err != nil {
		return err
	}
	if s.stmtVersion, err = s.read.PrepareContext(ctx, `SELECT `+objectColumnsQualified+`
		FROM objects o JOIN buckets b ON b.id = o.bucket_id
		WHERE b.name = ? AND o.key = ? AND o.version_id = ?`); err != nil {
		return err
	}
	return nil
}

// Close releases the database and the directory lock.
func (s *Store) Close() error {
	if s.stopGC != nil {
		s.stopGC()
	}
	var errs []error
	for _, st := range []*sql.Stmt{s.stmtLatest, s.stmtVersion} {
		if st != nil {
			errs = append(errs, st.Close())
		}
	}
	for _, db := range []*sql.DB{s.read, s.write} {
		if db != nil {
			errs = append(errs, db.Close())
		}
	}
	if s.lock != nil {
		errs = append(errs, unlockDir(s.lock))
	}
	return errors.Join(errs...)
}

// tx runs fn in a write transaction.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	err = tx.Commit()
	s.bump() // even after a failed commit: whether it landed is unknown
	return err
}

// exec runs a single write statement outside a transaction.
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := s.write.ExecContext(ctx, query, args...)
	s.bump()
	return res, err
}

// nowNanos is the current time as stored in the database.
func (s *Store) nowNanos() int64 { return s.now().UnixNano() }

func fromNanos(n int64) time.Time { return time.Unix(0, n).UTC() }
