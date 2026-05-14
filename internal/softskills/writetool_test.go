package softskills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const goodContent = `---
name: demo
description: A demo soft-skill used by tests.
---

# Demo

Body.
`

func TestCreateRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	out, _ := w.create(nil, createIn{Name: "../escape", Content: goodContent})
	if !strings.HasPrefix(out.Result, "Error") {
		t.Fatalf("expected error, got %q", out.Result)
	}
}

func TestCreateRejectsBadName(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	for _, bad := range []string{"", "Bad-Caps", "no_underscore", "-leading", "trailing-", "with space"} {
		out, _ := w.create(nil, createIn{Name: bad, Content: goodContent})
		if !strings.HasPrefix(out.Result, "Error") {
			t.Errorf("name %q: expected error, got %q", bad, out.Result)
		}
	}
}

func TestCreateThenCollision(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	out, _ := w.create(nil, createIn{Name: "demo", Content: goodContent})
	if strings.HasPrefix(out.Result, "Error") {
		t.Fatalf("first create failed: %s", out.Result)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "SKILL.md")); err != nil {
		t.Fatalf("file missing: %v", err)
	}
	out2, _ := w.create(nil, createIn{Name: "demo", Content: goodContent})
	if !strings.Contains(out2.Result, "already exists") {
		t.Errorf("expected collision error, got %q", out2.Result)
	}
}

func TestCreateValidatesContent(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"no-frontmatter", "# Just a heading\n", "frontmatter"},
		{"missing-name", "---\ndescription: x\n---\nbody", "frontmatter"},
		{"name-mismatch", "---\nname: other\ndescription: x\n---\nbody", "must equal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := w.create(nil, createIn{Name: "demo", Content: tc.content})
			if !strings.Contains(out.Result, tc.want) {
				t.Errorf("got %q, want substring %q", out.Result, tc.want)
			}
		})
	}
}

func TestUpdateRequiresReason(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	_, _ = w.create(nil, createIn{Name: "demo", Content: goodContent})
	out, _ := w.update(nil, updateIn{Name: "demo", Content: goodContent + "\n# More\n", Reason: "short"})
	if !strings.Contains(out.Result, "reason") {
		t.Errorf("expected reason error, got %q", out.Result)
	}
}

func TestUpdateRejectsTrivialChange(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	_, _ = w.create(nil, createIn{Name: "demo", Content: goodContent})
	// whitespace-only diff in the body
	trivial := goodContent + "   \n\n\n"
	out, _ := w.update(nil, updateIn{
		Name:    "demo",
		Content: trivial,
		Reason:  "added meaningful detail about edge cases",
	})
	if !strings.Contains(out.Result, "trivial") {
		t.Errorf("expected trivial rejection, got %q", out.Result)
	}
}

func TestUpdateAcceptsRealChange(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	_, _ = w.create(nil, createIn{Name: "demo", Content: goodContent})
	improved := goodContent + "\n## Constraints\n- Avoid foo when bar is true.\n- Always confirm baz before zap.\n"
	out, _ := w.update(nil, updateIn{
		Name:    "demo",
		Content: improved,
		Reason:  "added constraints discovered in production session",
	})
	if !strings.HasPrefix(out.Result, "updated") {
		t.Errorf("expected update success, got %q", out.Result)
	}
}

func TestIndexAppendCreatesSection(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	skillDir := filepath.Join(root, "rollout-debug")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: rollout-debug\ndescription: test\n---\n"), 0o644)
	out, _ := w.appendIndex(nil, indexIn{Category: "kubernetes", Name: "rollout-debug", Summary: "Diagnose stuck deployments"})
	if !strings.Contains(out.Result, "appended") {
		t.Fatalf("got %q", out.Result)
	}
	body, _ := os.ReadFile(filepath.Join(root, "INDEX.md"))
	s := string(body)
	if !strings.Contains(s, "### kubernetes") || !strings.Contains(s, "rollout-debug") {
		t.Errorf("INDEX.md missing entry:\n%s", s)
	}
}

func TestIndexAppendIdempotent(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	skillDir := filepath.Join(root, "demo")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: test\n---\n"), 0o644)
	in := indexIn{Category: "meta", Name: "demo", Summary: "first"}
	_, _ = w.appendIndex(nil, in)
	out, _ := w.appendIndex(nil, in)
	if !strings.Contains(out.Result, "already listed") {
		t.Errorf("expected idempotent message, got %q", out.Result)
	}
	body, _ := os.ReadFile(filepath.Join(root, "INDEX.md"))
	if c := strings.Count(string(body), "**demo**"); c != 1 {
		t.Errorf("entry duplicated: %d occurrences\n%s", c, string(body))
	}
}

func TestIndexAppendConcurrent(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	const n = 8
	for i := 0; i < n; i++ {
		name := "skill-" + string(rune('a'+i))
		skillDir := filepath.Join(root, name)
		_ = os.MkdirAll(skillDir, 0o755)
		_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: test\n---\n"), 0o644)
	}
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			name := "skill-" + string(rune('a'+i))
			_, _ = w.appendIndex(nil, indexIn{Category: "race", Name: name, Summary: "concurrent"})
		}()
	}
	wg.Wait()
	body, _ := os.ReadFile(filepath.Join(root, "INDEX.md"))
	s := string(body)
	for i := 0; i < n; i++ {
		want := "**skill-" + string(rune('a'+i)) + "**"
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
}

func TestEnsureInsideRejectsAbsolute(t *testing.T) {
	root := t.TempDir()
	w := &writer{root: root}
	if err := w.ensureInside("/etc/passwd"); err == nil {
		t.Error("expected rejection of absolute path outside root")
	}
}
