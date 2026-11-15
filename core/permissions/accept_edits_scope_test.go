package permissions

import (
	"os"
	"path/filepath"
	"testing"
)

// The shipped config auto-allows edits in the session's working directory
// (acceptEdits), but only there: most sessions' working dir is the user's home,
// so a plain acceptEdits would let an agent rewrite ~/.bashrc or
// ~/.omnis/permissions.json (granting itself rights) without a card.
func TestShippedAcceptEditsStaysInTheWorkingDir(t *testing.T) {
	cfg, err := Load("../../config/permissions.json")
	if err != nil {
		t.Fatal(err)
	}
	// Not under /tmp: the shipped Edit(//tmp/**) allow rule covers it anyway.
	proj, outside := nonTmpDir(t), nonTmpDir(t)
	if err := os.Mkdir(filepath.Join(proj, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(proj, "escape")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		tool string
		args map[string]any
		cwd  string
		want Decision
	}{
		{"file in the working dir", "Write", file("resume.md"), proj, DecisionAllow},
		{"absolute path in the working dir", "Write", file(filepath.Join(proj, "sub", "x.md")), proj, DecisionAllow},
		{"new sub-directory", "Write", file("a/b/c.md"), proj, DecisionAllow},
		{"edit in the working dir", "Edit", file("sub/x.go"), proj, DecisionAllow},
		{"hidden file", "Write", file(".bashrc"), proj, DecisionAsk},
		{"file in a hidden directory", "Edit", file(".git/config"), proj, DecisionAsk},
		{"omnis config under home", "Write", file(".omnis/permissions.json"), proj, DecisionAsk},
		{"parent directory", "Write", file("../x.md"), proj, DecisionAsk},
		{"other directory", "Write", file(filepath.Join(outside, "x.md")), proj, DecisionAsk},
		{"symlink out of the working dir", "Write", file("escape/x.md"), proj, DecisionAsk},
		{"working dir is the filesystem root", "Write", file("/srv/x.md"), "/", DecisionAsk},
		{"denied file stays denied", "Write", file("/etc/shadow"), "/etc", DecisionDeny},
		{"mkdir in the working dir", "Bash", bash("mkdir -p out/docs"), proj, DecisionAllow},
		{"cp inside the working dir", "Bash", bash("cp a.md sub/b.md"), proj, DecisionAllow},
		{"cp to a dotfile", "Bash", bash("cp a ~/.bashrc"), proj, DecisionAsk},
		{"mv out of the working dir", "Bash", bash("mv a /etc/x"), proj, DecisionAsk},
		{"ln to a path outside", "Bash", bash("ln -s /etc/passwd p"), proj, DecisionAsk},
		{"cp with a variable", "Bash", bash("cp a $HOME/x"), proj, DecisionAsk},
	}
	for _, c := range cases {
		if d, r := cfg.CheckArgs(c.tool, c.args, c.cwd); d != c.want {
			t.Errorf("%s: want %v got %v (%s)", c.name, c.want, d, r)
		}
	}
}

func nonTmpDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	d, err := os.MkdirTemp(wd, "acceptedits-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}
