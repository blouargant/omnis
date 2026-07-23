package main

import (
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

func TestClaudeAllowedToolsForCollectionOverride(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	if _, _, err := sessions.AddCollection("Service A"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfileData("Service A", sessions.CollectionProfileData{
		Role: "project", Engine: "claude",
		ClaudeAllowedTools: []string{"Read", "Bash(go test:*)"},
	}); err != nil {
		t.Fatal(err)
	}
	// A plain collection with no ClaudeAllowedTools override.
	if _, _, err := sessions.AddCollection("Notes"); err != nil {
		t.Fatal(err)
	}

	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "driver-a", Collection: "Service A"})
	reg.Add(&sessions.SessionMeta{ID: "driver-notes", Collection: "Notes"})
	reg.Add(&sessions.SessionMeta{ID: "driver-general"}) // General (empty Collection)

	got := claudeAllowedToolsFor(reg, "driver-a")
	if len(got) != 2 || got[0] != "Read" || got[1] != "Bash(go test:*)" {
		t.Fatalf("Service A allowlist = %v, want [Read Bash(go test:*)]", got)
	}

	if got := claudeAllowedToolsFor(reg, "driver-notes"); got != nil {
		t.Fatalf("Notes (no override) allowlist = %v, want nil (⇒ DefaultAllowedTools)", got)
	}

	if got := claudeAllowedToolsFor(reg, "driver-general"); got != nil {
		t.Fatalf("General (empty Collection) allowlist = %v, want nil", got)
	}

	if got := claudeAllowedToolsFor(reg, "unknown-session"); got != nil {
		t.Fatalf("unknown session allowlist = %v, want nil", got)
	}

	if got := claudeAllowedToolsFor(nil, "driver-a"); got != nil {
		t.Fatalf("nil registry allowlist = %v, want nil", got)
	}
}

// TestInstallClaudeAllowlistResolverSmoke exercises the actual startup wiring
// (installClaudeAllowlistResolver → claudecode.SetAllowlistResolver). The
// resolver it installs is a private variable inside internal/claudecode with
// no exported getter, so this can't observe the returned allowlist from here —
// that behavior is covered by TestClaudeAllowedToolsForCollectionOverride
// above, which tests the exact same mapping function the installed closure
// calls. This just confirms the installer runs (including a nil registry,
// e.g. CLI/TUI which never call it) without panicking.
func TestInstallClaudeAllowlistResolverSmoke(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	installClaudeAllowlistResolver(reg)
	installClaudeAllowlistResolver(nil)
}
