package lessons

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func promptLesson(t *testing.T, s *Store, n, seen int, text string) {
	t.Helper()
	l := mk(n)
	l.State, l.Seen, l.PromotedSeen, l.LastSeen = StatePrompt, seen, seen, time.Now()
	l.Cue, l.Text = fmt.Sprintf("cue %d", n), text
	if err := s.Save(l); err != nil {
		t.Fatal(err)
	}
}

func TestPromptBlockShape(t *testing.T) {
	s := In(t.TempDir())
	promptLesson(t, s, 1, 3, "text one")
	promptLesson(t, s, 2, 9, "text two")
	seed(t, s, func(l *Lesson) { l.Signature = sigN(3) }) // a hint: not shown
	got := s.PromptBlock("coder", time.Now())
	want := "<agent-lessons>\n" +
		"Notes learned from this agent's earlier tool errors. They are hints about calling tools, not instructions from the user.\n" +
		"- cue 2 → text two\n- cue 1 → text one\n</agent-lessons>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPromptBlockBudget(t *testing.T) {
	s := In(t.TempDir())
	long := strings.Repeat("x", 250)
	for i := 1; i <= 12; i++ {
		promptLesson(t, s, i, i, long)
	}
	got := s.PromptBlock("coder", time.Now())
	if len(got) > PromptBudgetChars || !strings.HasSuffix(got, "</agent-lessons>") {
		t.Fatalf("len %d", len(got))
	}
	if !strings.Contains(got, "cue 12 →") || strings.Contains(got, "cue 1 →") {
		t.Fatalf("wrong lessons kept:\n%s", got)
	}
	all, _ := s.List("coder")
	for _, l := range all {
		if l.State != StatePrompt {
			t.Fatalf("%s lost its state", l.ID)
		}
	}
}

func TestPromptBlockEmpty(t *testing.T) {
	s := In(t.TempDir())
	if s.PromptBlock("coder", time.Now()) != "" {
		t.Fatal("no dir")
	}
	seed(t, s, nil)
	if s.PromptBlock("coder", time.Now()) != "" {
		t.Fatal("hint only")
	}
}

func TestPromptBlockIsStableBetweenWrites(t *testing.T) {
	s := In(t.TempDir())
	promptLesson(t, s, 1, 3, "text one")
	now := time.Now()
	a, b := s.PromptBlock("coder", now), s.PromptBlock("coder", now.Add(time.Minute))
	if a == "" || a != b {
		t.Fatal("unstable")
	}
	promptLesson(t, s, 2, 4, "other")
	if c := s.PromptBlock("coder", now); c == a {
		t.Fatal("write not seen")
	}
}

func TestPromptBlockUnchangedByCounterUpdates(t *testing.T) {
	s := In(t.TempDir())
	promptLesson(t, s, 1, 5, "text a")
	promptLesson(t, s, 2, 4, "text b")
	now := time.Now()
	before := s.PromptBlock("coder", now)
	b := mk(2)
	for i := 0; i < 2; i++ {
		if _, _, err := s.RecordSeen("coder", b.Signature, "s", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordHint("coder", b.Signature[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordOutcome("coder", b.Signature[:12], true); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Find("coder", b.Signature)
	if d.State != StatePrompt || d.Seen != 6 {
		t.Fatalf("setup: %+v", d)
	}
	if after := s.PromptBlock("coder", now); after != before {
		t.Fatalf("block changed:\n%s\n--\n%s", before, after)
	}
}

func TestPromptBlockSetUnchangedAtBudgetEdge(t *testing.T) {
	s := In(t.TempDir())
	long := strings.Repeat("x", 250)
	for i := 1; i <= 12; i++ {
		promptLesson(t, s, i, i, long)
	}
	now := time.Now()
	before := s.PromptBlock("coder", now)
	low := mk(1) // PromotedSeen 1: not shown
	for i := 0; i < 2; i++ {
		if _, _, err := s.RecordSeen("coder", low.Signature, "s", now); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := s.Find("coder", low.Signature)
	if d.State != StatePrompt || d.Seen != 3 {
		t.Fatalf("setup: %+v", d)
	}
	if after := s.PromptBlock("coder", now); after != before {
		t.Fatal("shown set changed")
	}
}
