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
}

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
	s := &Store{dir: dir, lock: lock, now: time.Now}
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
	go s.gcLoop(ctx)
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
	s.read.SetMaxOpenConns(8)
	if err := migrate(context.Background(), s.write); err != nil {
		return fmt.Errorf("migrating %s: %w", path, err)
	}
	return nil
}

// Close releases the database and the directory lock.
func (s *Store) Close() error {
	if s.stopGC != nil {
		s.stopGC()
	}
	var errs []error
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
	return tx.Commit()
}

// nowNanos is the current time as stored in the database.
func (s *Store) nowNanos() int64 { return s.now().UnixNano() }

func fromNanos(n int64) time.Time { return time.Unix(0, n).UTC() }
