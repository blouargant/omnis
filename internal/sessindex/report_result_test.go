package sessindex

import (
	"strings"
	"testing"
)

func TestReportFinalResultListsSessionsByTitle(t *testing.T) {
	titles := map[string]string{"fair-lioness": "Comparaison prix serveurs 8 GPU H200, B200, B300"}
	got := reportFinalResult([]ReportedSession{{SessionID: "fair-lioness", Reason: "GPU prices"}, {SessionID: " "}, {SessionID: "glad-osprey", Reason: "LLM costs"}},
		func(id string) string { return titles[id] })
	for _, w := range []string{`"Comparaison prix serveurs 8 GPU H200, B200, B300"`, "fair-lioness", "GPU prices", "glad-osprey", "LLM costs", "title"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in %q", w, got)
		}
	}
	if !strings.Contains(reportFinalResult(nil, nil), "No matching session") {
		t.Fatal("empty report must say so")
	}
}

// The session_id is a petname (fair-lioness) the user never sees; the sidebar
// shows the title. Every tool that hands sessions to a model says to name them
// by title — an agent summarising sessions listed them by id.
func TestSessionToolsSayToNameSessionsByTitle(t *testing.T) {
	for _, tl := range NewTools(Deps{}) {
		if tl.Name() == "report_sessions" {
			continue
		}
		if !strings.Contains(tl.Description(), sessionNamingNote) {
			t.Errorf("%s description lacks the naming note", tl.Name())
		}
	}
}
