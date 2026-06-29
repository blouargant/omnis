// depgate.go — the runtime dependency gate for language servers, mirroring the
// skill (skills.SetDepGate) and MCP gates. When a configured server declares
// `requires` and its binary is missing, the host prompts the user to install it
// (in the active session) before the server starts. The gate is process-wide
// because the LSP manager is; it is set once from Infrastructure. With no gate
// set (CLI/TUI/tests), a missing binary simply fails the start with an exec
// error, which the tool reports.
package lsp

import (
	"context"
	"sync"

	"github.com/blouargant/omnis/internal/deps"
)

// sessionCtxKey carries the active session id on the context passed to
// ResolveServer, so the dep gate can prompt the right session.
type sessionCtxKey struct{}

// withSession returns ctx carrying sessionID (no-op when empty). The lsp tools
// plant it before calling ResolveServer so a first-start dependency prompt
// reaches the user's session.
func withSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

func sessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sessionCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// DepGate ensures a server's declared requirements are installed, prompting the
// user in sessionID. It returns an error when a required binary stays
// unavailable, which blocks the server start.
type DepGate func(ctx context.Context, sessionID string, reqs []deps.Requirement) error

var (
	depGateMu sync.RWMutex
	depGate   DepGate
)

// SetDepGate installs the process-wide LSP dependency gate (nil disables it).
func SetDepGate(g DepGate) {
	depGateMu.Lock()
	depGate = g
	depGateMu.Unlock()
}

func getDepGate() DepGate {
	depGateMu.RLock()
	defer depGateMu.RUnlock()
	return depGate
}
