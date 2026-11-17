package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/collmem"
)

func reviewMems() []collmem.Memory {
	return []collmem.Memory{
		{Name: "tabs", Type: "feedback", Created: "2026-01-01", Updated: "2026-01-01", Body: "Indent with tabs."},
		{Name: "indent", Type: "feedback", Created: "2026-03-01", Updated: "2026-03-01", Body: "Use tabs to indent."},
		{Name: "db", Type: "project", Created: "2026-02-01", Updated: "2026-02-01", Body: "DB is pg-staging."},
		{Name: "profile", Type: "user", Created: "2025-01-01", Updated: "2025-01-01", Body: "Go backend dev."},
		{Name: "docs", Type: "reference", Created: "2026-11-01", Updated: "2026-11-01", Body: "Docs at wiki/x."},
	}
}

func TestParseAndValidateReview(t *testing.T) {
	reply := "Here:\n```json\n" + `{"ops":[
	 {"op":"merge","names":["tabs","indent"],"into":{"name":"tabs","type":"feedback","description":"Tabs","content":"Indent with tabs."},"reason":"same rule"},
	 {"op":"delete","names":["tabs"],"reason":"again"},
	 {"op":"delete","names":["ghost"],"reason":"absent"},
	 {"op":"merge","names":["docs","profile"],"into":{"name":"db","type":"user","description":"d","content":"x"},"reason":"steals another memory's name"},
	 {"op":"update","names":["docs"],"into":{"name":"other","type":"reference","description":"d","content":"x"},"reason":"wrong name"},
	 {"op":"rename","names":["docs"],"reason":"unknown op"}
	]}` + "\n```"
	ops, err := parseMemoryReview(reply)
	if err != nil {
		t.Fatal(err)
	}
	got := validateReviewOps(ops, reviewMems(), time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	var kinds []string
	for _, op := range got {
		kinds = append(kinds, op.Op+":"+strings.Join(op.Names, "+"))
	}
	// the merge; tabs is not reused; ghost, the name theft, the bad update and
	// the unknown op are dropped; db (project, 9 months old) gets a check; the
	// old user memory does not.
	if strings.Join(kinds, " ") != "merge:tabs+indent check:db" {
		t.Fatalf("ops = %v", kinds)
	}
	if got[0].Versions["indent"] == "" || got[1].Versions["db"] == "" {
		t.Fatal("versions must be pinned")
	}
}

func TestParseMemoryReviewRejectsProse(t *testing.T) {
	if _, err := parseMemoryReview("Nothing to do."); err == nil {
		t.Fatal("prose without JSON must be an error")
	}
}

func TestReviewRequestCarriesDates(t *testing.T) {
	m := reviewMems()
	m[0].Confirmations, m[0].LastConfirmed = 2, "2026-10-01"
	req := buildMemoryReviewRequest(m, "2026-11-17")
	text := req.Contents[0].Parts[0].Text
	for _, want := range []string{"Today is 2026-11-17.", "## tabs (feedback)", "restated 2× (last 2026-10-01)", "saved 2026-02-01, updated 2026-02-01"} {
		if !strings.Contains(text, want) {
			t.Fatalf("request lacks %q:\n%s", want, text)
		}
	}
}
