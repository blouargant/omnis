package agent

import "sync"

// reloadHook is the process-wide hot-reload trigger. The server wires it to
// Manager.Reload after the Manager is built (SetReloadHook); CLI/TUI leave it
// nil. It is process-wide because the Manager is — a registries tool running
// inside any generation rebuilds the *current* generation, which is what we
// want regardless of which generation the calling session is pinned to.
var (
	reloadHookMu sync.RWMutex
	reloadHook   func()
)

// SetReloadHook registers the process-wide hot-reload trigger. Called once by
// the server after the Manager is built. Passing nil clears it.
func SetReloadHook(fn func()) {
	reloadHookMu.Lock()
	reloadHook = fn
	reloadHookMu.Unlock()
}

// requestReload invokes the registered hot-reload trigger if one is set and
// reports whether it fired. Returns false on surfaces without hot-reload
// (CLI/TUI), where config edits apply on the next start — so callers can
// surface "reloaded" honestly.
func requestReload() bool {
	reloadHookMu.RLock()
	fn := reloadHook
	reloadHookMu.RUnlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}
