package agent

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The System leader reads past sessions (to summarise them into a file) through
// the session_history group: search/read/list, but NOT report_sessions, which
// ends the caller's turn and is only meant for the session_search agent.
func TestSessionHistoryGroupOmitsReportSessions(t *testing.T) {
	cfg := RuntimeAgentConfig{Name: "leader", Tools: []string{"session_history"}}
	tools, _, _, _ := toolsForAgentConfig(context.Background(), cfg, RuntimeSettings{},
		nil, nil, nil, nil, nil, nil, nil, nil)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name())
	}
	for _, want := range []string{"search_sessions", "read_session", "list_sessions"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	if slices.Contains(names, "report_sessions") {
		t.Fatalf("report_sessions must not be mounted: %v", names)
	}
}

func TestShippedLeaderReadsSessionHistory(t *testing.T) {
	b, err := os.ReadFile("../registry/agents/leader/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	var a struct{ Tools []string }
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.Tools, "session_history") {
		t.Fatalf("leader tools %v lack session_history", a.Tools)
	}
}
