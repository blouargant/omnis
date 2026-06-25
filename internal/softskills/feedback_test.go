package softskills

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRecordAndLoadFeedbackRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if err := RecordFeedback(dir, "u1_sess", "anything off?", "yes the X bit was wrong"); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadFeedback(dir, "u1_sess")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("expected feedback record, got nil")
	}
	if rec.Question != "anything off?" || rec.Answer != "yes the X bit was wrong" {
		t.Errorf("round-tripped record = %+v", rec)
	}
	if time.Since(rec.Timestamp) > time.Minute {
		t.Errorf("Timestamp not recent: %v", rec.Timestamp)
	}
}

func TestRecordFeedbackRejectsBlankAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := RecordFeedback(dir, "s", "q", "   "); err == nil {
		t.Error("expected blank-answer rejection, got nil")
	}
}

func TestRecordFeedbackRejectsBlankSuffix(t *testing.T) {
	dir := t.TempDir()
	if err := RecordFeedback(dir, "", "q", "a"); err == nil {
		t.Error("expected blank-suffix rejection")
	}
}

func TestLoadFeedbackMissingFile(t *testing.T) {
	dir := t.TempDir()
	rec, err := LoadFeedback(dir, "nope")
	if err != nil {
		t.Fatalf("LoadFeedback on missing: %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil rec, got %+v", rec)
	}
}

func TestLoadFeedbackMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(FeedbackPath(dir, "bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFeedback(dir, "bad"); err == nil {
		t.Error("expected parse error on malformed file")
	}
}

func TestRecordFeedbackOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := RecordFeedback(dir, "s", "q", "first"); err != nil {
		t.Fatal(err)
	}
	if err := RecordFeedback(dir, "s", "q2", "second"); err != nil {
		t.Fatal(err)
	}
	rec, _ := LoadFeedback(dir, "s")
	if rec.Answer != "second" || rec.Question != "q2" {
		t.Errorf("overwrite failed: %+v", rec)
	}
}

func TestFeedbackPath(t *testing.T) {
	got := FeedbackPath("/omnis/logs", "u1_sess-A")
	if !strings.HasSuffix(got, "agent_feedback_u1_sess-A.json") {
		t.Errorf("FeedbackPath = %q", got)
	}
}
