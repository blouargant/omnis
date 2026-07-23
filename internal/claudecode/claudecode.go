// Package claudecode drives an external Claude Code CLI (`claude`) as a headless
// per-project worker: the `claude_code` tool runs `claude -p <task> --output-format
// json [--resume <id>] --allowedTools <allowlist> [--model <m>]` in the session's
// working directory, parses the JSON envelope, and remembers the returned
// session_id so later calls in the same driver session resume it. Modelled on
// internal/astgrep (a deps-gated, explicit-argv, JSON-output binary tool). Imports
// only stdlib + ADK + core/adk + internal/deps + core/tools — no agent/sessions/fleet.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/deps"
)

// binary is the Claude Code CLI executable name.
const binary = "claude"

// runTimeout bounds a single claude invocation. A coding task can be long; the
// fleet turn budget bounds cost elsewhere.
const runTimeout = 30 * time.Minute

// DefaultAllowedTools is the conservative launch allowlist for an unattended
// Driver: read/inspect + edit files + read-only git. Build/test/other commands
// are opt-in per project via the allowlist override. NEVER includes arbitrary
// Bash or --dangerously-skip-permissions. Rule syntax is Claude Code's
// --allowedTools grammar.
var DefaultAllowedTools = []string{
	"Read", "Edit", "Write", "Grep", "Glob",
	"Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)", "Bash(ls:*)", "Bash(cat:*)",
}

// Requirement is the dependency descriptor: the `claude` binary on PATH.
func Requirement() deps.Requirement {
	return deps.Requirement{
		Command: binary,
		Label:   "Claude Code CLI (external fleet worker)",
		Install: deps.Install{Default: "npm install -g @anthropic-ai/claude-code"},
	}
}

// DepGate mirrors the skills/astgrep gate: it returns "" when the claude CLI is
// available (or was just installed) and a model-facing notice otherwise.
// Installed once, process-wide, from the agent layer.
type DepGate func(tc adk.ToolContext) string

var gate DepGate

// SetDepGate installs the process-wide dependency gate. A nil gate disables
// gating (the tool then only does a plain PATH check).
func SetDepGate(g DepGate) { gate = g }

// ensureDep runs the gate (or a plain PATH check when no gate is wired) and
// returns a non-empty notice when the claude CLI is unavailable.
func ensureDep(tc adk.ToolContext) string {
	if gate != nil {
		return gate(tc)
	}
	if !deps.Present(binary) {
		return "the Claude Code CLI (`claude`) is not installed — install it (`" +
			Requirement().Install.Command() + "`) so this project's Driver can run."
	}
	return ""
}

// --- allowlist override hook -------------------------------------------------

var (
	allowMu       sync.RWMutex
	allowResolver func(sessionID string) []string
)

// SetAllowlistResolver installs a hook mapping a driver session to its project's
// allowlist override (nil/empty ⇒ DefaultAllowedTools). The server installs one
// backed by the session's collection; nil ⇒ always default.
func SetAllowlistResolver(f func(sessionID string) []string) {
	allowMu.Lock()
	allowResolver = f
	allowMu.Unlock()
}

func allowlistFor(sessionID string) []string {
	allowMu.RLock()
	f := allowResolver
	allowMu.RUnlock()
	if f != nil {
		if custom := f(sessionID); len(custom) > 0 {
			return custom
		}
	}
	return DefaultAllowedTools
}

// --- per-session claude session-id store -------------------------------------

var (
	sessMu   sync.Mutex
	sessions = map[string]string{} // omnis driver session id -> claude session_id
)

func rememberSession(sessionID, claudeID string) {
	if sessionID == "" || claudeID == "" {
		return
	}
	sessMu.Lock()
	sessions[sessionID] = claudeID
	sessMu.Unlock()
}

func resumeID(sessionID string) string {
	sessMu.Lock()
	defer sessMu.Unlock()
	return sessions[sessionID]
}

// ForgetSession drops the stored claude session id for a driver session. Called
// on session delete/archive so a reused id can't leak across tasks.
func ForgetSession(sessionID string) {
	sessMu.Lock()
	delete(sessions, sessionID)
	sessMu.Unlock()
}

// --- the tool ----------------------------------------------------------------

type claudeCodeIn struct {
	Task string `json:"task" jsonschema:"the self-contained coding task for the external Claude Code worker to carry out in this project's directory"`
}
type claudeCodeOut struct {
	Result string `json:"result"`
}

type envelope struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// runClaudeCode is the handler, kept as the direct functiontool handler (tc is
// used for SessionID() to key the resumed-session store + allowlist override,
// and for fstools.CwdForContext(tc) to run in the session's project directory —
// both need the ToolContext, not just a session id string).
func runClaudeCode(tc adk.ToolContext, in claudeCodeIn) (claudeCodeOut, error) {
	if notice := ensureDep(tc); notice != "" {
		return claudeCodeOut{Result: notice}, nil
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return claudeCodeOut{Result: "claude_code: `task` is required."}, nil
	}
	sessionID := tc.SessionID()
	cwd := fstools.CwdForContext(tc)

	args := []string{"-p", task, "--output-format", "json",
		"--allowedTools", strings.Join(allowlistFor(sessionID), ",")}
	if rid := resumeID(sessionID); rid != "" {
		args = append(args, "--resume", rid)
	}

	cctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.Output()
	if err != nil {
		return claudeCodeOut{Result: fmt.Sprintf("claude_code: the Claude Code worker failed: %v", err)}, nil
	}
	var env envelope
	if e := json.Unmarshal(stdout, &env); e != nil {
		// Not JSON (older/other output) — return raw so nothing is lost.
		return claudeCodeOut{Result: strings.TrimSpace(string(stdout))}, nil
	}
	if env.SessionID != "" {
		rememberSession(sessionID, env.SessionID)
	}
	if strings.TrimSpace(env.Result) == "" {
		return claudeCodeOut{Result: "(the Claude Code worker returned no text)"}, nil
	}
	return claudeCodeOut{Result: env.Result}, nil
}

// Tools returns the claude_code tool group.
func Tools() []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "claude_code",
		Description: "Carry out a coding task in THIS project's directory by driving the external Claude Code worker. " +
			"Pass a single self-contained `task`; the worker sees the repository on disk (not this conversation), so " +
			"restate everything it needs. Call it again to continue — the worker keeps its context across your calls " +
			"within this session. It runs with a fixed permission allowlist (it cannot ask for more mid-task).",
	}, runClaudeCode)
	if err != nil {
		panic(fmt.Errorf("claude_code tool: %w", err))
	}
	return []tool.Tool{t}
}
