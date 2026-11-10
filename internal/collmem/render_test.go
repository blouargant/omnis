package collmem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// setUpdated rewrites the "updated:" line of a stored memory file.
func setUpdated(t *testing.T, s *Store, collection, name, date string) {
	t.Helper()
	path := filepath.Join(s.Dir(collection), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^updated: .*$`)
	out := re.ReplaceAllString(string(data), "updated: "+date)
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderAnsweringRoot(t *testing.T) {
	s := In(t.TempDir())
	s.Save("Proxy", Memory{Name: "no-force-push", Description: "Never force-push", Type: TypeFeedback, Body: "Rebase instead.\n**Why:** lost commits."})
	s.Save("Proxy", Memory{Name: "release-date", Description: "Release freeze on 2026-12-01", Type: TypeProject, Body: "LONG PROJECT BODY"})
	out := s.Render("Proxy", "Proxy", false)
	for _, want := range []string{`<collection-memory collection="Proxy">`, "</collection-memory>", "memory_save", "Rebase instead.",
		"### release-date — Release freeze on 2026-12-01", "LONG PROJECT BODY", headProject} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "Rebase instead.") > strings.Index(out, "LONG PROJECT BODY") {
		t.Error("feedback must come before project facts")
	}
}

// Over the budget, project and reference bodies are demoted before feedback,
// even when the feedback is older.
func TestRenderBudgetDemotesFactsBeforeCorrections(t *testing.T) {
	s := In(t.TempDir())
	big := strings.Repeat("x", 3900)
	s.Save("", Memory{Name: "rule", Description: "old correction", Type: TypeFeedback, Body: "RULE " + big})
	setUpdated(t, s, "", "rule", "2026-01-01")
	for i := 0; i < 3; i++ {
		s.Save("", Memory{Name: fmt.Sprintf("p%d", i), Description: fmt.Sprintf("fact %d", i), Type: TypeProject, Body: big})
		setUpdated(t, s, "", fmt.Sprintf("p%d", i), "2026-06-01")
	}
	out := s.Render("", "General", false)
	if len(out) > BlockBudgetChars {
		t.Fatalf("block is %d chars", len(out))
	}
	if !strings.Contains(out, "RULE ") {
		t.Errorf("the correction must stay inlined:\n%.400s", out)
	}
	if !strings.Contains(out, "- p0 — fact 0") {
		t.Error("a project fact must fall back to an index line first")
	}
}

func TestRenderEmptyCollectionStillCarriesRules(t *testing.T) {
	out := In(t.TempDir()).Render("", "General", false)
	if !strings.Contains(out, "memory_save") || !strings.Contains(out, `collection="General"`) {
		t.Fatalf("got:\n%s", out)
	}
}

func TestRenderRouter(t *testing.T) {
	s := In(t.TempDir())
	if s.Render("", "General", true) != "" {
		t.Fatal("router with no memories must get nothing")
	}
	s.Save("", Memory{Name: "no-force-push", Description: "Never force-push", Type: TypeFeedback, Body: "BODY"})
	out := s.Render("", "General", true)
	if !strings.Contains(out, "- no-force-push — Never force-push") || strings.Contains(out, "BODY") || strings.Contains(out, "memory_save") {
		t.Fatalf("router view wrong:\n%s", out)
	}
}

func TestRenderBudgetDemotesOldestBodies(t *testing.T) {
	s := In(t.TempDir())
	big := strings.Repeat("x", 3900)
	for i := 0; i < 6; i++ { // 6 × 3900 > 12000
		s.Save("", Memory{Name: fmt.Sprintf("f%d", i), Description: fmt.Sprintf("desc %d", i), Type: TypeFeedback, Body: big})
		// make "updated" ordering deterministic: older first
		setUpdated(t, s, "", fmt.Sprintf("f%d", i), fmt.Sprintf("2026-01-0%d", i+1))
	}
	out := s.Render("", "General", false)
	if len(out) > BlockBudgetChars {
		t.Fatalf("block is %d chars", len(out))
	}
	if !strings.Contains(out, "- f0 — desc 0") {
		t.Error("oldest must fall back to an index line")
	}
	if strings.Count(out, big) == 0 {
		t.Error("newest bodies must stay inlined")
	}
	if !strings.Contains(out, "memory_read") {
		t.Error("the block must say demoted memories can be read")
	}
	if !strings.Contains(out, DemotedNote) {
		t.Error("the block must say some memories were shortened")
	}
}

func TestRenderNoDemotionNoNote(t *testing.T) {
	s := In(t.TempDir())
	s.Save("", Memory{Name: "tabs", Description: "Use tabs", Type: TypeFeedback, Body: "short"})
	if out := s.Render("", "General", false); strings.Contains(out, DemotedNote) {
		t.Fatalf("nothing was demoted, the note must be absent:\n%s", out)
	}
}

// Render is cached per (directory, label, router flag) on the directory's
// entries; every kind of change must still show on the next Render.
func TestRenderCacheFollowsDirectoryChanges(t *testing.T) {
	s := In(t.TempDir())
	mustSave := func(m Memory) {
		t.Helper()
		if _, _, err := s.Save("Proxy", m); err != nil {
			t.Fatal(err)
		}
	}
	mustSave(Memory{Name: "tabs", Description: "Use tabs", Type: TypeFeedback, Body: "BODY-ONE"})
	if out := s.Render("Proxy", "Proxy", false); !strings.Contains(out, "BODY-ONE") {
		t.Fatalf("first render:\n%s", out)
	}
	// Router and answering renders of the same directory never share an entry.
	if out := s.Render("Proxy", "Proxy", true); strings.Contains(out, "BODY-ONE") || !strings.Contains(out, "- tabs — Use tabs") {
		t.Fatalf("router render:\n%s", out)
	}
	if out := s.Render("Proxy", "Other", false); !strings.Contains(out, `collection="Other"`) {
		t.Fatalf("label must key the cache:\n%s", out)
	}

	// Save (new memory and update).
	mustSave(Memory{Name: "spaces", Description: "No spaces", Type: TypeFeedback, Body: "BODY-TWO"})
	mustSave(Memory{Name: "tabs", Description: "Use tabs", Type: TypeFeedback, Body: "BODY-ONE-UPDATED"})
	out := s.Render("Proxy", "Proxy", false)
	if !strings.Contains(out, "BODY-TWO") || !strings.Contains(out, "BODY-ONE-UPDATED") {
		t.Fatalf("after Save:\n%s", out)
	}

	// Delete.
	if _, err := s.Delete("Proxy", "spaces"); err != nil {
		t.Fatal(err)
	}
	if out := s.Render("Proxy", "Proxy", false); strings.Contains(out, "BODY-TWO") {
		t.Fatalf("after Delete:\n%s", out)
	}

	// Out-of-band edit that changes the size.
	path := filepath.Join(s.Dir("Proxy"), "tabs.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "BODY-ONE-UPDATED", "EDITED-BY-HAND-LONGER", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := s.Render("Proxy", "Proxy", false); !strings.Contains(out, "EDITED-BY-HAND-LONGER") {
		t.Fatalf("after size-changing edit:\n%s", out)
	}

	// Out-of-band edit of the same size: only the mtime tells it apart.
	same := strings.Replace(edited, "EDITED-BY-HAND-LONGER", "SAME-SIZE-EDIT-ABCDEF", 1)
	if len(same) != len(edited) {
		t.Fatal("test setup: sizes differ")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(same), 0o644); err != nil {
		t.Fatal(err)
	}
	later := st.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if out := s.Render("Proxy", "Proxy", false); !strings.Contains(out, "SAME-SIZE-EDIT-ABCDEF") {
		t.Fatalf("after same-size edit:\n%s", out)
	}
}
