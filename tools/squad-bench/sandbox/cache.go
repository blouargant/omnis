package sandbox

import "sync"

// LruCache is a tiny fixed-size cache used by the request layer.
type LruCache struct {
	mu    sync.Mutex
	cap   int
	data  map[string]string
	order []string
}

// NewLruCache returns an empty cache that holds at most capacity entries.
func NewLruCache(capacity int) *LruCache {
	return &LruCache{cap: capacity, data: map[string]string{}}
}
