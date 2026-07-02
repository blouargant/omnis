// manager.go — the (root, language) server pool. It is the LSP analogue of the
// MCP subprocess pool (internal/mcp/pool.go): lazily start one server per
// (workspace-root, language) key, share it across every tool call and squad
// that touches a file in that root, and reclaim it on idle. It is built once on
// Infrastructure so it survives hot-reload (the live config is read through a
// resolver func, not snapshotted). Server lifecycle — spawn, idle-GC, LRU
// eviction, shutdown — is the manager's; the langServer holds the connection.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// ErrNoServer is returned when no configured server handles a file's extension.
var ErrNoServer = errors.New("lsp: no language server configured for file")

// Default tuning. Servers like rust-analyzer can hold a project's whole model
// in memory, so the pool is capped and idle-evicted more aggressively than MCP.
const (
	defaultIdleTTL     = 10 * time.Minute
	defaultMaxServers  = 8
	defaultInitTimeout = 45 * time.Second
)

// Manager pools language servers by (root, language).
type Manager struct {
	cfgFn func() *Config // live config resolver (reload-aware)

	mu      sync.Mutex
	servers map[string]*serverEntry

	idleTTL     time.Duration
	maxServers  int
	initTimeout time.Duration
}

// serverEntry is a pool slot. ready is closed once the start attempt finishes;
// ls/err are valid only after ready closes (singleflight start).
type serverEntry struct {
	ls    *langServer
	err   error
	ready chan struct{}
}

// NewManager builds a pool reading its config through cfgFn (so a hot-reload of
// lsp_config.json is picked up on the next resolve). A nil cfgFn yields an
// empty config (every resolve returns ErrNoServer).
func NewManager(cfgFn func() *Config) *Manager {
	if cfgFn == nil {
		cfgFn = func() *Config { return &Config{} }
	}
	return &Manager{
		cfgFn:       cfgFn,
		servers:     map[string]*serverEntry{},
		idleTTL:     defaultIdleTTL,
		maxServers:  defaultMaxServers,
		initTimeout: defaultInitTimeout,
	}
}

func serverKey(root, name string) string { return root + "\x00" + name }

// HasServers reports whether the live config declares at least one language
// server. The squad wiring uses it to skip mounting the lsp_* tools entirely
// when no lsp_config.json is present, keeping the toolset clean (additive
// no-op contract).
func (m *Manager) HasServers() bool {
	c := m.cfgFn()
	return c != nil && len(c.Servers) > 0
}

// ResolveServer returns the running language server for path, lazily starting
// it on first use. Concurrent resolves of the same key share one start
// (singleflight). Returns ErrNoServer when no server handles the extension.
func (m *Manager) ResolveServer(ctx context.Context, path string) (*langServer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s, ok := m.cfgFn().ServerForFile(abs)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoServer, filepath.Ext(abs))
	}
	root := DetectRoot(filepath.Dir(abs), s.RootMarkers)
	key := serverKey(root, s.Name)

	m.mu.Lock()
	m.sweepLocked()
	if e, ok := m.servers[key]; ok {
		m.mu.Unlock()
		select {
		case <-e.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.err != nil {
			return nil, e.err
		}
		e.ls.touch()
		return e.ls, nil
	}
	e := &serverEntry{ready: make(chan struct{})}
	m.servers[key] = e
	m.mu.Unlock()

	// Ensure the server's declared dependencies (its binary) are installed
	// before spawning — prompts the user on first use, like skill/MCP requires.
	if err := m.ensureDeps(ctx, s); err != nil {
		e.err = err
		close(e.ready)
		m.mu.Lock()
		delete(m.servers, key)
		m.mu.Unlock()
		return nil, err
	}

	ls, startErr := startServer(s, root, m.initTimeout)
	e.ls, e.err = ls, startErr
	close(e.ready)
	if startErr != nil {
		// Drop the failed slot so a later call can retry.
		m.mu.Lock()
		delete(m.servers, key)
		m.mu.Unlock()
		return nil, startErr
	}
	m.mu.Lock()
	m.enforceMaxLocked(key)
	m.mu.Unlock()
	return ls, nil
}

// NotifyChange tells a running server that path changed on disk, so its view
// (and the diagnostics derived from it) stay current after an agent/user edit.
// It only touches an already-running server with the document already open —
// it never starts a server or opens a new document, so editing files for
// languages nobody is using costs nothing. Wired to the host's file-change
// signal (M6); a no-op when no matching live server holds the document.
func (m *Manager) NotifyChange(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	s, ok := m.cfgFn().ServerForFile(abs)
	if !ok {
		return
	}
	key := serverKey(DetectRoot(filepath.Dir(abs), s.RootMarkers), s.Name)
	m.mu.Lock()
	e, ok := m.servers[key]
	m.mu.Unlock()
	if ok && isReady(e) {
		e.ls.syncIfOpen(abs)
	}
}

// runningServerFor returns the already-running language server whose scope
// includes path, or nil when none is live — it never starts one.
func (m *Manager) runningServerFor(path string) *langServer {
	if m == nil {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	c := m.cfgFn()
	if c == nil {
		return nil
	}
	s, ok := c.ServerForFile(abs)
	if !ok {
		return nil
	}
	key := serverKey(DetectRoot(filepath.Dir(abs), s.RootMarkers), s.Name)
	m.mu.Lock()
	e, ok := m.servers[key]
	m.mu.Unlock()
	if ok && isReady(e) && e.err == nil {
		return e.ls
	}
	return nil
}

// DiagnosticsIfRunning returns path's current diagnostics from an already-running
// server, briefly waiting for the just-applied edit to settle. ok is false when
// no server is live for path — the caller (edit-fused diagnostics) then skips
// fusion with zero added latency, so an edit to a file whose language has no
// running server is never slowed down. A server that runs but errors returns
// (nil, true), i.e. "no delta information available".
func (m *Manager) DiagnosticsIfRunning(ctx context.Context, path string, maxWait, quiet time.Duration) ([]Diagnostic, bool) {
	ls := m.runningServerFor(path)
	if ls == nil {
		return nil, false
	}
	d, err := ls.Diagnostics(ctx, path, maxWait, quiet)
	if err != nil {
		return nil, true
	}
	return d, true
}

// ensureDeps runs the process-wide dependency gate for a server's requirements.
// A no-op when the server declares none or no gate is set (the binary must then
// already be on PATH). deps.Ensure short-circuits when the binary is present, so
// this only prompts/installs when something is actually missing.
func (m *Manager) ensureDeps(ctx context.Context, s Server) error {
	if len(s.Requires) == 0 {
		return nil
	}
	g := getDepGate()
	if g == nil {
		return nil
	}
	return g(ctx, sessionFromContext(ctx), s.Requires)
}

// sweepLocked stops servers idle beyond idleTTL. Victims are removed from the
// map under the lock and stopped asynchronously (stop() blocks up to a few
// seconds). Caller holds m.mu.
func (m *Manager) sweepLocked() {
	if m.idleTTL <= 0 {
		return
	}
	now := time.Now()
	for key, e := range m.servers {
		if !isReady(e) {
			continue
		}
		if now.Sub(e.ls.idleSince()) > m.idleTTL {
			delete(m.servers, key)
			go e.ls.stop()
		}
	}
}

// enforceMaxLocked evicts least-recently-used ready servers until the pool is
// within maxServers, never evicting protect (the just-started key). Caller
// holds m.mu.
func (m *Manager) enforceMaxLocked(protect string) {
	if m.maxServers <= 0 {
		return
	}
	for len(m.servers) > m.maxServers {
		var victimKey string
		var victim *serverEntry
		for key, e := range m.servers {
			if key == protect || !isReady(e) {
				continue
			}
			if victim == nil || e.ls.idleSince().Before(victim.ls.idleSince()) {
				victimKey, victim = key, e
			}
		}
		if victim == nil {
			break // nothing evictable (all starting or protected)
		}
		delete(m.servers, victimKey)
		go victim.ls.stop()
	}
}

// Shutdown stops every pooled server and waits for them to exit. Call on
// process shutdown.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	entries := make([]*serverEntry, 0, len(m.servers))
	for k, e := range m.servers {
		entries = append(entries, e)
		delete(m.servers, k)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, e := range entries {
		if !isReady(e) {
			continue
		}
		wg.Add(1)
		go func(ls *langServer) {
			defer wg.Done()
			ls.stop()
		}(e.ls)
	}
	wg.Wait()
}

// isReady reports whether a slot has finished a successful start.
func isReady(e *serverEntry) bool {
	select {
	case <-e.ready:
		return e.err == nil && e.ls != nil
	default:
		return false
	}
}
