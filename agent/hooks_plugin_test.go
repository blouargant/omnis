package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/events"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/attest"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/hookstate"
)

// buildHooksPlugin returns nothing for the router squad (hooks fire on the
// answering squad), and a real plugin otherwise.
func TestBuildHooksPluginRouterSkipped(t *testing.T) {
	engine := hooks.NewReloader(filepath.Join(t.TempDir(), "hooks.json"), nil)

	if p, err := buildHooksPlugin(engine, nil, nil, nil, true); err != nil || p != nil {
		t.Fatalf("router squad: got (%v, %v), want (nil, nil)", p, err)
	}
	if p, err := buildHooksPlugin(engine, nil, nil, nil, false); err != nil || p == nil {
		t.Fatalf("answering squad: got (%v, %v), want a plugin", p, err)
	}
	if p, err := buildHooksPlugin(nil, nil, nil, nil, false); err != nil || p != nil {
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

	before, _ := hookToolCallbacks(engine, nil, state, nil, false)
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
	beforeBlock, _ := hookToolCallbacks(hooks.NewReloader(blockPath, nil), nil, state, nil, false)
	beforeAllow, _ := hookToolCallbacks(hooks.NewReloader(allowPath, nil), nil, state, nil, false)

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

// The record-to-read join: a verdict recorded through the tool's store must reach
// the hook's stdin under the SAME session key. Both sides resolve the session with
// realSessionID; this fails if that ever stops being true.
func TestRecordedAttestationReachesTheHookInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hooks.json")
	toolName := "Bash"
	body := `{"hooks":{"PreToolUse":[{"matcher":"^` + toolName + `$","hooks":[{"command":"cat > input.json"}]}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := hooks.NewReloader(cfgPath, nil)
	state := hookstate.New()
	store := attest.New()

	sid := "attest-session"
	args := map[string]any{"command": "kubectl apply -f manifest.yaml"}
	subject := hookstate.HashArgs(args)
	store.Record(sid, subject, attest.VerdictApproved, "helm-owned check passed")

	before, _ := hookToolCallbacks(engine, nil, state, store, false)
	if before == nil {
		t.Fatal("hookToolCallbacks returned a nil BeforeToolCallback")
	}

	// The hook command runs with the tool context's cwd as its working
	// directory (see beforeTool: cwd := fstools.CwdForContext(tc)), so
	// `cat > input.json` lands in dir — planted via fstools.WithCwd exactly
	// like a real invocation context carries it.
	tc := hookTestCtx{
		ctx:       fstools.WithCwd(context.Background(), dir),
		sessionID: sid,
		agentName: "test-agent",
	}
	if _, err := before(tc, hookTestTool{name: toolName}, args); err != nil {
		t.Fatalf("beforeTool: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "input.json"))
	if err != nil {
		t.Fatalf("read captured hook stdin: %v", err)
	}
	var captured map[string]any
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("unmarshal captured hook stdin: %v (raw: %s)", err, raw)
	}

	// A wrong SESSION key would make this an empty map — assert it isn't, so
	// that failure mode is distinguishable from a wrong SUBJECT key below.
	attestations, ok := captured["attestations"].(map[string]any)
	if !ok || len(attestations) == 0 {
		t.Fatalf("captured input has no attestations for session %q (wrong session key?): %v", sid, captured)
	}

	// A wrong SUBJECT key would make the entry for `subject` missing even
	// though the map itself is non-empty.
	rec, ok := attestations[subject].(map[string]any)
	if !ok {
		t.Fatalf("attestations has no entry for subject %q (wrong subject key?): %v", subject, attestations)
	}
	if rec["verdict"] != string(attest.VerdictApproved) {
		t.Fatalf("attestations[%q].verdict = %v, want %q", subject, rec["verdict"], attest.VerdictApproved)
	}
}

// End to end through the callback: a PreToolUse hook that escalates, in a run
// where nobody can answer, must come back as a refusal — and say so honestly.
// The old text read "(the user declined)" even when there was no user, which
// sends whoever reads the transcript hunting for a decision nobody made.
func TestUnanswerableEscalationBlocksWithAnHonestReason(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	t.Setenv("OMNIS_NON_INTERACTIVE", "1")

	cfgPath := filepath.Join(t.TempDir(), "hooks.json")
	body := `{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[{"command":` +
		`"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\",` +
		`\"permissionDecisionReason\":\"a human must confirm this\"}}'"}]}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// A REAL registry, which is the whole point: reg == nil already denied, but
	// every shipped surface builds one, so this is the path a bench actually
	// takes.
	reg := askuser.NewRegistry()
	state := hookstate.New()
	before, _ := hookToolCallbacks(hooks.NewReloader(cfgPath, nil), reg, state, nil, false)

	sid, toolName := "unattended-session", "Bash"
	args := map[string]any{"command": "kubectl delete pod x -n demo"}

	type result struct {
		out map[string]any
		err error
	}
	done := make(chan result, 1)
	go func() {
		o, err := before(newHookTestCtx(sid), hookTestTool{name: toolName}, args)
		done <- result{o, err}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("beforeTool blocked on an escalation nobody can answer")
	}
	if got.err != nil {
		t.Fatalf("beforeTool: %v", got.err)
	}
	output, _ := got.out["output"].(string)
	if !strings.Contains(output, "[BLOCKED BY HOOK]") {
		t.Fatalf("the call was not blocked: %q", output)
	}
	if !strings.Contains(output, "a human must confirm this") {
		t.Fatalf("the hook's own reason was lost: %q", output)
	}
	if !strings.Contains(output, "unattended") {
		t.Fatalf("the refusal does not say why it could not be escalated: %q", output)
	}
	if strings.Contains(output, "the user declined") {
		t.Fatalf("the refusal claims a decision nobody made: %q", output)
	}
	// A block must still advance the consecutive counter — that is what lets a
	// hook script's own escalation threshold work.
	if _, cons := state.Attempt(sid, toolName, args); cons != 1 {
		t.Fatalf("consecutive after an unanswerable escalation = %d, want 1", cons)
	}
}
