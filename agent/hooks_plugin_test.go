package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/hookstate"
)

// buildHooksPlugin returns nothing for the router squad (hooks fire on the
// answering squad), and a real plugin otherwise.
func TestBuildHooksPluginRouterSkipped(t *testing.T) {
	engine := hooks.NewReloader(filepath.Join(t.TempDir(), "hooks.json"), nil)

	if p, err := buildHooksPlugin(engine, nil, nil, true); err != nil || p != nil {
		t.Fatalf("router squad: got (%v, %v), want (nil, nil)", p, err)
	}
	if p, err := buildHooksPlugin(engine, nil, nil, false); err != nil || p == nil {
		t.Fatalf("answering squad: got (%v, %v), want a plugin", p, err)
	}
	if p, err := buildHooksPlugin(nil, nil, nil, false); err != nil || p != nil {
		t.Fatalf("nil engine: got (%v, %v), want (nil, nil)", p, err)
	}
}

// A SessionStart hook fires when EventSessionStart is emitted on the bus through
// the once-wired listeners — the path the CLI/TUI use.
func TestWireHookListenersFiresOnBusEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "fired")
	cfgPath := filepath.Join(dir, "hooks.json")
	body := `{"hooks":{"SessionStart":[{"hooks":[{"command":"touch ` + sentinel + `"}]}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := hooks.NewReloader(cfgPath, nil)
	bus := events.NewBus()
	wireHookListeners(context.Background(), bus, engine)

	bus.Emit(events.EventSessionStart, map[string]any{"session_id": "s1"})

	// SessionStart fires synchronously, but allow a brief settle for the OS.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sentinel); err == nil {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("SessionStart hook did not run (sentinel not created)")
}

// hookTestTool is a minimal tool.Tool stub for exercising hookToolCallbacks: only
// Name() is read by the code under test (it is matched against the hook config
// and used as the counter-store key), so Description/IsLongRunning are inert.
type hookTestTool struct{ name string }

func (h hookTestTool) Name() string        { return h.name }
func (h hookTestTool) Description() string { return "" }
func (h hookTestTool) IsLongRunning() bool { return false }

// hookTestCtx is a minimal adk.ToolContext stub, following the cancelCtx pattern
// above (concurrent_agent_tool_test.go): the embedded interface is nil, so only
// the methods implemented here are safe to call — anything else panics, which is
// the point (it would mean beforeTool reaches further into ToolContext than this
// test accounts for). Deadline/Done/Err/Value make it a genuine context.Context —
// cfg.Run derives a timeout context from it to exec the hook command, which would
// panic against a bare nil embed — and SessionID/AgentName are what
// realSessionID and the AgentName input field read.
type hookTestCtx struct {
	adk.ToolContext
	ctx       context.Context
	sessionID string
	agentName string
}

func (c hookTestCtx) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c hookTestCtx) Done() <-chan struct{}       { return c.ctx.Done() }
func (c hookTestCtx) Err() error                  { return c.ctx.Err() }
func (c hookTestCtx) Value(key any) any           { return c.ctx.Value(key) }
func (c hookTestCtx) SessionID() string           { return c.sessionID }
func (c hookTestCtx) AgentName() string           { return c.agentName }

func newHookTestCtx(sessionID string) hookTestCtx {
	return hookTestCtx{ctx: context.Background(), sessionID: sessionID, agentName: "test-agent"}
}

// The no-op contract, pinned. With a PreToolUse matcher that does not match this
// tool, the callback must return BEFORE touching the counter store — otherwise
// every Bash call in the fleet mutates a map on a build with no relevant hooks.
func TestHookCallbacksLeaveTheStoreUntouchedWhenNoHookMatches(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hooks.json")
	// A PreToolUse matcher for a DIFFERENT tool than the one beforeTool is called
	// with below, so cfg.Match must find nothing for it.
	body := `{"hooks":{"PreToolUse":[{"matcher":"^OtherTool$","hooks":[{"command":"exit 2"}]}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := hooks.NewReloader(cfgPath, nil)
	state := hookstate.New()

	before, _ := hookToolCallbacks(engine, nil, state, false)
	if before == nil {
		t.Fatal("hookToolCallbacks returned a nil BeforeToolCallback")
	}

	sid, toolName := "probe-session", "ProbedTool"
	args := map[string]any{"command": "irrelevant"}

	// Baseline: the very first Attempt call for this key is always 1.
	if n, _ := state.Attempt(sid, toolName, args); n != 1 {
		t.Fatalf("baseline Attempt = %d, want 1", n)
	}

	tc := newHookTestCtx(sid)
	if _, err := before(tc, hookTestTool{name: toolName}, args); err != nil {
		t.Fatalf("beforeTool: %v", err)
	}

	// If beforeTool never touched the store (because no hook matched), the next
	// direct Attempt call is the SECOND call ever made for this key, so it must
	// read back 2 — not 3, which is what an internal, unaccounted-for Attempt
	// call inside beforeTool would produce.
	if n, _ := state.Attempt(sid, toolName, args); n != 2 {
		t.Fatalf("Attempt after a non-matching beforeTool call = %d, want 2 (beforeTool touched the store despite no matching hook)", n)
	}
}

// The consecutive counter is what makes escalation possible: advanced on a block,
// reset on anything else. Drift here fires the escalation early or never.
func TestHookCallbacksRecordBlockedAndAllowedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	dir := t.TempDir()

	blockPath := filepath.Join(dir, "block.json")
	if err := os.WriteFile(blockPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[{"command":"exit 2"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	allowPath := filepath.Join(dir, "allow.json")
	if err := os.WriteFile(allowPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[{"command":"exit 0"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// One shared state, two engines (a block-always config and an allow-always
	// config): the store — not the engine — is what carries the consecutive
	// counter across the two calls below.
	state := hookstate.New()
	beforeBlock, _ := hookToolCallbacks(hooks.NewReloader(blockPath, nil), nil, state, false)
	beforeAllow, _ := hookToolCallbacks(hooks.NewReloader(allowPath, nil), nil, state, false)

	sid, toolName := "outcome-session", "Bash"
	args := map[string]any{"command": "irrelevant"}
	tc := newHookTestCtx(sid)

	// A matching hook that exits 2 blocks the call, which must advance the
	// consecutive counter.
	if _, err := beforeBlock(tc, hookTestTool{name: toolName}, args); err != nil {
		t.Fatalf("beforeBlock: %v", err)
	}
	if _, cons := state.Attempt(sid, toolName, args); cons != 1 {
		t.Fatalf("consecutive after one blocked call = %d, want 1", cons)
	}

	// A matching hook that exits 0 allows the call, which must reset it.
	if _, err := beforeAllow(tc, hookTestTool{name: toolName}, args); err != nil {
		t.Fatalf("beforeAllow: %v", err)
	}
	if _, cons := state.Attempt(sid, toolName, args); cons != 0 {
		t.Fatalf("consecutive after an allowed call = %d, want 0 (reset)", cons)
	}
}
