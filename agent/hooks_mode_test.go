package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/permissions"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/hookstate"
)

type modeRig struct {
	before func(adk.ToolContext, string, map[string]any) map[string]any
	asked  atomic.Int32
	judged atomic.Int32
	clear  *permissions.Clearances
}

func newModeRig(t *testing.T, hookCmd string, mode permissions.Mode, judge permissions.AutoJudge) *modeRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	cfgPath := filepath.Join(t.TempDir(), "hooks.json")
	body := `{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[{"command":` + jsonQuote(hookCmd) + `}]}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &modeRig{clear: permissions.NewClearances()}
	reg := askuser.NewRegistry()
	reg.SetNotify(func(q askuser.Question) {
		r.asked.Add(1)
		go func() { _ = reg.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{choiceHookDeny}}) }()
	})
	modes := permissions.NewModeStore()
	modes.Set("s1", mode)
	var wrapped permissions.AutoJudge
	if judge != nil {
		wrapped = func(tc adk.ToolContext, tool string, args map[string]any, cwd, why string) (permissions.AutoVerdict, error) {
			r.judged.Add(1)
			return judge(tc, tool, args, cwd, why)
		}
	}
	cb, _ := hookToolCallbacks(hooks.NewReloader(cfgPath, nil), reg, hookstate.New(), nil, r.clear, nil, false,
		hookPermDeps{Modes: modes, Judge: wrapped})
	r.before = func(tc adk.ToolContext, name string, args map[string]any) map[string]any {
		out, _ := cb(tc, hookTestTool{name: name}, args)
		return out
	}
	return r
}

func jsonQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

const askCmd = `echo '{"hookSpecificOutput":{"permissionDecision":"ask"}}'`

func outStr(m map[string]any) string {
	s, _ := m["output"].(string)
	return s
}

func cleared(r *modeRig) bool {
	return r.clear.Take("s1", "inv1", "call1", "Bash")
}

func TestHookAskBypassAllows(t *testing.T) {
	r := newModeRig(t, askCmd, permissions.ModeBypass, nil)
	if out := r.before(wtc("a"), "Bash", map[string]any{"command": "x"}); out != nil {
		t.Fatalf("blocked: %v", out)
	}
	if r.asked.Load() != 0 {
		t.Fatal("bypass must not ask")
	}
	if !cleared(r) {
		t.Fatal("expected a clearance")
	}
}

func TestHookAskAutoJudged(t *testing.T) {
	block := func(adk.ToolContext, string, map[string]any, string, string) (permissions.AutoVerdict, error) {
		return permissions.AutoVerdict{Allow: false, Reason: "nope"}, nil
	}
	r := newModeRig(t, askCmd, permissions.ModeAuto, block)
	out := r.before(wtc("a"), "Bash", map[string]any{"command": "x"})
	if outStr(out) != permissions.AutoBlockMessage("Bash", "nope") {
		t.Fatalf("out = %v", out)
	}
	if r.asked.Load() != 0 {
		t.Fatal("must not ask")
	}

	allow := func(adk.ToolContext, string, map[string]any, string, string) (permissions.AutoVerdict, error) {
		return permissions.AutoVerdict{Allow: true}, nil
	}
	r = newModeRig(t, askCmd, permissions.ModeAuto, allow)
	if out := r.before(wtc("a"), "Bash", map[string]any{"command": "x"}); out != nil {
		t.Fatalf("blocked: %v", out)
	}
	if !cleared(r) {
		t.Fatal("expected a clearance")
	}
}

func TestHookAskAutoJudgeErrorFallsThroughToAsk(t *testing.T) {
	fail := func(adk.ToolContext, string, map[string]any, string, string) (permissions.AutoVerdict, error) {
		return permissions.AutoVerdict{}, os.ErrInvalid
	}
	r := newModeRig(t, askCmd, permissions.ModeAuto, fail)
	r.before(wtc("a"), "Bash", map[string]any{"command": "x"})
	if r.asked.Load() != 1 {
		t.Fatalf("asked = %d, want 1", r.asked.Load())
	}
}

func TestHookAskDontAskBlocks(t *testing.T) {
	r := newModeRig(t, askCmd, permissions.ModeDontAsk, nil)
	out := r.before(wtc("a"), "Bash", map[string]any{"command": "x"})
	if outStr(out) != "[BLOCKED BY HOOK] Bash: dontAsk mode: the hook asked for confirmation" {
		t.Fatalf("out = %v", out)
	}
	if r.asked.Load() != 0 {
		t.Fatal("must not ask")
	}
}

func TestHookAskDefaultAsks(t *testing.T) {
	r := newModeRig(t, askCmd, permissions.ModeDefault, nil)
	out := r.before(wtc("a"), "Bash", map[string]any{"command": "x"})
	if r.asked.Load() != 1 {
		t.Fatalf("asked = %d, want 1", r.asked.Load())
	}
	if !strings.Contains(outStr(out), "the user declined") {
		t.Fatalf("out = %v", out)
	}
}

func TestHookDenyBlocksInBypass(t *testing.T) {
	r := newModeRig(t, "echo no >&2; exit 2", permissions.ModeBypass, nil)
	out := r.before(wtc("a"), "Bash", map[string]any{"command": "x"})
	if !strings.HasPrefix(outStr(out), "[BLOCKED BY HOOK]") {
		t.Fatalf("out = %v", out)
	}
}
