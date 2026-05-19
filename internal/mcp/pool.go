// pool.go — refcounted MCP toolset pool. Two agent generations that mount
// the same MCP server share a single backing connection: each Acquire
// bumps the refcount, each Release decrements it. The underlying
// transport (a child process for stdio, an HTTP client for http) is torn
// down when the last reference is released, so a hot-reload that only
// changes one server doesn't restart the others.
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
)

// Handle is a refcounted reference to a pooled MCP toolset. Callers receive
// a Handle from Pool.Acquire and must return it via Pool.Release exactly
// once. The Toolset field is the live toolset to mount on an agent.
type Handle struct {
	Name    string
	Toolset tool.Toolset
	key     string
}

// Pool deduplicates MCP toolset construction by configuration key. Two
// servers with identical transport configuration resolve to the same
// Handle and share their underlying connection.
type Pool struct {
	mu       sync.Mutex
	entries  map[string]*poolEntry
	resolver *InputResolver
}

type poolEntry struct {
	handle   *Handle
	refcount int
}

// NewPool returns an empty pool. The optional resolver enables
// interactive resolution of "${input:id}" template references in
// server env / header / arg / url / command fields; pass nil to
// disable (servers with references will then fail at first Connect).
func NewPool(resolver *InputResolver) *Pool {
	return &Pool{
		entries:  map[string]*poolEntry{},
		resolver: resolver,
	}
}

// Acquire returns a Handle for the configured server, sharing the
// underlying connection with any prior Acquire of an identical
// configuration. Inputs is the parent Config's input declarations,
// used to resolve "${input:id}" references at first Connect. The
// caller MUST invoke Pool.Release on the returned handle when it is
// done.
func (p *Pool) Acquire(s Server, inputs []Input) (*Handle, error) {
	key := configKey(s)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[key]; ok {
		e.refcount++
		return e.handle, nil
	}
	transport, err := p.buildPooledTransport(s, inputs)
	if err != nil {
		return nil, err
	}
	ts, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", s.Name, err)
	}
	h := &Handle{Name: s.Name, Toolset: ts, key: key}
	p.entries[key] = &poolEntry{handle: h, refcount: 1}
	return h, nil
}

// AcquireAll resolves every server in c into pooled Handles, returning the
// toolsets in declaration order and the handles for later Release. On error
// every handle acquired so far is rolled back.
func (p *Pool) AcquireAll(c *Config) ([]tool.Toolset, []*Handle, error) {
	if c == nil {
		return nil, nil, nil
	}
	servers := c.ServerList()
	toolsets := make([]tool.Toolset, 0, len(servers))
	handles := make([]*Handle, 0, len(servers))
	for _, s := range servers {
		h, err := p.Acquire(s, c.Inputs)
		if err != nil {
			for _, prev := range handles {
				p.Release(prev)
			}
			return nil, nil, err
		}
		toolsets = append(toolsets, h.Toolset)
		handles = append(handles, h)
	}
	return toolsets, handles, nil
}

// Release decrements the handle's refcount. When the refcount hits zero
// the entry is removed from the pool; the underlying transport is torn
// down the next time ADK closes the toolset (mcptoolset does not
// currently expose an explicit Close — termination occurs when the last
// reference is dropped and Go's GC closes the pipes).
func (p *Pool) Release(h *Handle) {
	if h == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[h.key]
	if !ok {
		return
	}
	e.refcount--
	if e.refcount <= 0 {
		delete(p.entries, h.key)
	}
}

// Refcount returns the current refcount for a configuration. Returns zero
// when the pool has no entry for that configuration. Useful for tests.
func (p *Pool) Refcount(s Server) int {
	key := configKey(s)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[key]; ok {
		return e.refcount
	}
	return 0
}

// buildPooledTransport constructs the transport for a pooled handle.
// Servers whose env / headers / args / url / command contain a
// "${input:id}" template reference get a lazyTransport wrapper that
// defers the real transport build until first Connect — so the user
// is prompted at first tool use (when a session context exists)
// rather than at agent build time.
func (p *Pool) buildPooledTransport(s Server, inputs []Input) (mcp.Transport, error) {
	if hasInputReferences(s) {
		return newLazyTransport(s, inputs, p.resolver), nil
	}
	return buildTransport(s)
}

// configKey returns a stable digest of the server configuration. Map
// fields (env, headers) are sorted so two semantically equal
// configurations always produce the same key. The transport kind is
// prefixed so a stdio and an http server with otherwise empty fields
// don't collide.
func configKey(s Server) string {
	h := sha256.New()
	kind := s.TransportKind()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	switch kind {
	case TransportHTTP:
		h.Write([]byte(s.URL))
		h.Write([]byte{0})
		writeSortedMap(h, s.Headers)
	default:
		h.Write([]byte(s.Command))
		h.Write([]byte{0})
		for _, a := range s.Args {
			h.Write([]byte(a))
			h.Write([]byte{0})
		}
		writeSortedMap(h, s.Env)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeSortedMap streams k=v\0 pairs into h in lexicographic key order
// so two equal maps hash identically regardless of iteration order.
func writeSortedMap(h interface{ Write([]byte) (int, error) }, m map[string]string) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{'='})
		_, _ = h.Write([]byte(m[k]))
		_, _ = h.Write([]byte{0})
	}
}
