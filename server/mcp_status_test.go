package main

import (
	"testing"

	mcpcfg "github.com/blouargant/omnis/internal/mcp"
)

func TestMCPStatusViewDefaultsToIdle(t *testing.T) {
	got := mcpStatusView([]string{"fs", "jira"}, map[string]mcpcfg.ServerStatus{
		"fs": {State: mcpcfg.StateError, Err: "boom"},
	})
	if len(got) != 2 || got[0].Name != "fs" || got[0].State != "error" || got[0].Error != "boom" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Name != "jira" || got[1].State != "idle" {
		t.Fatalf("a server the pool never built must read idle, got %+v", got[1])
	}
}
