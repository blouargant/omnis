package main

import (
	"os"
	"testing"
)

// TestMain keeps the developer's installed Agent-Skills registry (and the
// Claude Code plugins under it) out of every test: a test that wants plugins
// points OMNIS_AGENTSKILLS_DIR at a fixture itself.
func TestMain(m *testing.M) {
	os.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	os.Exit(m.Run())
}
