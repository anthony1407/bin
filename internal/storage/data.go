package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"sync"
)

// ReadVerify decides when served reads check data against its SHA-256.
type ReadVerify int

const (
	// VerifyOnce checks a blob the first time it is read in full by this
	// process; later reads stream straight from disk (zero-copy). Blobs are
	// immutable, so a blob that passed stays trusted until restart, when it is
	// checked again.
	VerifyOnce ReadVerify = iota
	// VerifyAlways checks every whole-object read.
	VerifyAlways
	// VerifyOff never checks served reads. Copies, replication and the
	// integrity scan still do.
	VerifyOff
)

// ParseReadVerify accepts "once", "always" or "off".
func ParseReadVerify(s string) (ReadVerify, error) {
	switch s {
	case "once":
		return VerifyOnce, nil
	case "always":
		return VerifyAlways, nil
	case "off":
		return VerifyOff, nil
	}
	return 0, fmt.Errorf("must be once, always or off, got %q", s)
}

// SetReadVerify sets the policy Reader applies. Call it before serving.
func (s *Store) SetReadVerify(v ReadVerify) { s.readVerify = v }

// verifiedSet remembers blobs that passed verification, up to a bound; when
// full it starts over, which only means some blobs get checked again.
type verifiedSet struct {
	mu sync.RWMutex
	m  map[string]struct{}
}

const maxVerified = 1 << 20

func (v *verifiedSet) has(blob string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.m[blob]
	return ok
}

func (v *verifiedSet) add(blob string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.m == nil || len(v.m) >= maxVerified {
		v.m = map[string]struct{}{}
	}
	v.m[blob] = struct{}{}
}

// ObjectData gives access to an object's bytes. While it is open, the blobs
// it reads from are protected from garbage collection, even if the object is
// overwritten or deleted meanwhile. Close it when done.
type ObjectData struct {
	s     *Store
	parts []partData

	mem []byte // content of a single-blob object found in the read cache

	mu     sync.Mutex
	first  *os.File // the first blob, opened by OpenVersion; handed to one reader
	open   []*os.File
	closed bool
}

// OpenObject returns the metadata of the current version of bucket/key and
// access to its data.
func (s *Store) OpenObject(ctx context.Context, bucket, key string) (ObjectInfo, *ObjectData, error) {
	info, data, err := s.OpenVersion(ctx, bucket, key, "")
	if errors.Is(err, ErrDeleteMarker) {
		return ObjectInfo{}, nil, ErrNoSuchKey
	}
	return info, data, err
}

// OpenVersion returns the metadata of one version of bucket/key (the latest
// when versionID is "") and access to its data. A delete marker is returned
// with ErrDeleteMarker and no data.
func (s *Store) OpenVersion(ctx context.Context, bucket, key, versionID string) (ObjectInfo, *ObjectData, error) {
	// A concurrent overwrite can delete the blobs between reading the
	// metadata and reserving them; then a fresh lookup finds the new ones.
	for attempt := 0; ; attempt++ {
		info, err := s.StatVersion(ctx, bucket, key, versionID)
		if err != nil {
			return info, nil, err
		}
		parts := info.dataParts()
		blobs := make([]string, len(parts))
		for i, p := range parts {
			blobs[i] = p.Blob
		}
		s.acquire(blobs)
		if len(parts) == 1 && s.readVerify != VerifyAlways {
			// removeBlob evicts before deleting, and the blob is reserved
			// now: a hit cannot vanish.
			if mem, ok := s.hot.get(blobs[0]); ok {
				return info, &ObjectData{s: s, parts: parts, mem: mem}, nil
			}
		}
		// Opening the first blob proves it exists and is the descriptor the
		// first read uses: one system call instead of a stat, an open and a
		// seek. Further parts of multipart objects are only checked.
		first, err := os.Open(s.blobPath(blobs[0]))
		missing := err != nil
		for _, b := range blobs[1:] {
			if missing {
				break
			}
			if _, err := os.Stat(s.blobPath(b)); err != nil {
				missing = true
			}
		}
		if !missing {
			return info, &ObjectData{s: s, parts: parts, first: first, open: []*os.File{first}}, nil
		}
		if first != nil {
			first.Close()
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
	d.mem = nil
	blobs := make([]string, len(d.parts))
	for i, p := range d.parts {
		blobs[i] = p.Blob
	}
	d.s.release(blobs)
	return nil
}

// Range returns n bytes starting at off, without integrity verification.
func (d *ObjectData) Range(off, n int64) io.Reader {
	if d.mem != nil {
		return bytes.NewReader(d.mem[off : off+n])
	}
	var readers []io.Reader
	for i, p := range d.parts {
		start, end := p.Offset, p.Offset+p.Size
		if n <= 0 || off >= end {
			continue
		}
		if off < start {
			break
		}
		take := min(end-off, n)
		readers = append(readers, d.file(i, off-start, take))
		off += take
		n -= take
	}
	return multi(readers)
}

// Verified returns the whole object, checking each part's SHA-256 as it is
// read. See NewVerifiedReader for what a corrupt part looks like to readers.
func (d *ObjectData) Verified() io.Reader {
	readers := make([]io.Reader, len(d.parts))
	for i, p := range d.parts {
		readers[i] = NewVerifiedReader(d.file(i, 0, p.Size), p.SHA256)
	}
	return multi(readers)
}

// Reader returns the whole object for serving to a client, verified as the
// store's ReadVerify policy says. Parts that need no check are streamed with
// zero-copy when the destination supports it.
func (d *ObjectData) Reader() io.Reader {
	if d.mem != nil {
		return bytes.NewReader(d.mem)
	}
	if p := d.parts[0]; len(d.parts) == 1 && p.Size <= MaxCachedBlob && d.s.hot != nil && d.s.readVerify != VerifyAlways {
		return d.load(p)
	}
	readers := make([]io.Reader, len(d.parts))
	for i, p := range d.parts {
		f := d.file(i, 0, p.Size)
		switch {
		case d.s.readVerify == VerifyOff || (d.s.readVerify == VerifyOnce && d.s.verified.has(p.Blob)):
			readers[i] = f
		case d.s.readVerify == VerifyOnce:
			v := newVerifiedReader(f, p.SHA256)
			v.onValid = func() { d.s.verified.add(p.Blob) }
			readers[i] = v
		default:
			readers[i] = NewVerifiedReader(f, p.SHA256)
		}
	}
	return multi(readers)
}

// load reads a small blob into memory in one go, checks it as the policy
// says, and keeps it in the read cache for the next readers.
func (d *ObjectData) load(p partData) io.Reader {
	buf := make([]byte, p.Size)
	if _, err := io.ReadFull(d.file(0, 0, p.Size), buf); err != nil {
		return errReader{err}
	}
	if d.s.readVerify == VerifyOnce && !d.s.verified.has(p.Blob) {
		if sum := sha256.Sum256(buf); !bytes.Equal(sum[:], p.SHA256) {
			return errReader{ErrCorrupt}
		}
		d.s.verified.add(p.Blob)
	}
	d.s.hot.put(p.Blob, buf)
	return bytes.NewReader(buf)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// multi avoids the MultiReader wrapper for the usual single-part object, so
// the destination sees the blob reader itself.
func multi(readers []io.Reader) io.Reader {
	if len(readers) == 1 {
		return readers[0]
	}
	return io.MultiReader(readers...)
}

// file returns a reader of n bytes at off in part i.
func (d *ObjectData) file(i int, off, n int64) *lazyFile {
	l := &lazyFile{d: d, path: d.s.blobPath(d.parts[i].Blob), off: off, n: n}
	if i == 0 {
		d.mu.Lock()
		l.f, d.first = d.first, nil
		d.mu.Unlock()
	}
	return l
}

// lazyFile reads n bytes at off from a blob, opening it on first use and
// closing it at the end, so objects with thousands of parts never hold
// thousands of descriptors.
type lazyFile struct {
	d      *ObjectData
	path   string
	off, n int64
	f      *os.File // opened already (the first part), or by start
	r      *io.LimitedReader
}

func (l *lazyFile) start() error {
	if l.f == nil {
		f, err := os.Open(l.path)
		if err != nil {
			return err
		}
		l.d.mu.Lock()
		l.d.open = append(l.d.open, f)
		l.d.mu.Unlock()
		l.f = f
	}
	if l.off != 0 {
		if _, err := l.f.Seek(l.off, io.SeekStart); err != nil {
			return err
		}
	}
	l.r = &io.LimitedReader{R: l.f, N: l.n}
	return nil
}

func (l *lazyFile) Read(p []byte) (int, error) {
	if l.r == nil {
		if err := l.start(); err != nil {
			return 0, err
		}
	}
	n, err := l.r.Read(p)
	if err == io.EOF {
		l.f.Close()
	}
	return n, err
}

// WriteTo hands the destination an *io.LimitedReader over the file itself,
// which net/http turns into sendfile/TransmitFile: the bytes go from the page
// cache to the socket without passing through user space.
func (l *lazyFile) WriteTo(w io.Writer) (int64, error) {
	if l.r == nil {
		if err := l.start(); err != nil {
			return 0, err
		}
	}
	n, err := io.Copy(w, l.r)
	if err == nil && l.r.N > 0 {
		err = io.ErrUnexpectedEOF // the blob is shorter than its metadata says
	}
	l.f.Close()
	return n, err
}

// --- integrity ---------------------------------------------------------------

// holdback is how much verified-but-unreleased data NewVerifiedReader keeps
// until the whole blob has been checked.
const holdback = 64 << 10

// readChunk is the size of each read a verifiedReader makes.
const readChunk = 128 << 10

// verifyBufSize fits the holdback plus a read and a typical caller's buffer,
// so the buffer is reused for the whole blob instead of regrown per read.
const verifyBufSize = holdback + 2*readChunk

var verifyBufPool = sync.Pool{New: func() any { b := make([]byte, verifyBufSize); return &b }}

// verifiedReader hashes data as it reads and withholds the last holdback
// bytes until EOF, so a reader of corrupt data never receives the complete
// content: it gets ErrCorrupt before the final bytes instead.
type verifiedReader struct {
	r       io.Reader
	h       hash.Hash
	want    []byte
	mem     *[]byte // backing array of buf; pooled when it has the standard size
	buf     []byte  // read and hashed, not yet released
	eof     bool
	err     error
	onValid func() // called once the content matched
}

// NewVerifiedReader wraps r, whose full content must hash to sha256sum.
func NewVerifiedReader(r io.Reader, sha256sum []byte) io.Reader {
	return newVerifiedReader(r, sha256sum)
}

func newVerifiedReader(r io.Reader, sha256sum []byte) *verifiedReader {
	return &verifiedReader{r: r, h: sha256.New(), want: sha256sum}
}

// reserve makes room for n more bytes at the end of buf, sliding the
// unreleased bytes to the front of the backing array when that is enough.
func (v *verifiedReader) reserve(n int) {
	if cap(v.buf)-len(v.buf) >= n {
		return
	}
	if v.mem == nil {
		v.mem = verifyBufPool.Get().(*[]byte)
	}
	if mem := *v.mem; len(v.buf)+n > len(mem) {
		grown := make([]byte, 2*(len(v.buf)+n))
		kept := copy(grown, v.buf)
		v.free() // only after the copy: the pool may hand it out at once
		v.mem, v.buf = &grown, grown[:kept]
		return
	}
	mem := *v.mem
	v.buf = mem[:copy(mem, v.buf)]
}

// free returns a standard-size buffer to the pool once nothing refers to it.
func (v *verifiedReader) free() {
	if v.mem != nil && len(*v.mem) == verifyBufSize {
		verifyBufPool.Put(v.mem)
	}
	v.mem = nil
}

func (v *verifiedReader) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	for !v.eof && len(v.buf) <= holdback+len(p) {
		v.reserve(readChunk)
		end := len(v.buf)
		n, err := v.r.Read(v.buf[end : end+readChunk])
		v.h.Write(v.buf[end : end+n])
		v.buf = v.buf[:end+n]
		if err == io.EOF {
			v.eof = true
			if !bytes.Equal(v.h.Sum(nil), v.want) {
				v.err = ErrCorrupt
				v.buf = nil
				v.free()
				return 0, v.err
			}
			if v.onValid != nil {
				v.onValid()
			}
		} else if err != nil {
			v.err = err
			v.buf = nil
			v.free()
			return 0, err
		}
	}
	avail := len(v.buf)
	if !v.eof {
		avail -= holdback
	}
	if avail <= 0 {
		if v.eof {
			v.buf = nil
			v.free()
			return 0, io.EOF
		}
		return 0, nil
	}
	n := copy(p, v.buf[:avail])
	v.buf = v.buf[n:]
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

// release ends the protection and collects the blobs whose removal collect
// had to put off because of readers. Other blobs are live: releasing them
// costs no database lookup.
func (s *Store) release(blobs []string) {
	var free []string
	s.mu.Lock()
	for _, b := range blobs {
		if s.inUse[b]--; s.inUse[b] <= 0 {
			delete(s.inUse, b)
			if s.deferred[b] {
				delete(s.deferred, b)
				free = append(free, b)
			}
		}
	}
	s.mu.Unlock()
	if len(free) > 0 {
		s.collect(context.Background(), free...)
	}
}
