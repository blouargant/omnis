package agent

import "sync"

// fileChangeHook is the process-wide "a file changed on disk" notifier. The
// server wires it to the LSP manager's NotifyChange after Infrastructure is
// built (SetFileChangeHook), so an agent Write/Edit/revert keeps any running
// language server's open-buffer view current. Process-wide because the LSP
// manager is — it survives hot-reload, independent of which generation made the
// edit. CLI/TUI leave it nil (no file-change signal there; the lsp_diagnostics
// tool re-syncs from disk on its own, so correctness is unaffected).
var (
	fileChangeHookMu sync.RWMutex
	fileChangeHook   func(path string)
)

// SetFileChangeHook registers the process-wide file-change notifier. Called once
// by the server. Passing nil clears it.
func SetFileChangeHook(fn func(path string)) {
	fileChangeHookMu.Lock()
	fileChangeHook = fn
	fileChangeHookMu.Unlock()
}

// FireFileChange notifies the registered hook (if any) that path changed. A
// no-op on surfaces that never set it.
func FireFileChange(path string) {
	fileChangeHookMu.RLock()
	fn := fileChangeHook
	fileChangeHookMu.RUnlock()
	if fn != nil {
		fn(path)
	}
}
