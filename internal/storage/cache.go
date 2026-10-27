package storage

import "sync"

// Hot read paths (object lookups, buckets, access keys) are served from
// in-memory caches. Every committed write to the database advances the
// store's epoch, which empties them all: the caches never need to know
// which rows a write touched, and a cached value is never older than the
// last write.
//
// Ordering makes this safe without locking readers against writers: a reader
// takes the epoch before querying, and a writer advances it only after its
// commit. Whatever a reader caches under epoch e was read no earlier than e
// began; if a commit landed meanwhile, the epoch has moved on and the entry
// is never served.

// Epoch returns the current write epoch. Callers caching data derived from
// the store take it before reading and discard their entries once it changes.
func (s *Store) Epoch() uint64 { return s.epoch.Load() }

// bump advances the epoch. Call it after a write commits, never before.
func (s *Store) bump() { s.epoch.Add(1) }

// EpochCache is a bounded map whose entries are valid for one epoch only.
type EpochCache[K comparable, V any] struct {
	mu    sync.RWMutex
	epoch uint64
	max   int
	m     map[K]V
}

// NewEpochCache returns a cache holding at most max entries; when full, it
// starts over rather than tracking recency, which keeps hits lock-cheap.
func NewEpochCache[K comparable, V any](max int) *EpochCache[K, V] {
	return &EpochCache[K, V]{max: max, m: map[K]V{}}
}

// Get returns the value cached for k during epoch.
func (c *EpochCache[K, V]) Get(k K, epoch uint64) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.epoch != epoch {
		var zero V
		return zero, false
	}
	v, ok := c.m[k]
	return v, ok
}

// Put caches v for k, read during epoch. Values from an epoch older than the
// cache's are dropped; a newer epoch empties the cache first.
func (c *EpochCache[K, V]) Put(k K, v V, epoch uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case epoch < c.epoch:
		return
	case epoch > c.epoch || len(c.m) >= c.max:
		c.epoch = epoch
		clear(c.m)
	}
	c.m[k] = v
}
