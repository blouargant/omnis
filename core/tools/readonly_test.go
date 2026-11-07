package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sandbox"
)

// projectDir makes a directory outside the scratch roots (the default temp
// dir is writable for a read-only agent, so t.TempDir cannot stand in for a
// project).
func projectDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(".", "readonly-project-")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(d)
	t.Cleanup(func() { os.RemoveAll(abs) })
	return abs
}

func TestReadOnlyBashCannotModifyTheProject(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("Landlock not available")
	}
	proj := projectDir(t)
	if err := os.WriteFile(filepath.Join(proj, "a.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(cmd string) string {
		out, _ := RunBash(context.Background(), BashIn{Command: cmd, Cwd: proj, ReadOnly: true})
		return out
	}
	if out := run("cat a.txt"); !strings.Contains(out, "original") {
		t.Fatalf("read failed: %q", out)
	}
	for _, cmd := range []string{"echo x >> a.txt", "sed -i s/original/changed/ a.txt", "rm a.txt", "touch b.txt", "mkdir d"} {
		out := run(cmd + " && echo SUCCEEDED")
		if strings.Contains(out, "SUCCEEDED") {
			t.Errorf("%q succeeded: %q", cmd, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "a.txt")); string(b) != "original\n" {
		t.Fatalf("project file changed: %q", b)
	}
	if entries, _ := os.ReadDir(proj); len(entries) != 1 {
		t.Fatalf("project gained or lost entries: %v", entries)
	}
	scratch := t.TempDir()
	if out := run("cp a.txt " + scratch + "/copy.txt && echo ok"); !strings.Contains(out, "ok") {
		t.Fatalf("writing to the scratch dir failed: %q", out)
	}
	// The same command without ReadOnly does write (the test proves the
	// sandbox, not a read-only directory).
	if out, _ := RunBash(context.Background(), BashIn{Command: "touch b.txt && echo ok", Cwd: proj}); !strings.Contains(out, "ok") {
		t.Fatalf("control write failed: %q", out)
	}
}

func TestUnderWritable(t *testing.T) {
	proj := projectDir(t)
	tmp := t.TempDir()
	if !UnderWritable("", filepath.Join(tmp, "x", "y.txt")) {
		t.Error("a path under the temp dir must be writable")
	}
	if UnderWritable(proj, "main.go") || UnderWritable("", filepath.Join(proj, "main.go")) {
		t.Error("a project path must not be writable")
	}
	link := filepath.Join(tmp, "into-project")
	if err := os.Symlink(proj, link); err == nil && UnderWritable("", filepath.Join(link, "main.go")) {
		t.Error("a symlink from the temp dir into the project must not count as writable")
	}
	if UnderWritable(tmp, "../../../../../../../../etc/passwd") {
		t.Error("a path climbing out of the temp dir must not be writable")
	}
}
