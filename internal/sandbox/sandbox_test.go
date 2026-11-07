package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The test binary is the program Wrap re-executes, so it must act as the
// helper too.
func TestMain(m *testing.M) {
	MaybeRunHelper()
	os.Exit(m.Run())
}

func runSandboxed(t *testing.T, dir string, writable []string, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Dir = dir
	if err := Wrap(cmd, writable); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReadOnlyCommandCannotTouchTheRepo(t *testing.T) {
	if !Available() {
		t.Skip("Landlock not available")
	}
	repo := t.TempDir()
	scratch := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writable := []string{scratch, "/dev"}

	// Reading, and writing to the scratch dir, work.
	out, err := runSandboxed(t, repo, writable, "cat main.go && echo probe > "+scratch+"/p.txt && cat "+scratch+"/p.txt && echo x > /dev/null")
	if err != nil || !strings.Contains(out, "package main") || !strings.Contains(out, "probe") {
		t.Fatalf("allowed operations failed: %v\n%s", err, out)
	}
	// A copy of the repo into the scratch dir is allowed — that is how a
	// reviewer runs probes.
	if out, err := runSandboxed(t, repo, writable, "cp -a . "+scratch+"/copy && echo y >> "+scratch+"/copy/main.go"); err != nil {
		t.Fatalf("copy into scratch failed: %v\n%s", err, out)
	}

	for _, script := range []string{
		"echo x >> main.go",
		"sed -i s/main/other/ main.go",
		"rm main.go",
		"touch new.go",
		"mkdir sub",
		"mv main.go moved.go",
		": > main.go",
	} {
		if _, err := runSandboxed(t, repo, writable, script); err == nil {
			t.Errorf("%q succeeded inside the read-only sandbox", script)
		}
	}
	b, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if string(b) != "package main\n" {
		t.Fatalf("repo file changed: %q", b)
	}
	if entries, _ := os.ReadDir(repo); len(entries) != 1 {
		t.Fatalf("repo gained or lost entries: %v", entries)
	}
}

func TestGitStashIsRefused(t *testing.T) {
	if !Available() {
		t.Skip("Landlock not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("1\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "init")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("uncommitted work\n"), 0o644)

	out, err := runSandboxed(t, repo, []string{t.TempDir(), "/dev"}, "git status --short && git diff --stat && git stash")
	if err == nil {
		t.Fatalf("git stash succeeded inside the sandbox:\n%s", out)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("read-only git commands should still run: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "uncommitted work\n" {
		t.Fatalf("the author's uncommitted work was touched: %q", b)
	}
}

func TestParseHelperArgs(t *testing.T) {
	w, c, ok := parseHelperArgs([]string{"--rw", "/tmp", "--rw", "/dev", "--", "/bin/sh", "-c", "ls"})
	if !ok || len(w) != 2 || len(c) != 3 || c[0] != "/bin/sh" {
		t.Fatalf("w=%v c=%v ok=%v", w, c, ok)
	}
	for _, bad := range [][]string{{}, {"--rw"}, {"--"}, {"/bin/sh"}} {
		if _, _, ok := parseHelperArgs(bad); ok {
			t.Errorf("%v accepted", bad)
		}
	}
}
