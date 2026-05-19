// curate_tool.go — exposes a `curate_session` tool the lead agent can call
// when the user explicitly asks to "save this as a learned skill". The
// tool simply touches a marker file the EventSessionEnd hook checks; the
// actual curation still runs after the session ends, on the same path as
// the auto-trigger. We do NOT spawn the curator inline because doing so
// from inside the lead's invocation would deadlock on the shared LLM
// budget.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/yoke/internal/paths"
)

// curateMarkerPath is set per-process; the hook reads it. We use an
// in-memory set rather than a file because the marker is only ever
// consumed within the same process that wrote it.
var (
	curateRequestMu sync.Mutex
	curateRequested = map[string]bool{}
)

// RequestCurateSession marks a session for curator processing. It is used
// by the curate_session tool and by explicit UI shortcuts (for example
// /learn in the TUI) so both paths share identical behavior.
func RequestCurateSession(userID, sessionID, reason string) (string, error) {
	if userID == "" || sessionID == "" {
		return "", fmt.Errorf("missing user_id/session_id")
	}
	key := SessionSuffix(userID, sessionID)
	curateRequestMu.Lock()
	curateRequested[key] = true
	curateRequestMu.Unlock()
	logsDir := paths.LogsDir()
	path := filepath.Join(logsDir, fmt.Sprintf("agent_curate_%s.txt", key))
	if reason == "" {
		reason = "manual curation request"
	}
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(reason+"\n"), 0o644); err != nil {
		return "", err
	}
	return "Session marked for curation. The curator will examine the audit and statelog after the session ends.", nil
}

// CurateSessionRequested reports whether the lead asked to curate this
// session. Consumed by registerCuratorHook to bypass the "no candidate"
// short-circuit and treat the session as worth examining.
func CurateSessionRequested(sessionKey string) bool {
	curateRequestMu.Lock()
	defer curateRequestMu.Unlock()
	return curateRequested[sessionKey]
}

// CurateSessionRequestedByIDs is a convenience helper for callers that
// have user/session IDs rather than the pre-computed session suffix key.
func CurateSessionRequestedByIDs(userID, sessionID string) bool {
	if userID == "" || sessionID == "" {
		return false
	}
	return CurateSessionRequested(SessionSuffix(userID, sessionID))
}

type curateIn struct {
	Reason string `json:"reason" jsonschema:"one-line reason explaining why this session is worth distilling into a soft-skill"`
}
type curateOut struct {
	Result string `json:"result"`
}

// curateSessionTool returns the tool the lead mounts. The session key
// is derived from the tool.Context at call time so each invocation marks
// the right session.
func curateSessionTool() tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "curate_session",
		Description: "Mark the current session for soft-skill curation. " +
			"Call this when the user explicitly says something like 'save this as a skill', " +
			"'remember how we solved this', or when you reached a non-trivial multi-step success " +
			"the curator should learn from. The actual curation runs after the session ends. " +
			"Argument: `reason` (string, required, one-line explanation).",
	}, func(ctx tool.Context, in curateIn) (curateOut, error) {
		msg, err := RequestCurateSession(ctx.UserID(), ctx.SessionID(), in.Reason)
		if err != nil {
			return curateOut{}, err
		}
		return curateOut{Result: msg}, nil
	})
	if err != nil {
		panic(fmt.Errorf("curate_session tool: %w", err))
	}
	return t
}

// silence unused-context warning if a future change needs it
var _ = context.Background
