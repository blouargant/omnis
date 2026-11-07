package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	c := exec.Command("git", "init", "-q")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrepRespectsGitignore(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, ".gitignore", "node_modules/\n")
	write(t, dir, "src/a.go", "needle here\n")
	write(t, dir, "node_modules/x/b.js", "needle there\n")
	write(t, dir, "bin.dat", "needle\x00\x01binary\n")

	out, _ := RunGrep(context.Background(), GrepIn{Pattern: "needle", Cwd: dir})
	if !strings.Contains(out, "src/a.go:1:needle here") {
		t.Fatalf("tracked-or-untracked file missing: %q", out)
	}
	if strings.Contains(out, "node_modules") || strings.Contains(out, "bin.dat") {
		t.Fatalf("ignored or binary file searched: %q", out)
	}
	// Naming an ignored path explicitly still searches it.
	out, _ = RunGrep(context.Background(), GrepIn{Pattern: "needle", Path: "node_modules", Cwd: dir})
	if !strings.Contains(out, "node_modules/x/b.js:1:needle there") {
		t.Fatalf("explicit ignored path not searched: %q", out)
	}
}

func TestGrepOutsideARepoFallsBackToGrep(t *testing.T) {
	dir := t.TempDir() // not a git repository
	write(t, dir, "a.txt", "alpha\nbeta\n")
	out, _ := RunGrep(context.Background(), GrepIn{Pattern: "beta", Cwd: dir})
	if !strings.Contains(out, "a.txt:2:beta") {
		t.Fatalf("got %q", out)
	}
}

func TestGrepMatchLimitAndLongLines(t *testing.T) {
	for _, repo := range []bool{true, false} {
		var dir string
		if repo {
			dir = gitRepo(t)
		} else {
			dir = t.TempDir()
		}
		var b strings.Builder
		for i := range 250 {
			fmt.Fprintf(&b, "hit %d\n", i)
		}
		b.WriteString("hit " + strings.Repeat("x", 2000) + "\n")
		write(t, dir, "many.txt", b.String())

		out, _ := RunGrep(context.Background(), GrepIn{Pattern: "hit", Cwd: dir})
		if n := strings.Count(out, "many.txt:"); n != grepDefaultLimit {
			t.Fatalf("repo=%v: %d matches, want %d", repo, n, grepDefaultLimit)
		}
		if !strings.Contains(out, "[100 matches limit reached. Use limit=200 for more") {
			t.Fatalf("repo=%v: no limit notice: %q", repo, out[len(out)-200:])
		}
		out, _ = RunGrep(context.Background(), GrepIn{Pattern: "x{100}", Cwd: dir, Limit: 1000})
		if !strings.Contains(out, "Some lines truncated to 500 chars") || len(out) > 800 {
			t.Fatalf("repo=%v: long line not cut (%d bytes)", repo, len(out))
		}
	}
}

func TestGrepContextGlobCaseAndLiteral(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "a.go", "one\nTwo(x)\nthree\n")
	write(t, dir, "sub/b.go", "two(y)\n")
	write(t, dir, "c.txt", "two(z)\n")

	out, _ := RunGrep(context.Background(), GrepIn{Pattern: "two(", Literal: true, IgnoreCase: true, Glob: "*.go", Context: 1, Cwd: dir})
	for _, want := range []string{"a.go-1-one", "a.go:2:Two(x)", "a.go-3-three", "sub/b.go:1:two(y)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "c.txt") {
		t.Fatalf("glob ignored: %q", out)
	}
	// A context line does not count towards the limit.
	out, _ = RunGrep(context.Background(), GrepIn{Pattern: "Two", Context: 1, Limit: 1, Cwd: dir})
	if !strings.Contains(out, "a.go-3-three") || strings.Contains(out, "limit reached. Use limit=2") == false {
		t.Fatalf("context/limit: %q", out)
	}
}

func TestGrepBadPatternIsReported(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "x\n")
	out, _ := RunGrep(context.Background(), GrepIn{Pattern: "(", Cwd: dir})
	if !strings.HasPrefix(out, "Error:") {
		t.Fatalf("got %q", out)
	}
}

func TestGrepArgAliases(t *testing.T) {
	args := map[string]any{"pattern": "x", "file_path": "src", "ignoreCase": true, "include": "*.go", "max_results": 5.0, "-C": 2.0}
	RepairToolArgs("Grep", args)
	for k, v := range map[string]any{"path": "src", "ignore_case": true, "glob": "*.go", "limit": 5.0, "context": 2.0} {
		if args[k] != v {
			t.Fatalf("%s = %v, want %v (args %v)", k, args[k], v, args)
		}
	}
}
