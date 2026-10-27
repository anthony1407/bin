package storage

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"bin/internal/checksum"
)

// blobSums are the digests computed while a blob is written.
type blobSums struct {
	MD5       []byte
	SHA256    []byte
	Checksums map[checksum.Algorithm][]byte
}

// newBlobID returns a random 128-bit identifier in hex.
func newBlobID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// blobPath spreads blobs over 65536 directories: blobs/ab/cd/abcd....
func (s *Store) blobPath(id string) string {
	return filepath.Join(s.dir, "blobs", id[0:2], id[2:4], id)
}

// writeBlob streams r into a new blob and makes it durable:
//
//  1. write to .bin/tmp/<id> while hashing (MD5, SHA-256, requested checksums)
//  2. fsync the file
//  3. rename into blobs/ab/cd/<id> (atomic on the same file system)
//  4. fsync the destination directory so the rename itself survives a crash
//
// If size >= 0, exactly size bytes must be read. On any error the temporary
// file is removed. The returned blob is not referenced by metadata yet: the
// caller commits it, or deletes it with removeBlob.
func (s *Store) writeBlob(r io.Reader, size int64, algs []checksum.Algorithm) (id string, n int64, sums blobSums, err error) {
	id = newBlobID()
	tmp := filepath.Join(s.dir, ".bin", "tmp", id)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return "", 0, sums, err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()

	md5h, sha := md5.New(), sha256.New()
	writers := []io.Writer{f, md5h, sha}
	extra := map[checksum.Algorithm]hash.Hash{}
	for _, a := range algs {
		if a == checksum.SHA256 {
			continue // shared with the integrity hash
		}
		h := a.New()
		extra[a] = h
		writers = append(writers, h)
	}

	src := r
	if size >= 0 {
		src = io.LimitReader(r, size+1) // one extra byte detects oversized bodies
	}
	n, err = io.CopyBuffer(io.MultiWriter(writers...), src, make([]byte, 256<<10))
	if err != nil {
		return "", 0, sums, err
	}
	if size >= 0 && n != size {
		return "", 0, sums, fmt.Errorf("%w: read %d of %d bytes", ErrIncompleteBody, n, size)
	}
	if err = f.Sync(); err != nil {
		return "", 0, sums, err
	}
	if err = f.Close(); err != nil {
		return "", 0, sums, err
	}

	dst := s.blobPath(id)
	if err = s.mkdirDurable(filepath.Dir(dst)); err != nil {
		return "", 0, sums, err
	}
	if err = os.Rename(tmp, dst); err != nil {
		return "", 0, sums, err
	}
	if err = syncDir(filepath.Dir(dst)); err != nil {
		os.Remove(dst)
		return "", 0, sums, err
	}

	sums = blobSums{MD5: md5h.Sum(nil), SHA256: sha.Sum(nil), Checksums: map[checksum.Algorithm][]byte{}}
	for a, h := range extra {
		sums.Checksums[a] = h.Sum(nil)
	}
	for _, a := range algs {
		if a == checksum.SHA256 {
			sums.Checksums[a] = sums.SHA256
		}
	}
	return id, n, sums, nil
}

// removeBlob deletes a blob file. A missing file is not an error.
func (s *Store) removeBlob(id string) error {
	s.hot.evict(id)
	err := os.Remove(s.blobPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// mkdirDurable creates dir (and missing parents) and fsyncs each parent whose
// entries changed, so new directories survive a crash.
func (s *Store) mkdirDurable(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	parent := filepath.Dir(dir)
	if err := s.mkdirDurable(parent); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDir(parent)
}

// syncDir fsyncs a directory so renames and creations inside it are durable.
// Windows cannot fsync directories; NTFS journals metadata itself.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
