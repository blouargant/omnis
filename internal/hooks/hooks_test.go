package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndMatch(t *testing.T) {
	data := []byte(`{
	  "hooks": {
	    "PreToolUse": [
	      { "matcher": "Write|Edit", "hooks": [ { "type": "command", "command": "guard" } ] },
	      { "matcher": "", "hooks": [ { "command": "all-tools" } ] }
	    ],
	    "SessionStart": [
	      { "hooks": [ { "command": "start" } ] }
	    ]
	  }
	}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.HasRules() {
		t.Fatal("HasRules = false, want true")
	}

	// Write matches both the "Write|Edit" matcher and the empty (match-all) one.
	got := cfg.Match(PreToolUse, "Write")
	if len(got) != 2 {
		t.Fatalf("Match(PreToolUse, Write) = %d cmds, want 2", len(got))
	}
	// Bash matches only the empty matcher.
	if got := cfg.Match(PreToolUse, "Bash"); len(got) != 1 || got[0].Command != "all-tools" {
		t.Fatalf("Match(PreToolUse, Bash) = %+v, want [all-tools]", got)
	}
	// A subject-less event matches every matcher under it.
	if got := cfg.Match(SessionStart, ""); len(got) != 1 || got[0].Command != "start" {
		t.Fatalf("Match(SessionStart) = %+v, want [start]", got)
	}
	// Unknown event → nothing.
	if got := cfg.Match(Notification, ""); got != nil {
		t.Fatalf("Match(Notification) = %+v, want nil", got)
	}
}

func TestMatcherRegexFallback(t *testing.T) {
	cfg, _ := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"(","hooks":[{"command":"x"}]}]}}`))
	// "(" is an invalid regexp → falls back to exact equality.
	if got := cfg.Match(PreToolUse, "Write"); got != nil {
		t.Fatalf("invalid-regex matcher matched %q unexpectedly", "Write")
	}
	if got := cfg.Match(PreToolUse, "("); len(got) != 1 {
		t.Fatalf("invalid-regex matcher should match its literal self, got %d", len(got))
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load(missing) error = %v, want nil", err)
	}
	if cfg.HasRules() {
		t.Fatal("missing file should yield no rules")
	}
}

func TestMergeIsAdditive(t *testing.T) {
	base, _ := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"command":"a"}]}]}}`))
	overlay, _ := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"command":"b"}]}]}}`))
	merged := Merge(base, overlay)
	got := merged.Match(PreToolUse, "Write")
	if len(got) != 2 {
		t.Fatalf("merged Match = %d cmds, want 2 (additive)", len(got))
	}
	if got[0].Command != "a" || got[1].Command != "b" {
		t.Fatalf("merge order = %v, want [a b]", []string{got[0].Command, got[1].Command})
	}
}

func TestReloaderPicksUpChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReloader(path, nil)
	if r.Snapshot().HasRules() {
		t.Fatal("expected no rules initially")
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"hooks":[{"command":"x"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !r.Snapshot().HasRules() {
		t.Fatal("reloader did not pick up the new rules")
	}
}
