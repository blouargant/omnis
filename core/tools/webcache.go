package tools

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/omnis/core/events"
)

// webCache remembers WebFetch and WebSearch results for a short while, per
// user-facing session, so a page or query one research sub-agent already
// retrieved is not fetched again by its siblings. In the 2026-10-31 Knowledge
// bench 20–30 % of the fetches of a deep-research turn were URLs another
// parallel web_agent had already read.
//
// It saves the network round-trip and the search-provider call, not context
// tokens: each sub-agent still receives the content in its own context.
//
// Keyed by the turn's root session (a sub-agent's own session is an ephemeral
// per-call one), so one user's results are never served to another session.
// Errors and empty searches are not cached, so a transient failure is retried.
type webCache struct {
	mu  sync.Mutex
	ttl time.Duration
	max int
	m   map[string]webCacheEntry
	now func() time.Time
}

type webCacheEntry struct {
	val string
	at  time.Time
}

const (
	webCacheTTL = 15 * time.Minute
	webCacheMax = 512
)

var sharedWebCache = newWebCache(webCacheTTL, webCacheMax)

func newWebCache(ttl time.Duration, max int) *webCache {
	return &webCache{ttl: ttl, max: max, m: map[string]webCacheEntry{}, now: time.Now}
}

// webCacheScope returns the session the cache entry belongs to: the turn's
// root session when the run context carries one, else the tool's own
// session. Empty means "do not cache".
func webCacheScope(ctx context.Context, own string) string {
	if root := events.RootSessionFromContext(ctx); root != "" {
		return root
	}
	return own
}

// get returns the cached value for key in scope, or calls fetch and caches its
// result when it is cacheable.
func (c *webCache) get(scope, key string, fetch func() string) string {
	if c == nil || scope == "" {
		return fetch()
	}
	k := scope + "\x00" + key
	c.mu.Lock()
	if e, ok := c.m[k]; ok && c.now().Sub(e.at) < c.ttl {
		c.mu.Unlock()
		return e.val
	}
	c.mu.Unlock()

	val := fetch()
	if !cacheableWebResult(val) {
		return val
	}
	c.mu.Lock()
	c.m[k] = webCacheEntry{val: val, at: c.now()}
	c.evictLocked()
	c.mu.Unlock()
	return val
}

// evictLocked drops expired entries, then the oldest ones, until the cache is
// within its size limit.
func (c *webCache) evictLocked() {
	if len(c.m) <= c.max {
		return
	}
	now := c.now()
	for k, e := range c.m {
		if now.Sub(e.at) >= c.ttl {
			delete(c.m, k)
		}
	}
	for len(c.m) > c.max {
		var oldest string
		var at time.Time
		for k, e := range c.m {
			if oldest == "" || e.at.Before(at) {
				oldest, at = k, e.at
			}
		}
		delete(c.m, oldest)
	}
}

func cacheableWebResult(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && t != "(no results)" && !strings.HasPrefix(t, "error ")
}

// cachedSearch runs a WebSearch through the shared cache. The provider is part
// of the key so switching providers mid-session never serves the other's
// results.
func cachedSearch(tc interface {
	context.Context
	SessionID() string
}, provider string, in DDGIn, run func() string) string {
	key := "search\x00" + provider + "\x00" + strings.TrimSpace(in.Query) + "\x00" + strconv.Itoa(in.MaxResults)
	return sharedWebCache.get(webCacheScope(tc, tc.SessionID()), key, run)
}
