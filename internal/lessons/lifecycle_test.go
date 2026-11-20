package lessons

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func seed(t *testing.T, s *Store, mut func(*Lesson)) Lesson {
	t.Helper()
	l := mk(1)
	if mut != nil {
		mut(&l)
	}
	if err := s.Save(l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestPromoteAfterThreeSeenInTwoSessions(t *testing.T) {
	s := In(t.TempDir())
	l := seed(t, s, nil)
	var got Lesson
	for i := 0; i < 3; i++ {
		var found bool
		var err error
		got, found, err = s.RecordSeen("coder", l.Signature, "s1", t0)
		if err != nil || !found {
			t.Fatal(found, err)
		}
	}
	if got.State != StateHint || got.Seen != 3 {
		t.Fatalf("one session: %+v", got)
	}
	got, _, _ = s.RecordSeen("coder", l.Signature, "s2", t0)
	if got.State != StatePrompt || got.Seen != 4 || got.SeenInPrompt != 0 {
		t.Fatalf("two sessions: %+v", got)
	}
	disk, _ := s.Find("coder", l.Signature)
	if disk.State != StatePrompt {
		t.Fatal("not persisted")
	}
}

func TestRetireWhenHintsDoNotHelp(t *testing.T) {
	for _, c := range []struct {
		out  []bool
		want State
	}{
		{[]bool{false, false, true, false}, StateRetired},
		{[]bool{true, false, true, false, true}, StateHint},
	} {
		s := In(t.TempDir())
		l := seed(t, s, nil)
		id := l.Signature[:12]
		var got Lesson
		for _, o := range c.out {
			var err error
			if got, err = s.RecordOutcome("coder", id, o); err != nil {
				t.Fatal(err)
			}
		}
		if got.State != c.want {
			t.Fatalf("%v: state %s want %s", c.out, got.State, c.want)
		}
	}
}

func TestRecordHintAndMissing(t *testing.T) {
	s := In(t.TempDir())
	l := seed(t, s, nil)
	if err := s.RecordHint("coder", l.Signature[:12]); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Find("coder", l.Signature); d.Hinted != 1 {
		t.Fatal(d.Hinted)
	}
	if err := s.RecordHint("coder", "000000000000"); err == nil {
		t.Fatal("want error")
	}
	if _, found, err := s.RecordSeen("coder", sigN(9), "s", t0); found || err != nil {
		t.Fatal(found, err)
	}
}

func TestRetiredIsNeverSeenAgain(t *testing.T) {
	s := In(t.TempDir())
	l := seed(t, s, func(l *Lesson) { l.State = StateRetired; l.Seen = 5 })
	got, found, err := s.RecordSeen("coder", l.Signature, "s9", t0)
	if err != nil || !found || got.State != StateRetired || got.Seen != 6 || !got.LastSeen.Equal(t0) {
		t.Fatalf("%+v %v %v", got, found, err)
	}
}

func TestDemoteWhenPromptDoesNotPrevent(t *testing.T) {
	s := In(t.TempDir())
	l := seed(t, s, func(l *Lesson) {
		l.State = StatePrompt
		l.Seen, l.SeenInPrompt = 5, DemoteSeenInPrompt-1
		l.SessionIDs = []string{"a", "b"}
	})
	got, _, _ := s.RecordSeen("coder", l.Signature, "c", t0)
	if got.State != StateHint || !got.PromptFailed {
		t.Fatalf("%+v", got)
	}
	got, _, _ = s.RecordSeen("coder", l.Signature, "d", t0)
	if got.State != StateHint {
		t.Fatal("promoted again")
	}
	if d, _ := s.Find("coder", l.Signature); !d.PromptFailed {
		t.Fatal("PromptFailed not persisted")
	}
}

func TestDemoteAfterSixtyIdleDays(t *testing.T) {
	s := In(t.TempDir())
	l := seed(t, s, func(l *Lesson) {
		l.State = StatePrompt
		l.Seen, l.SessionIDs = 4, []string{"a", "b"}
		l.LastSeen = t0
	})
	if s.PromptBlock("coder", t0.Add(30*24*time.Hour)) == "" {
		t.Fatal("should be shown at 30 days")
	}
	if b := s.PromptBlock("coder", t0.Add(61*24*time.Hour)); b != "" {
		t.Fatalf("should be omitted: %q", b)
	}
	d, _ := s.Find("coder", l.Signature)
	if d.State != StateHint {
		t.Fatalf("state %s", d.State)
	}
	got, _, _ := s.RecordSeen("coder", l.Signature, "a", t0.Add(62*24*time.Hour))
	if got.State != StatePrompt {
		t.Fatalf("not promoted again: %+v", got)
	}
}

func TestSessionIDsAreDistinctAndBounded(t *testing.T) {
	s := In(t.TempDir())
	l := seed(t, s, nil)
	var got Lesson
	for _, sid := range []string{"a", "a", "b", "a"} {
		got, _, _ = s.RecordSeen("coder", l.Signature, sid, t0)
	}
	if len(got.SessionIDs) != 2 {
		t.Fatalf("%v", got.SessionIDs)
	}
	for _, sid := range []string{"c", "d", "e", "f", "g", "h", "i", "j"} {
		got, _, _ = s.RecordSeen("coder", l.Signature, sid, t0)
	}
	if len(got.SessionIDs) != maxSessionIDs || got.SessionIDs[maxSessionIDs-1] != "j" {
		t.Fatalf("%v", got.SessionIDs)
	}
	for _, old := range []string{"a", "b"} {
		for _, k := range got.SessionIDs {
			if k == old {
				t.Fatalf("%s should have been trimmed: %v", old, got.SessionIDs)
			}
		}
	}
}

func TestRecordOnMissingOrUnwritableStore(t *testing.T) {
	s := In(filepath.Join(t.TempDir(), "nope"))
	if _, found, err := s.RecordSeen("coder", sigN(1), "s", t0); found || err != nil {
		t.Fatal(found, err)
	}
	if _, err := s.RecordOutcome("coder", "000000000000", true); err == nil {
		t.Fatal("want error")
	}
	if s.PromptBlock("coder", t0) != "" || s.PromptBlock("Bad Agent", t0) != "" {
		t.Fatal("want empty")
	}
	// unwritable directory
	root := t.TempDir()
	s = In(root)
	l := seed(t, s, nil)
	dir := filepath.Join(root, "lessons", "coder")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if os.Geteuid() != 0 {
		if _, _, err := s.RecordSeen("coder", l.Signature, "s", t0); err == nil {
			t.Fatal("want write error")
		}
	}
}
