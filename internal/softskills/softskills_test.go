package softskills

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"google.golang.org/adk/agent"
)

func TestToolsetRenamesAndDiscovers(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
name: demo
description: A learned demo procedure used by tests.
category: meta
---

# Demo

Just a fixture.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, err := Toolset(context.Background(), dir)
	if err != nil {
		t.Fatalf("Toolset: %v", err)
	}
	if got := ts.Name(); got != "softskills" {
		t.Errorf("toolset name = %q, want %q", got, "softskills")
	}
	tools, err := ts.Tools(agent.ReadonlyContext(nil))
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	got := make([]string, 0, len(tools))
	for _, tool := range tools {
		got = append(got, tool.Name())
	}
	sort.Strings(got)
	want := []string{"list_softskills", "load_softskill", "load_softskill_resource"}
	if len(got) != len(want) {
		t.Fatalf("got tools %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("tool[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestToolsetCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist-yet")
	if _, err := Toolset(context.Background(), dir); err != nil {
		t.Fatalf("Toolset: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("expected dir to be created: %v", err)
	}
}
