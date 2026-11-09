package collmem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	for _, want := range []string{`<collection-memory collection="Proxy">`, "</collection-memory>", "memory_save", "Rebase instead.", "- release-date — Release freeze on 2026-12-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "LONG PROJECT BODY") {
		t.Error("project bodies must not be inlined")
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
}
