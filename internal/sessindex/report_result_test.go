package sessindex

import (
	"strings"
	"testing"
)

func TestReportFinalResultListsSessions(t *testing.T) {
	got := reportFinalResult([]ReportedSession{{SessionID: "fair-lioness", Reason: "GPU prices"}, {SessionID: " "}, {SessionID: "glad-osprey", Reason: "LLM costs"}})
	for _, w := range []string{"fair-lioness", "GPU prices", "glad-osprey", "LLM costs"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in %q", w, got)
		}
	}
	if !strings.Contains(reportFinalResult(nil), "No matching session") {
		t.Fatal("empty report must say so")
	}
}
