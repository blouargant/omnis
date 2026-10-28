package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fstools "github.com/blouargant/omnis/core/tools"
)

const fixtureRoot = "../claudeplugins/testdata/agentskills/claude/demo"

func recordPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "record")
	t.Setenv("HOOK_RECORD", p)
	return p
}

func readRecords(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range strings.Split(string(b), "---\n") {
		if strings.TrimSpace(r) != "" {
			out = append(out, r)
		}
	}
	return out
}

func pluginConfig(t *testing.T, matcher string) *Config {
	t.Helper()
	root, err := filepath.Abs(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	c := Empty()
	c.events[PreToolUse] = []Matcher{{
		Matcher: matcher,
		Hooks:   []Command{{Type: "command", Command: "${CLAUDE_PLUGIN_ROOT}/hooks/record.sh", Timeout: 10}},
		Plugin:  &PluginRef{Name: "demo", Root: root},
	}}
	return c
}

func runPre(c *Config, ctx context.Context, tool string, in Input) Outcome {
	in.ToolName = tool
	return c.Run(ctx, PreToolUse, tool, in, "", 10*time.Second)
}

func TestPluginMatcherSeesMultiEditPerFile(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "MultiEdit")
	runPre(c, context.Background(), "MultiEdit", Input{ToolInput: map[string]any{"files": []any{
		map[string]any{"file_path": "/a.go", "edits": []any{}},
		map[string]any{"file_path": "/b.go", "edits": []any{}},
	}}})
	recs := readRecords(t, rec)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d: %v", len(recs), recs)
	}
	for i, want := range []string{"/a.go", "/b.go"} {
		if !strings.Contains(recs[i], `"file_path":"`+want+`"`) || !strings.Contains(recs[i], `"edits"`) || !strings.Contains(recs[i], `"tool_name":"MultiEdit"`) {
			t.Errorf("record %d: %s", i, recs[i])
		}
	}
}

func TestPluginMatcherMultiEditWithoutPathsPassesOriginal(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "MultiEdit")
	runPre(c, context.Background(), "MultiEdit", Input{ToolInput: map[string]any{"files": []any{}}})
	recs := readRecords(t, rec)
	if len(recs) != 1 || !strings.Contains(recs[0], `"files"`) {
		t.Fatalf("records: %v", recs)
	}
}

func TestPluginMatcherAliasesBackgroundShell(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "Bash")
	if len(c.Match(PreToolUse, "bash_background")) == 0 {
		t.Fatal("bash_background should match a plugin Bash matcher")
	}
	runPre(c, context.Background(), "bash_background", Input{ToolInput: map[string]any{"command": "iapcli x", "extra": 1}})
	recs := readRecords(t, rec)
	if len(recs) != 1 || !strings.Contains(recs[0], `"tool_name":"Bash"`) || !strings.Contains(recs[0], `"tool_input":{"command":"iapcli x"}`) {
		t.Fatalf("records: %v", recs)
	}
}

func TestNativeMatcherUnchanged(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "Bash")
	c.events[PreToolUse][0].Plugin = nil
	c.events[PreToolUse][0].Hooks[0].Command = filepath.Join(mustAbs(t, fixtureRoot), "hooks/record.sh")
	if len(c.Match(PreToolUse, "bash_background")) != 0 {
		t.Fatal("native Bash matcher must not match bash_background")
	}
	c.events[PreToolUse][0].Matcher = "MultiEdit"
	runPre(c, context.Background(), "MultiEdit", Input{ToolInput: map[string]any{"files": []any{map[string]any{"file_path": "/a"}}}})
	recs := readRecords(t, rec)
	if len(recs) != 1 || !strings.Contains(recs[0], `"files"`) {
		t.Fatalf("records: %v", recs)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPluginCommandGetsPluginRoot(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "Bash")
	runPre(c, context.Background(), "Bash", Input{ToolInput: map[string]any{"command": "ls"}})
	recs := readRecords(t, rec)
	if len(recs) != 1 || !strings.Contains(recs[0], "CLAUDE_PLUGIN_ROOT="+mustAbs(t, fixtureRoot)) {
		t.Fatalf("records: %v", recs)
	}
}

func TestPluginHookNeverSeesShellEnv(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "Bash")
	ctx := fstools.WithShellEnv(context.Background(), []string{"IAPCLI_TOKEN=secret"})
	runPre(c, ctx, "Bash", Input{ToolInput: map[string]any{"command": "ls"}})
	recs := readRecords(t, rec)
	if len(recs) != 1 || !strings.Contains(recs[0], "IAPCLI_TOKEN=unset") || strings.Contains(recs[0], "secret") {
		t.Fatalf("records: %v", recs)
	}
}

func TestAgentTypeIsSerialised(t *testing.T) {
	rec := recordPath(t)
	c := pluginConfig(t, "Bash")
	runPre(c, context.Background(), "Bash", Input{AgentType: "demo:worker", ToolInput: map[string]any{"command": "ls"}})
	recs := readRecords(t, rec)
	if len(recs) != 1 || !strings.Contains(recs[0], `"agent_type":"demo:worker"`) {
		t.Fatalf("records: %v", recs)
	}
}

func TestSetPluginLayersReloads(t *testing.T) {
	r := NewReloader("", nil)
	layer := PluginLayer{Name: "demo", Root: mustAbs(t, fixtureRoot), Path: mustAbs(t, fixtureRoot+"/hooks/hooks.json")}
	r.SetPluginLayers([]PluginLayer{layer})
	if n := len(r.Snapshot().Match(PreToolUse, "Bash")); n != 1 {
		t.Fatalf("want 1 command, got %d", n)
	}
	r.SetPluginLayers(nil)
	if n := len(r.Snapshot().Match(PreToolUse, "Bash")); n != 0 {
		t.Fatalf("want 0 commands, got %d", n)
	}
}

func TestPluginHooksCannotFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"exit 1","fail_closed":true}]}]}}`), 0o644)
	r := NewReloader("", nil)
	r.SetPluginLayers([]PluginLayer{{Name: "p", Root: dir, Path: path}})
	out := r.Snapshot().Run(context.Background(), PreToolUse, "Bash", Input{ToolName: "Bash"}, "", 5*time.Second)
	if out.Decision != DecisionProceed || out.Ran != 1 {
		t.Fatalf("want proceed after 1 run, got %+v", out)
	}
}

func TestSetPluginLayersLastWins(t *testing.T) {
	layer := PluginLayer{Name: "demo", Root: mustAbs(t, fixtureRoot), Path: mustAbs(t, fixtureRoot+"/hooks/hooks.json")}
	r := NewReloader("", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = r.Refresh()
		}
	}()
	for i := 0; i < 100; i++ {
		r.SetPluginLayers([]PluginLayer{layer})
		r.SetPluginLayers(nil)
	}
	<-done
	if n := len(r.Snapshot().Match(PreToolUse, "Bash")); n != 0 {
		t.Fatalf("stale snapshot: %d commands after final SetPluginLayers(nil)", n)
	}
}
