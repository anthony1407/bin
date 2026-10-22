package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"os"
	"sync"
)

// ObjectData gives access to an object's bytes. While it is open, the blobs
// it reads from are protected from garbage collection, even if the object is
// overwritten or deleted meanwhile. Close it when done.
type ObjectData struct {
	s     *Store
	parts []partData

	mu     sync.Mutex
	open   []*os.File
	closed bool
}

// OpenObject returns the metadata of bucket/key and access to its data.
func (s *Store) OpenObject(ctx context.Context, bucket, key string) (ObjectInfo, *ObjectData, error) {
	// A concurrent overwrite can delete the blobs between reading the
	// metadata and reserving them; then a fresh lookup finds the new ones.
	for attempt := 0; ; attempt++ {
		info, err := s.StatObject(ctx, bucket, key)
		if err != nil {
			return ObjectInfo{}, nil, err
		}
		parts := info.dataParts()
		blobs := make([]string, len(parts))
		for i, p := range parts {
			blobs[i] = p.Blob
		}
		s.acquire(blobs)
		missing := false
		for _, b := range blobs {
			if _, err := os.Stat(s.blobPath(b)); err != nil {
				missing = true
				break
			}
		}
		if !missing {
			return info, &ObjectData{s: s, parts: parts}, nil
		}
		s.release(blobs)
		if attempt == 2 {
			return ObjectInfo{}, nil, errors.New("object data vanished while being opened")
		}
	}
}

// Close releases the open files and the blobs' protection.
func (d *ObjectData) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	for _, f := range d.open {
		f.Close()
	}
	blobs := make([]string, len(d.parts))
	for i, p := range d.parts {
		blobs[i] = p.Blob
	}
	d.s.release(blobs)
	return nil
}

// Range returns n bytes starting at off, without integrity verification.
func (d *ObjectData) Range(off, n int64) io.Reader {
	var readers []io.Reader
	for _, p := range d.parts {
		start, end := p.Offset, p.Offset+p.Size
		if n <= 0 || off >= end {
			continue
		}
		if off < start {
			break
		}
		take := min(end-off, n)
		readers = append(readers, &lazyFile{d: d, path: d.s.blobPath(p.Blob), off: off - start, n: take})
		off += take
		n -= take
	}
	return io.MultiReader(readers...)
}

// Verified returns the whole object, checking each part's SHA-256 as it is
// read. See NewVerifiedReader for what a corrupt part looks like to readers.
func (d *ObjectData) Verified() io.Reader {
	readers := make([]io.Reader, len(d.parts))
	for i, p := range d.parts {
		readers[i] = NewVerifiedReader(&lazyFile{d: d, path: d.s.blobPath(p.Blob), n: p.Size}, p.SHA256)
	}
	return io.MultiReader(readers...)
}

// lazyFile reads n bytes at off from a blob, opening it on first use and
// closing it at the end, so objects with thousands of parts never hold
// thousands of descriptors.
type lazyFile struct {
	d      *ObjectData
	path   string
	off, n int64
	f      *os.File
	r      io.Reader
}

func (l *lazyFile) Read(p []byte) (int, error) {
	if l.r == nil {
		f, err := os.Open(l.path)
		if err != nil {
			return 0, err
		}
		if _, err := f.Seek(l.off, io.SeekStart); err != nil {
			f.Close()
			return 0, err
		}
		l.d.mu.Lock()
		l.d.open = append(l.d.open, f)
		l.d.mu.Unlock()
		l.f, l.r = f, io.LimitReader(f, l.n)
	}
	n, err := l.r.Read(p)
	if err == io.EOF {
		l.f.Close()
	}
	return n, err
}

// --- integrity ---------------------------------------------------------------

// holdback is how much verified-but-unreleased data NewVerifiedReader keeps
// until the whole blob has been checked.
const holdback = 64 << 10

// verifiedReader hashes data as it reads and withholds the last holdback
// bytes until EOF, so a reader of corrupt data never receives the complete
// content: it gets ErrCorrupt before the final bytes instead.
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

// --- blob reservations -----------------------------------------------------------

// acquire protects blobs from garbage collection while they are being read.
func (s *Store) acquire(blobs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range blobs {
		s.inUse[b]++
	}
}

// release ends the protection and collects blobs that were queued meanwhile.
func (s *Store) release(blobs []string) {
	var free []string
	s.mu.Lock()
	for _, b := range blobs {
		if s.inUse[b]--; s.inUse[b] <= 0 {
			delete(s.inUse, b)
			free = append(free, b)
		}
	}
	s.mu.Unlock()
	s.collect(context.Background(), free...)
}
