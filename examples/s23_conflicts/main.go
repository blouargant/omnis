// Component s23 — worktree merge conflict reporting (Phase 6 / s23). This
// programmatically creates two conflicting worktrees and shows the merge
// abort with conflicting files listed.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	wt "github.com/blouargant/yoke/internal/worktree"
)

func main() {
	repo, _ := os.Getwd()
	tmp, err := os.MkdirTemp("", "s23-conflict-")
	must(err)
	defer os.RemoveAll(tmp)

	// init a fresh repo to avoid touching the workspace.
	must(os.Chdir(tmp))
	run("git", "init", "-q", "-b", "main")
	run("git", "config", "user.email", "demo@example.com")
	run("git", "config", "user.name", "demo")
	must(os.WriteFile("file.txt", []byte("base\n"), 0o644))
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "base")

	// branch A and B both touch file.txt
	run("git", "checkout", "-q", "-b", "feat-a")
	must(os.WriteFile("file.txt", []byte("from A\n"), 0o644))
	run("git", "commit", "-q", "-am", "A change")
	run("git", "checkout", "-q", "main")
	run("git", "checkout", "-q", "-b", "feat-b")
	must(os.WriteFile("file.txt", []byte("from B\n"), 0o644))
	run("git", "commit", "-q", "-am", "B change")
	run("git", "checkout", "-q", "main")

	// merge A cleanly first, then attempt B → conflict.
	report, err := wt.Merge(tmp, "feat-a")
	must(err)
	fmt.Println(report)
	report, err = wt.Merge(tmp, "feat-b")
	if err != nil {
		fmt.Println("conflict path triggered as expected:", err)
	}
	fmt.Println(report)

	_ = filepath.SkipDir
	_ = repo
}

func run(name string, args ...string) {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	must(c.Run())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
