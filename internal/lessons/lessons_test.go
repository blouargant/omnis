package lessons

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sigN(n int) string {
	return Signature("T", fmt.Sprintf("distinct error kind %c%c", 'a'+n%26, 'a'+n/26))
}

func mk(n int) Lesson {
	return Lesson{
		Agent: "coder", Tool: "Edit", Signature: sigN(n),
		Cue: "old_string not found", Text: "Read the file again before editing.",
		State: StateHint, Created: time.Unix(1700000000, 0).UTC(), LastSeen: time.Unix(1700000100, 0).UTC(),
	}
}

func TestSaveFindListDelete(t *testing.T) {
	s := In(t.TempDir())
	a := mk(1)
	a.Failed = "old_string:\n```go\nfmt.Println()\n```"
	a.Worked = "Read first\nthen Edit"
	a.Seen, a.Hinted, a.Recovered, a.Repeated, a.SeenInPrompt = 5, 4, 3, 2, 1
	a.SessionIDs = []string{"s1", "s2", "s1"}
	a.Outcomes = []bool{true, false, true}
	a.State = StatePrompt
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Find("coder", a.Signature)
	if !ok {
		t.Fatal("not found")
	}
	if got.ID != a.Signature[:12] {
		t.Errorf("id %q", got.ID)
	}
	if got.Tool != a.Tool || got.Cue != a.Cue || got.Text != a.Text || got.Failed != a.Failed ||
		got.Worked != a.Worked || got.State != a.State || got.Seen != 5 || got.Hinted != 4 ||
		got.Recovered != 3 || got.Repeated != 2 || got.SeenInPrompt != 1 {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if !got.Created.Equal(a.Created) || !got.LastSeen.Equal(a.LastSeen) {
		t.Errorf("times: %v %v", got.Created, got.LastSeen)
	}
	if strings.Join(got.SessionIDs, ",") != "s1,s2" {
		t.Errorf("sessions %v", got.SessionIDs)
	}
	if len(got.Outcomes) != 3 || !got.Outcomes[0] || got.Outcomes[1] || !got.Outcomes[2] {
		t.Errorf("outcomes %v", got.Outcomes)
	}

	b, c := mk(2), mk(3)
	b.Seen, c.Seen = 9, 5
	for _, l := range []Lesson{b, c} {
		if err := s.Save(l); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List("coder")
	if err != nil || len(list) != 3 {
		t.Fatalf("list %v %v", list, err)
	}
	if list[0].Seen != 9 {
		t.Errorf("first should be Seen 9: %+v", list[0])
	}
	if list[1].Seen != 5 || list[2].Seen != 5 || list[1].ID > list[2].ID {
		t.Errorf("tie must sort by ID: %v %v", list[1].ID, list[2].ID)
	}
	if err := s.Delete("coder", b.Signature[:12]); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Find("coder", b.Signature); ok {
		t.Fatal("still there")
	}
	if err := s.Delete("coder", b.Signature[:12]); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	root := t.TempDir()
	s := In(root)
	bad := map[string]func(l *Lesson){
		"empty cue":     func(l *Lesson) { l.Cue = "" },
		"empty text":    func(l *Lesson) { l.Text = " " },
		"newline cue":   func(l *Lesson) { l.Cue = "a\nb" },
		"newline text":  func(l *Lesson) { l.Text = "a\rb" },
		"long cue":      func(l *Lesson) { l.Cue = strings.Repeat("x", MaxLineRunes+1) },
		"long text":     func(l *Lesson) { l.Text = strings.Repeat("é", MaxLineRunes+1) },
		"unknown state": func(l *Lesson) { l.State = "weird" },
		"agent upper":   func(l *Lesson) { l.Agent = "Coder" },
		"agent slash":   func(l *Lesson) { l.Agent = "../x" },
		"agent empty":   func(l *Lesson) { l.Agent = "" },
		"bad signature": func(l *Lesson) { l.Signature = "zz" },
	}
	for name, f := range bad {
		l := mk(1)
		f(&l)
		if err := s.Save(l); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "lessons")); err == nil {
		t.Fatal("something was written")
	}
}

func TestSaveTruncatesWorked(t *testing.T) {
	s := In(t.TempDir())
	l := mk(1)
	l.Worked = strings.Repeat("é", MaxWorkedChars+50)
	if err := s.Save(l); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Find("coder", l.Signature)
	if n := len([]rune(got.Worked)); n != MaxWorkedChars {
		t.Fatalf("worked has %d runes", n)
	}
}

func TestMaxPerAgentReplacesLeastSeen(t *testing.T) {
	s := In(t.TempDir())
	for i := 0; i < MaxPerAgent; i++ {
		l := mk(i)
		l.Seen = 10 + i
		switch {
		case i == 0:
			l.State, l.Seen = StatePrompt, 1 // never removed
		case i == 1:
			l.State, l.Seen = StateHint, 2
		case i == 2:
			l.State, l.Seen = StateRetired, 50
		case i == 3:
			l.State, l.Seen = StateRetired, 7
		}
		if err := s.Save(l); err != nil {
			t.Fatal(err)
		}
	}
	// A retired lesson goes first, the lowest Seen one.
	n := mk(MaxPerAgent)
	n.Seen = 100
	if err := s.Save(n); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Find("coder", sigN(3)); ok {
		t.Fatal("lowest-Seen retired lesson should be gone")
	}
	if _, ok := s.Find("coder", sigN(2)); !ok {
		t.Fatal("other retired lesson must stay")
	}
	// Update of an existing lesson never evicts.
	up := mk(5)
	up.Seen = 99
	if err := s.Save(up); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List("coder"); len(l) != MaxPerAgent {
		t.Fatalf("len %d", len(l))
	}
	// Next: retired sn2 goes, then the hint with the lowest Seen.
	n1 := mk(MaxPerAgent + 1)
	n1.Seen = 100
	if err := s.Save(n1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Find("coder", sigN(2)); ok {
		t.Fatal("retired lesson should be gone")
	}
	n2 := mk(MaxPerAgent + 2)
	n2.Seen = 100
	if err := s.Save(n2); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Find("coder", sigN(1)); ok {
		t.Fatal("lowest-Seen hint should be gone")
	}
	if _, ok := s.Find("coder", sigN(0)); !ok {
		t.Fatal("prompt lesson must never be removed")
	}
}

func TestMaxPerAgentAllPromptIsFull(t *testing.T) {
	s := In(t.TempDir())
	for i := 0; i < MaxPerAgent; i++ {
		l := mk(i)
		l.State = StatePrompt
		if err := s.Save(l); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(mk(MaxPerAgent)); err == nil {
		t.Fatal("expected ErrFull")
	}
}

func TestCorruptFilesAreSkipped(t *testing.T) {
	root := t.TempDir()
	s := In(root)
	good := mk(1)
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "lessons", "coder")
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := mk(2)
	okFile, _ := os.ReadFile(filepath.Join(dir, good.Signature[:12]+".md"))
	write("aaaaaaaaaaaa.md", "no front matter here")
	// id differs from its stem
	write("bbbbbbbbbbbb.md", strings.Replace(string(okFile), good.Signature[:12], other.Signature[:12], 1))
	// unknown state
	if err := s.Save(other); err != nil {
		t.Fatal(err)
	}
	f, _ := os.ReadFile(filepath.Join(dir, other.Signature[:12]+".md"))
	write(other.Signature[:12]+".md", strings.Replace(string(f), "state: hint", "state: weird", 1))

	list, err := s.List("coder")
	if err != nil || len(list) != 1 || list[0].ID != good.Signature[:12] {
		t.Fatalf("list %v %v", list, err)
	}
	if _, ok := s.Find("coder", other.Signature); ok {
		t.Fatal("corrupt lesson must not be found")
	}
}

func TestConcurrentSavesOfOneLesson(t *testing.T) {
	root := t.TempDir()
	s := In(root)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := mk(1)
			l.Seen = i
			if err := s.Save(l); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	list, err := s.List("coder")
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "lessons", "coder"))
	if len(entries) != 1 {
		t.Fatalf("%d files (temp files left?)", len(entries))
	}
}
