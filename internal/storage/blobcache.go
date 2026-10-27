package storage

import (
	"container/list"
	"sync"
)

// MaxCachedBlob is the largest blob kept in the read cache. Bigger objects
// are streamed from disk with zero-copy, which already moves them at wire
// speed; for small ones, opening and closing the file is most of the cost.
const MaxCachedBlob = 256 << 10

// blobCache keeps the content of small, recently read blobs in memory, least
// recently used out first. Blobs never change once written, so an entry can
// only become wrong by its blob being deleted, and removeBlob evicts it.
type blobCache struct {
	mu    sync.Mutex
	max   int64
	size  int64
	lru   *list.List // of *cachedBlob, most recent at the front
	items map[string]*list.Element
}

type cachedBlob struct {
	id   string
	data []byte
}

func newBlobCache(max int64) *blobCache {
	return &blobCache{max: max, lru: list.New(), items: map[string]*list.Element{}}
}

// get returns a blob's content. Callers must not modify it.
func (c *blobCache) get(id string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[id]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(e)
	return e.Value.(*cachedBlob).data, true
}

func (c *blobCache) put(id string, data []byte) {
	if c == nil || int64(len(data)) > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; ok {
		return
	}
	c.items[id] = c.lru.PushFront(&cachedBlob{id, data})
	c.size += int64(len(data))
	for c.size > c.max {
		c.drop(c.lru.Back())
	}
}

func (c *blobCache) evict(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[id]; ok {
		c.drop(e)
	}
}

func (c *blobCache) drop(e *list.Element) {
	b := c.lru.Remove(e).(*cachedBlob)
	delete(c.items, b.id)
	c.size -= int64(len(b.data))
}

// SetReadCache sets how many bytes of small blobs reads may keep in memory;
// 0 disables the cache. Call it before serving.
func (s *Store) SetReadCache(bytes int64) {
	if bytes <= 0 {
		s.hot = nil
		return
	}
	s.hot = newBlobCache(bytes)
}
