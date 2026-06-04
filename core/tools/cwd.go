package tools

import (
	"context"
	"path/filepath"
	"sync"

	"google.golang.org/adk/tool"
)

// cwdCtxKey keys the per-invocation working directory carried on the Go context.
type cwdCtxKey struct{}

// WithCwd returns a context carrying the working directory the agent's
// file-system tools should operate in for this invocation. Unlike the
// SessionID-based resolver it propagates down into sub-agent runners (which run
// in a freshly created session with a different id), so a Read/Grep the leader
// delegates to the investigator uses the same directory. An empty cwd is a
// no-op. Takes precedence over the resolver.
func WithCwd(ctx context.Context, cwd string) context.Context {
	if cwd == "" {
		return ctx
	}
	return context.WithValue(ctx, cwdCtxKey{}, cwd)
}

func cwdFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(cwdCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// cwdResolver, when set, maps a session ID to that session's working directory
// for agent tool execution (Bash, Read, Write, Edit, Grep, Glob, …). The server
// installs one backed by the same per-session store the "!cd" shell-escape and
// the web-UI Folders panel mutate, so changing a session's folder also changes
// where its agent tools operate. When nil (CLI/one-shot, or a session with no
// recorded directory) tools fall back to the process working directory — the
// historical behaviour.
var (
	cwdResolverMu sync.RWMutex
	cwdResolver   func(sessionID string) string
)

// SetCwdResolver installs the per-session working-directory resolver. Pass nil
// to clear it. Safe for concurrent use.
func SetCwdResolver(f func(sessionID string) string) {
	cwdResolverMu.Lock()
	cwdResolver = f
	cwdResolverMu.Unlock()
}

// sessionCwd returns the working directory for the given tool context's session,
// or "" when no resolver is installed or it yields no directory (the caller then
// falls back to the process working directory).
func sessionCwd(ctx tool.Context) string {
	if ctx == nil {
		return ""
	}
	// A cwd planted on the invocation context wins — it survives into sub-agent
	// runners (which generate a fresh session id) so delegated file ops share
	// the leader's directory. tool.Context embeds context.Context.
	if c := cwdFromContext(ctx); c != "" {
		return c
	}
	cwdResolverMu.RLock()
	f := cwdResolver
	cwdResolverMu.RUnlock()
	if f == nil {
		return ""
	}
	return f(ctx.SessionID())
}

// CwdForContext returns the working directory the agent's file-system tools
// will operate in for this tool invocation — the context-carried cwd (WithCwd)
// if present, otherwise the SessionID resolver. Empty when neither resolves
// (CLI/one-shot). Exposed so the permissions layer can scope "Allow in this
// project" to the very directory the tools will use, keeping rule matching and
// tool execution in agreement.
func CwdForContext(ctx tool.Context) string { return sessionCwd(ctx) }

// CwdFor resolves the working directory from a plain context plus an explicit
// session id — the cwd planted by WithCwd wins, otherwise the SessionID
// resolver. It mirrors sessionCwd for callers that only hold a context.Context
// and a session id (e.g. a BeforeModelCallback), not a tool.Context. Empty when
// neither resolves.
func CwdFor(ctx context.Context, sessionID string) string {
	if c := cwdFromContext(ctx); c != "" {
		return c
	}
	cwdResolverMu.RLock()
	f := cwdResolver
	cwdResolverMu.RUnlock()
	if f == nil {
		return ""
	}
	return f(sessionID)
}

// resolveAgainst joins a relative path onto cwd. An absolute path, an empty
// path, or an empty cwd is returned unchanged.
func resolveAgainst(cwd, path string) string {
	if cwd == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}
