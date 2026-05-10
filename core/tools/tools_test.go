package tools

import (
	"testing"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tools := New()

	wantNames := []string{"bash", "read", "write", "grep", "glob", "revert", "mime"}
	if len(tools) != len(wantNames) {
		t.Fatalf("New() returned %d tools, want %d", len(tools), len(wantNames))
	}

	got := make(map[string]bool, len(tools))
	for _, tool := range tools {
		got[tool.Name()] = true
	}
	for _, name := range wantNames {
		if !got[name] {
			t.Errorf("New() missing tool %q", name)
		}
	}
}
