package agentmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLayerOrderingAndWalkUp(t *testing.T) {
	sys := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sys)
	t.Setenv("OMNIS_HOME", home)

	write(t, filepath.Join(sys, FileName), "SYSTEM")
	write(t, filepath.Join(home, FileName), "USER")
	// Project tree: repo root (has .git) + a nested subdir, AGENT.md in both.
	write(t, filepath.Join(proj, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(proj, FileName), "PROJECT_ROOT")
	sub := filepath.Join(proj, "a", "b")
	write(t, filepath.Join(sub, FileName), "PROJECT_SUB")

	got := Resolve(sub)
	for _, want := range []string{"SYSTEM", "USER", "PROJECT_ROOT", "PROJECT_SUB"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Ascending precedence: system < user < project-root < project-sub.
	order := []string{"SYSTEM", "USER", "PROJECT_ROOT", "PROJECT_SUB"}
	last := -1
	for _, m := range order {
		i := strings.Index(got, m)
		if i <= last {
			t.Fatalf("layer %q out of order in:\n%s", m, got)
		}
		last = i
	}
	if !strings.HasPrefix(got, "<project-context") || !strings.HasSuffix(got, "</project-context>") {
		t.Fatalf("missing container wrapper:\n%s", got)
	}
}

func TestResolveEmptyWhenNoFiles(t *testing.T) {
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", t.TempDir())
	t.Setenv("OMNIS_HOME", t.TempDir())
	if got := Resolve(t.TempDir()); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestResolveCacheInvalidatesOnChange(t *testing.T) {
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", t.TempDir())
	t.Setenv("OMNIS_HOME", t.TempDir())
	proj := t.TempDir()
	f := filepath.Join(proj, FileName)
	write(t, f, "ONE")
	if got := Resolve(proj); !strings.Contains(got, "ONE") {
		t.Fatalf("want ONE, got %q", got)
	}
	// Rewrite with different size; signature (size/mtime) must invalidate cache.
	write(t, f, "TWO TWO TWO")
	if got := Resolve(proj); !strings.Contains(got, "TWO") || strings.Contains(got, "ONE") {
		t.Fatalf("cache not invalidated, got %q", got)
	}
}

func TestAppendMemoryCreateAndAppend(t *testing.T) {
	proj := t.TempDir()
	// No .git → target is cwd/AGENT.md.
	path, err := AppendMemory(proj, "#  first note  ")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(proj, FileName) {
		t.Fatalf("unexpected path %q", path)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "## Notes") || !strings.Contains(string(data), "- first note") {
		t.Fatalf("first append wrong:\n%s", data)
	}
	if _, err := AppendMemory(proj, "second note"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "- first note") || !strings.Contains(string(data), "- second note") {
		t.Fatalf("second append wrong:\n%s", data)
	}
	// Only one Notes header.
	if strings.Count(string(data), "## Notes") != 1 {
		t.Fatalf("duplicate Notes header:\n%s", data)
	}
}

func TestAppendMemoryTargetsRepoRoot(t *testing.T) {
	proj := t.TempDir()
	write(t, filepath.Join(proj, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(proj, "x", "y")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := AppendMemory(sub, "note from subdir")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(proj, FileName) {
		t.Fatalf("want repo-root target, got %q", path)
	}
}

func TestAppendMemoryEmpty(t *testing.T) {
	if _, err := AppendMemory(t.TempDir(), "#   "); err == nil {
		t.Fatal("want error for empty memory")
	}
}
