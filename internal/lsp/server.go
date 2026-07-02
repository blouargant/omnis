// server.go — a single running language server: its OS process plus the
// JSON-RPC Client, the negotiated server capabilities, an idle clock for GC,
// and the set of documents opened with it. Query helpers (DocumentSymbols, and
// in later milestones definition/references/hover/diagnostics) live here so
// they share document-sync bookkeeping. Lifecycle is owned by the Manager.
package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// boundedBuffer keeps only the last max bytes written — used to capture a tail
// of a language server's stderr for diagnosing a start/handshake failure.
type boundedBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

// tail returns the last n non-empty captured stderr lines, joined for a one-line
// error suffix.
func (b *boundedBuffer) tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(b.buf)), "\n")
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			out = append([]string{s}, out...)
		}
	}
	return strings.Join(out, " | ")
}

// langServer is one live (root, language) server instance.
type langServer struct {
	name string // config key, e.g. "go"
	lang string // default LSP languageId, e.g. "go" / "typescript"
	cfg  Server // the server config, for per-file languageId resolution
	root string // workspace root the server was initialized against
	cmd  *exec.Cmd
	cli  *Client
	caps map[string]any // server capabilities from the initialize result

	mu       sync.Mutex
	lastUsed time.Time
	openDocs map[DocumentURI]int // uri → last sent version

	diagMu  sync.Mutex
	diags   map[DocumentURI]diagRecord // latest published diagnostics per uri
	diagSeq uint64                     // bumped on every publish (any uri)

	progMu     sync.Mutex
	activeProg map[string]bool // open $/progress tokens (server is busy)
	everProg   bool            // server has used $/progress at least once

	stderr *boundedBuffer // bounded tail of server stderr, for start diagnosis
}

// diagMinQuiet is the shortened quiet window used when a progress-emitting
// server has signalled it is idle (analysis done) after a fresh publish.
const diagMinQuiet = 100 * time.Millisecond

// diagRecord is the cached result of a publishDiagnostics notification.
type diagRecord struct {
	items     []Diagnostic
	version   *int
	updatedAt time.Time
	seq       uint64
}

// startServer spawns and initializes a language server rooted at root. The
// handshake runs on its own background context (bounded by initTimeout), not a
// request context, because the server is shared across callers and must not be
// torn down when the first caller's request ends.
func startServer(s Server, root string, initTimeout time.Duration) (*langServer, error) {
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &boundedBuffer{max: 8192}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", s.Command, err)
	}

	cli := NewClient(stdout, stdin)
	ls := &langServer{
		name:       s.Name,
		lang:       s.langID(),
		cfg:        s,
		root:       root,
		cmd:        cmd,
		cli:        cli,
		openDocs:   map[DocumentURI]int{},
		diags:      map[DocumentURI]diagRecord{},
		activeProg: map[string]bool{},
		stderr:     stderr,
	}
	// Install the diagnostics handler before Start so no early publish is lost.
	cli.SetNotifyHandler(ls.onNotify)
	cli.Start()

	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	res, err := cli.Initialize(ctx, root)
	if err != nil {
		ls.stop()
		if tail := stderr.tail(5); tail != "" {
			return nil, fmt.Errorf("initialize %q: %w (server stderr: %s)", s.Name, err, tail)
		}
		return nil, fmt.Errorf("initialize %q: %w", s.Name, err)
	}
	ls.caps = res.Capabilities
	ls.touch()
	return ls, nil
}

// Root returns the workspace root the server was initialized against.
func (ls *langServer) Root() string { return ls.root }

// Lang returns the server's LSP languageId.
func (ls *langServer) Lang() string { return ls.lang }

func (ls *langServer) touch() {
	ls.mu.Lock()
	ls.lastUsed = time.Now()
	ls.mu.Unlock()
}

func (ls *langServer) idleSince() time.Time {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.lastUsed
}

// ensureOpen sends textDocument/didOpen for path the first time it is queried,
// reading the current content from disk. M4 adds didChange on agent edits.
func (ls *langServer) ensureOpen(path string) error {
	uri := PathToURI(path)
	ls.mu.Lock()
	_, open := ls.openDocs[uri]
	ls.mu.Unlock()
	if open {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := ls.cli.DidOpen(uri, ls.cfg.langIDForPath(path), string(data)); err != nil {
		return err
	}
	ls.mu.Lock()
	ls.openDocs[uri] = 1
	ls.mu.Unlock()
	return nil
}

// DocumentSymbols returns the symbol outline of a file, opening it first.
func (ls *langServer) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
	if err := ls.ensureOpen(path); err != nil {
		return nil, err
	}
	ls.touch()
	return ls.cli.DocumentSymbols(ctx, PathToURI(path))
}

// onNotify handles inbound server notifications: publishDiagnostics (cached
// per-URI with a monotonic seq so a waiter can tell a fresh publish from a stale
// one) and $/progress (tracking whether the server is still doing background
// work, used to shorten the diagnostics wait once it goes idle).
func (ls *langServer) onNotify(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		ls.diagMu.Lock()
		ls.diagSeq++
		ls.diags[p.URI] = diagRecord{
			items:     p.Diagnostics,
			version:   p.Version,
			updatedAt: time.Now(),
			seq:       ls.diagSeq,
		}
		ls.diagMu.Unlock()
	case "$/progress":
		var p struct {
			Token json.RawMessage `json:"token"`
			Value struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		key := string(p.Token)
		ls.progMu.Lock()
		switch p.Value.Kind {
		case "begin":
			ls.activeProg[key] = true
			ls.everProg = true
		case "end":
			delete(ls.activeProg, key)
		}
		ls.progMu.Unlock()
	}
}

// progressIdle reports whether the server uses $/progress and currently has no
// open progress token — i.e. it has signalled that background analysis is done.
// For a server that never emits progress it returns false, so the diagnostics
// wait falls back to the full quiet window.
func (ls *langServer) progressIdle() bool {
	ls.progMu.Lock()
	defer ls.progMu.Unlock()
	return ls.everProg && len(ls.activeProg) == 0
}

// syncDoc pushes path's current on-disk content to the server: didOpen the
// first time, didChange (bumping version) thereafter. Calling it before a query
// or diagnostics request is what makes the server analyse the agent's latest
// edits without any edit-tool hook.
func (ls *langServer) syncDoc(path string) error {
	uri := PathToURI(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	ls.mu.Lock()
	ver, open := ls.openDocs[uri]
	if !open {
		ls.openDocs[uri] = 1
		ls.mu.Unlock()
		return ls.cli.DidOpen(uri, ls.cfg.langIDForPath(path), string(data))
	}
	ver++
	ls.openDocs[uri] = ver
	ls.mu.Unlock()
	return ls.cli.DidChange(uri, ver, string(data))
}

// syncIfOpen pushes new content only for a document already open with this
// server — the proactive hook for external edits (Manager.NotifyChange). It
// never opens a new document, so editing a file no language server is using
// stays free.
func (ls *langServer) syncIfOpen(path string) {
	uri := PathToURI(path)
	ls.mu.Lock()
	ver, open := ls.openDocs[uri]
	if !open {
		ls.mu.Unlock()
		return
	}
	ver++
	ls.openDocs[uri] = ver
	ls.mu.Unlock()
	if data, err := os.ReadFile(path); err == nil {
		_ = ls.cli.DidChange(uri, ver, string(data))
	}
}

// Diagnostics syncs path to the server and returns its diagnostics. It waits up
// to maxWait for a publish that post-dates the sync, then a quiet window for any
// follow-up (servers often publish an empty set during indexing, then the real
// one), and returns the latest. An absent publish within maxWait yields the
// last-known set (or nil), which the tool renders as "no diagnostics".
func (ls *langServer) Diagnostics(ctx context.Context, path string, maxWait, quiet time.Duration) ([]Diagnostic, error) {
	uri := PathToURI(path)
	ls.diagMu.Lock()
	baseline := ls.diagSeq
	ls.diagMu.Unlock()

	if err := ls.syncDoc(path); err != nil {
		return nil, err
	}
	ls.touch()

	deadline := time.Now().Add(maxWait)
	for {
		ls.diagMu.Lock()
		rec, ok := ls.diags[uri]
		ls.diagMu.Unlock()

		now := time.Now()
		if ok && rec.seq > baseline {
			sinceUpdate := now.Sub(rec.updatedAt)
			// Return once the publish has settled for the full quiet window, or
			// sooner if a progress-emitting server has gone idle (analysis done)
			// after a brief minimum — never on the first instant, to let an
			// empty-then-real follow-up land.
			if sinceUpdate >= quiet || (sinceUpdate >= diagMinQuiet && ls.progressIdle()) {
				return rec.items, nil
			}
		}
		if now.After(deadline) {
			if ok {
				return rec.items, nil
			}
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// locateSymbol translates a symbol name to an LSP position in path. It prefers
// the symbol's own declaration (its documentSymbol selectionRange — exact and
// stable), and falls back to the first whole-word textual occurrence so a
// symbol that is merely *referenced* in the file (not declared there) still
// resolves. Returns false when the name appears nowhere in the file.
func (ls *langServer) locateSymbol(ctx context.Context, path, symbol string) (Position, bool, error) {
	syms, err := ls.DocumentSymbols(ctx, path) // also ensures the file is open
	if err != nil {
		return Position{}, false, err
	}
	if p, ok := findSymbolPos(syms, symbol); ok {
		return p, true, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Position{}, false, err
	}
	if p, ok := locateWord(string(data), symbol); ok {
		return p, true, nil
	}
	return Position{}, false, nil
}

// findSymbolPos searches a symbol tree for name, matching either the full name
// or its base (so "Call" matches gopls's "(*Client).Call"). Returns the
// symbol's selection-range start.
func findSymbolPos(syms []DocumentSymbol, name string) (Position, bool) {
	for _, s := range syms {
		if s.Name == name || symbolBaseName(s.Name) == name {
			return s.SelectionRange.Start, true
		}
		if p, ok := findSymbolPos(s.Children, name); ok {
			return p, true
		}
	}
	return Position{}, false
}

// SymbolExtent returns the full source range of a named symbol in path (its
// whole declaration, not just the name), resolved via the document outline.
// Used by lsp_read_symbol to slice out one symbol's body so the model reads the
// 20 lines it needs instead of the whole file. Returns ok=false when the name
// isn't a declared symbol in the file.
func (ls *langServer) SymbolExtent(ctx context.Context, path, symbol string) (DocumentSymbol, bool, error) {
	syms, err := ls.DocumentSymbols(ctx, path)
	if err != nil {
		return DocumentSymbol{}, false, err
	}
	if s, ok := findSymbolFull(syms, symbol); ok {
		return s, true, nil
	}
	return DocumentSymbol{}, false, nil
}

// findSymbolFull is findSymbolPos's sibling: it returns the whole matching
// DocumentSymbol (so the caller has Range/Kind), matching the full name or its
// base (so "Call" matches gopls's "(*Client).Call").
func findSymbolFull(syms []DocumentSymbol, name string) (DocumentSymbol, bool) {
	for _, s := range syms {
		if s.Name == name || symbolBaseName(s.Name) == name {
			return s, true
		}
		if c, ok := findSymbolFull(s.Children, name); ok {
			return c, true
		}
	}
	return DocumentSymbol{}, false
}

// symbolBaseName strips a receiver/qualifier prefix, e.g. "(*Client).Call" →
// "Call", "pkg.Foo" → "Foo".
func symbolBaseName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

// Definition resolves the definition location(s) of a named symbol in path.
func (ls *langServer) Definition(ctx context.Context, path, symbol string) ([]Location, error) {
	pos, ok, err := ls.locateSymbol(ctx, path, symbol)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("symbol %q not found in %s", symbol, filepath.Base(path))
	}
	ls.touch()
	return ls.cli.Definition(ctx, PathToURI(path), pos)
}

// References lists every usage of a named symbol across the project.
func (ls *langServer) References(ctx context.Context, path, symbol string, includeDecl bool) ([]Location, error) {
	pos, ok, err := ls.locateSymbol(ctx, path, symbol)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("symbol %q not found in %s", symbol, filepath.Base(path))
	}
	ls.touch()
	return ls.cli.References(ctx, PathToURI(path), pos, includeDecl)
}

// Hover returns a named symbol's signature/documentation as plain text.
func (ls *langServer) Hover(ctx context.Context, path, symbol string) (string, error) {
	pos, ok, err := ls.locateSymbol(ctx, path, symbol)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("symbol %q not found in %s", symbol, filepath.Base(path))
	}
	ls.touch()
	return ls.cli.Hover(ctx, PathToURI(path), pos)
}

// WorkspaceSymbols searches the project-wide symbol index by name.
func (ls *langServer) WorkspaceSymbols(ctx context.Context, query string) ([]SymbolInformation, error) {
	ls.touch()
	return ls.cli.WorkspaceSymbols(ctx, query)
}

// Rename computes the per-file edits to rename a named symbol to newName across
// the project. It does not write anything — the caller applies the edits.
func (ls *langServer) Rename(ctx context.Context, path, symbol, newName string) (map[DocumentURI][]TextEdit, error) {
	pos, ok, err := ls.locateSymbol(ctx, path, symbol)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("symbol %q not found in %s", symbol, filepath.Base(path))
	}
	ls.touch()
	return ls.cli.Rename(ctx, PathToURI(path), pos, newName)
}

// RequestCodeActions asks the server for the code actions of the given kinds
// over path's whole content, resolving each action's edit (via codeAction/resolve
// when the server returned the action without one). It syncs the file first so
// the actions reflect the latest on-disk content. diags is the diagnostic context
// used for quickfixes. The caller decides which actions to apply and writes them.
func (ls *langServer) RequestCodeActions(ctx context.Context, path string, only []string, diags []Diagnostic) ([]CodeAction, error) {
	if err := ls.syncDoc(path); err != nil {
		return nil, err
	}
	ls.touch()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	uri := PathToURI(path)
	actions, err := ls.cli.CodeActions(ctx, uri, wholeFileRange(string(data)), only, diags)
	if err != nil {
		return nil, err
	}
	for i := range actions {
		if actions[i].Edit == nil && len(actions[i].Data) > 0 {
			if resolved, rerr := ls.cli.ResolveCodeAction(ctx, actions[i]); rerr == nil && resolved.Edit != nil {
				actions[i].Edit = resolved.Edit
			}
		}
	}
	return actions, nil
}

// stop shuts the server down gracefully (shutdown+exit), then closes the
// transport and reaps the process. Best-effort and bounded so a wedged server
// can't block the manager.
func (ls *langServer) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = ls.cli.Shutdown(ctx)
	_ = ls.cli.Close()
	if ls.cmd != nil && ls.cmd.Process != nil {
		_ = ls.cmd.Process.Kill()
		_ = ls.cmd.Wait()
	}
}
