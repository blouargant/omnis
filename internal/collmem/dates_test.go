package collmem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// at sets the clock used for memory dates until the test ends.
func at(t *testing.T, day string) {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	now = func() time.Time { return d }
	t.Cleanup(func() { now = old })
}

// The creation date survives updates; a confirmation counts, a change resets the
// count, an edit (UI) keeps it.
func TestSaveModesKeepDatesAndCountConfirmations(t *testing.T) {
	s := In(t.TempDir())
	m := Memory{Name: "tabs", Description: "Use tabs", Type: TypeFeedback, Body: "Indent with tabs."}

	at(t, "2026-10-01")
	if _, _, err := s.SaveWith("", m, SaveChange); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-10-05")
	s.SaveWith("", m, SaveConfirm)
	at(t, "2026-10-09")
	got, _, _ := s.SaveWith("", m, SaveConfirm)
	if got.Created != "2026-10-01" || got.Updated != "2026-10-09" || got.Confirmations != 2 || got.LastConfirmed != "2026-10-09" {
		t.Fatalf("after two confirmations: %+v", got)
	}
	if g, _ := s.Get("", "tabs"); g.Confirmations != 2 || g.Created != "2026-10-01" {
		t.Fatalf("not persisted: %+v", g)
	}

	at(t, "2026-10-12")
	got, _, _ = s.Save("", m) // UI edit
	if got.Confirmations != 2 || got.LastConfirmed != "2026-10-09" || got.Created != "2026-10-01" {
		t.Fatalf("an edit must keep the confirmations: %+v", got)
	}

	at(t, "2026-10-20")
	m.Body = "Indent with 4 spaces."
	got, _, _ = s.SaveWith("", m, SaveChange)
	if got.Confirmations != 0 || got.LastConfirmed != "" || got.Created != "2026-10-01" || got.Updated != "2026-10-20" {
		t.Fatalf("a change must reset the confirmations, keep created: %+v", got)
	}

	// confirms=true on a memory that does not exist yet creates it plainly.
	got, created, _ := s.SaveWith("", Memory{Name: "new", Type: TypeUser, Body: "b"}, SaveConfirm)
	if !created || got.Confirmations != 0 {
		t.Fatalf("confirm on a new memory: %v %+v", created, got)
	}
}

// A file written before the creation date existed reads its update date as
// its creation date.
func TestLegacyFileGetsCreatedFromUpdated(t *testing.T) {
	s := In(t.TempDir())
	dir := s.Dir("")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "old.md"), []byte("---\nname: old\ndescription: d\ntype: project\nupdated: 2026-09-01\n---\n\nbody\n"), 0o644)
	m, ok := s.Get("", "old")
	if !ok || m.Created != "2026-09-01" || m.Confirmations != 0 {
		t.Fatalf("legacy = %v %+v", ok, m)
	}
}

func TestRenderShowsDatesAndToday(t *testing.T) {
	s := In(t.TempDir())
	at(t, "2026-10-01")
	s.SaveWith("P", Memory{Name: "tabs", Description: "Use tabs", Type: TypeFeedback, Body: "B"}, SaveChange)
	at(t, "2026-10-08")
	s.SaveWith("P", Memory{Name: "tabs", Description: "Use tabs", Type: TypeFeedback, Body: "B"}, SaveConfirm)
	s.SaveWith("P", Memory{Name: "db", Description: "Staging DB", Type: TypeProject, Body: "stg2"}, SaveChange)

	at(t, "2026-11-17")
	out := s.Render("P", "P", false)
	for _, want := range []string{
		"### tabs — Use tabs (saved 2026-10-01, updated 2026-10-08, restated 1× (last 2026-10-08))",
		"### db — Staging DB (saved 2026-10-08)",
		"Today is 2026-11-17.",
		"the most recent is right",
		"confirms=true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("block lacks %q:\n%s", want, out)
		}
	}
	if r := s.Render("P", "P", true); !strings.Contains(r, "- tabs — Use tabs (saved 2026-10-01") || strings.Contains(r, "Today is") {
		t.Fatalf("router block:\n%s", r)
	}

	// A new day re-renders (cached per day).
	at(t, "2026-11-18")
	if out := s.Render("P", "P", false); !strings.Contains(out, "Today is 2026-11-18.") {
		t.Fatal("the cached block kept yesterday's date")
	}
}

// Over budget, a memory the user restated keeps its body longer than one never
// restated, whatever their age.
func TestDemotionPrefersRestatedMemories(t *testing.T) {
	s := In(t.TempDir())
	big := strings.Repeat("x", 3000)
	at(t, "2026-01-01")
	s.SaveWith("", Memory{Name: "old-confirmed", Description: "kept", Type: TypeProject, Body: "KEEP " + big}, SaveChange)
	s.SaveWith("", Memory{Name: "old-confirmed", Description: "kept", Type: TypeProject, Body: "KEEP " + big}, SaveConfirm)
	at(t, "2026-11-01")
	for i := 0; i < 4; i++ {
		s.SaveWith("", Memory{Name: fmt.Sprintf("p%d", i), Description: "d", Type: TypeProject, Body: big}, SaveChange)
	}
	out := s.Render("", "General", false)
	if !strings.Contains(out, "KEEP ") {
		t.Fatalf("the restated memory lost its body:\n%s", out[:400])
	}
}
