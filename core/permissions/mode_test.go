package permissions

import (
	"path/filepath"
	"strings"
	"testing"
)

func modeCfg(t *testing.T, body string) *Config {
	t.Helper()
	cfg, err := ParseBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCheckArgsVerdictModeOverridesConfig(t *testing.T) {
	cfg := modeCfg(t, `{"permissions":{"defaultMode":"default","allow":[],"ask":[],"deny":[]}}`)
	args := map[string]any{"command": "make deploy"}
	if v := cfg.CheckArgsVerdictMode("Bash", args, "/work", ModeDontAsk); v.Decision != DecisionDeny {
		t.Errorf("dontAsk: %+v", v)
	}
	if v := cfg.CheckArgsVerdictMode("Bash", args, "/work", ModeBypass); v.Decision != DecisionAllow {
		t.Errorf("bypass: %+v", v)
	}
	if v := cfg.CheckArgsVerdict("Bash", args, "/work"); v.Decision != DecisionAsk || !v.DefaultAsk {
		t.Errorf("config mode: %+v", v)
	}
}

func TestAutoAppliesAcceptEditsStep(t *testing.T) {
	cfg := modeCfg(t, `{"permissions":{"defaultMode":"default","allow":[],"ask":[],"deny":[]}}`)
	cwd := t.TempDir()
	v := cfg.CheckArgsVerdictMode("Edit", map[string]any{"file_path": filepath.Join(cwd, "a.go")}, cwd, ModeAuto)
	if v.Decision != DecisionAllow || v.Reason != "acceptEdits mode" {
		t.Errorf("in cwd: %+v", v)
	}
	v = cfg.CheckArgsVerdictMode("Edit", map[string]any{"file_path": "/etc/x"}, cwd, ModeAuto)
	if v.Decision != DecisionAsk || !v.DefaultAsk {
		t.Errorf("outside: %+v", v)
	}
}

func TestMultiEditUsesGivenMode(t *testing.T) {
	cfg := modeCfg(t, `{"permissions":{"defaultMode":"default","allow":[],"ask":[],"deny":[]}}`)
	cwd := t.TempDir()
	args := map[string]any{"files": []any{
		map[string]any{"file_path": filepath.Join(cwd, "a.go"), "edits": []any{}},
		map[string]any{"file_path": filepath.Join(cwd, "b.go"), "edits": []any{}},
	}}
	if v := cfg.CheckArgsVerdictMode("MultiEdit", args, cwd, ModePlan); v.Decision != DecisionDeny {
		t.Errorf("plan: %+v", v)
	}
}

func TestReadBypassDisabled(t *testing.T) {
	dir := t.TempDir()
	on := writePerm(t, dir, "on.json", `{"permissions":{"disableBypassPermissionsMode":"disable"}}`)
	off := writePerm(t, dir, "off.json", `{"permissions":{"disableBypassPermissionsMode":"enable"}}`)
	if !ReadBypassDisabled(on) {
		t.Error("disable should be true")
	}
	if ReadBypassDisabled(off) || ReadBypassDisabled(filepath.Join(dir, "none.json")) {
		t.Error("other/missing should be false")
	}
}

func TestParseModeStrict(t *testing.T) {
	if m, ok := ParseModeStrict("plan"); !ok || m != ModePlan {
		t.Error("plan")
	}
	if _, ok := ParseModeStrict(""); ok {
		t.Error("empty")
	}
	if _, ok := ParseModeStrict("nope"); ok {
		t.Error("unknown")
	}
}

func TestModeStoreEffective(t *testing.T) {
	var nilStore *ModeStore
	if nilStore.Effective("s") != ModeDefault {
		t.Error("nil store")
	}
	nilStore.AutoAllowed("s")
	nilStore.AutoBlocked("s")

	s := NewModeStore()
	if s.Effective("s") != ModeDefault {
		t.Error("no source")
	}
	s.SetConfigSource(&Static{C: modeCfg(t, `{"permissions":{"defaultMode":"acceptEdits"}}`)})
	if s.Effective("s") != ModeAcceptEdits || s.ConfigMode() != ModeAcceptEdits {
		t.Error("config mode")
	}
	s.Set("s", ModePlan)
	if m, ok := s.Get("s"); !ok || m != ModePlan || s.Effective("s") != ModePlan {
		t.Error("override")
	}
	s.Clear("s")
	if s.Effective("s") != ModeAcceptEdits {
		t.Error("clear")
	}
	s.Set("s", ModeBypass)
	if s.Effective("s") != ModeBypass {
		t.Error("bypass allowed")
	}
	s.SetBypassDisabled(true)
	if !s.BypassDisabled() || s.Effective("s") != ModeDefault {
		t.Error("bypass disabled")
	}
	s.Forget("s")
	if _, ok := s.Get("s"); ok {
		t.Error("forget")
	}
}

func TestModeStoreAutoEscalation(t *testing.T) {
	s := NewModeStore()
	if s.AutoBlocked("a") || s.AutoBlocked("a") {
		t.Fatal("early escalate")
	}
	if !s.AutoBlocked("a") || !s.AutoEscalated("a") {
		t.Fatal("third should escalate")
	}
	s.ResetAuto("a")
	if s.AutoEscalated("a") {
		t.Fatal("reset")
	}
	// allows reset consecutive but not total
	for i := 0; i < 19; i++ {
		if s.AutoBlocked("b") {
			t.Fatalf("escalated early at %d", i)
		}
		s.AutoAllowed("b")
	}
	if s.AutoEscalated("b") {
		t.Fatal("19 total")
	}
	if !s.AutoBlocked("b") || !s.AutoEscalated("b") {
		t.Fatal("20th total")
	}
	s.ResetAuto("b")
	if s.AutoEscalated("b") || s.AutoBlocked("b") {
		t.Fatal("reset b")
	}
}

func TestConvertNoAutoWarning(t *testing.T) {
	_, warns, err := ImportClaudeSettings([]byte(`{"permissions":{"defaultMode":"auto"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warns {
		if strings.Contains(w, "auto") {
			t.Errorf("warning: %s", w)
		}
	}
}
