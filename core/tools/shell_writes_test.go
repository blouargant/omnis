package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blouargant/omnis/core/adk"
)

func enableTracking(t *testing.T) {
	t.Helper()
	EnableShellWriteTracking(true)
	t.Cleanup(func() { EnableShellWriteTracking(false) })
}

// runTracked runs a command the way the Bash handlers do, tracked under id.
func runTracked(t *testing.T, id, command, dir string) {
	t.Helper()
	finish := trackShellWrites(id, command, dir)
	if _, _, err := runBashStatus(context.Background(), BashIn{Command: command, Cwd: dir}); err != nil {
		t.Logf("runBashStatus: %v", err)
	}
	finish()
}

func TestShellWritesCreatedAndModified(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	runTracked(t, "sw-created", "echo a > new.txt", dir)
	want := []ShellWrite{{Path: filepath.Join(dir, "new.txt"), Created: true}}
	if got := ShellWrites("sw-created"); !reflect.DeepEqual(got, want) {
		t.Fatalf("created: got %v, want %v", got, want)
	}
	old := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(old, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTracked(t, "sw-modified", "echo b >> old.txt", dir)
	want = []ShellWrite{{Path: old, Created: false}}
	if got := ShellWrites("sw-modified"); !reflect.DeepEqual(got, want) {
		t.Fatalf("modified: got %v, want %v", got, want)
	}
}

func TestShellWritesNothingWritten(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	runTracked(t, "sw-none-1", "ls > /dev/null", dir)
	runTracked(t, "sw-none-2", "false && echo x > never.txt", dir)
	for _, id := range []string{"sw-none-1", "sw-none-2"} {
		if got := ShellWrites(id); got != nil {
			t.Fatalf("%s: got %v, want nil", id, got)
		}
	}
}

func TestShellWritesCreatedThenRemoved(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	runTracked(t, "sw-rm", "echo a > f.txt && rm f.txt", dir)
	if got := ShellWrites("sw-rm"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestShellWritesDirectoryAndSymlink(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	runTracked(t, "sw-dir", "echo x > sub 2>/dev/null; true", dir)
	if got := ShellWrites("sw-dir"); got != nil {
		t.Fatalf("directory: got %v, want nil", got)
	}
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	runTracked(t, "sw-link", "echo b >> link.txt", dir)
	want := []ShellWrite{{Path: link, Created: false}}
	if got := ShellWrites("sw-link"); !reflect.DeepEqual(got, want) {
		t.Fatalf("symlink: got %v, want %v", got, want)
	}
}

func TestShellWritesTakeRemoves(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	runTracked(t, "sw-take", "echo a > t.txt", dir)
	if ShellWrites("sw-take") == nil || ShellWrites("sw-take") == nil {
		t.Fatal("ShellWrites must not remove the entry")
	}
	if TakeShellWrites("sw-take") == nil {
		t.Fatal("TakeShellWrites returned nil")
	}
	if got := TakeShellWrites("sw-take"); got != nil {
		t.Fatalf("second take: %v", got)
	}
	if got := ShellWrites("sw-take"); got != nil {
		t.Fatalf("after take: %v", got)
	}
}

func TestShellWritesOff(t *testing.T) {
	EnableShellWriteTracking(false)
	dir := t.TempDir()
	runTracked(t, "sw-off", "echo a > off.txt", dir)
	if got := ShellWrites("sw-off"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	RecordShellWrites("sw-off2", []ShellWrite{{Path: "/x", Created: true}})
	if got := ShellWrites("sw-off2"); got != nil {
		t.Fatalf("record while off: got %v", got)
	}
}

func TestShellWritesEmptyCallID(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	runTracked(t, "", "echo a > e.txt", dir)
	if got := ShellWrites(""); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestShellWritesStoreBounded(t *testing.T) {
	enableTracking(t)
	for i := 0; i < 1100; i++ {
		RecordShellWrites(fmt.Sprintf("sw-bound-%d", i), []ShellWrite{{Path: "/x", Created: true}})
	}
	if n := shellWriteCount(); n > 1024 {
		t.Fatalf("%d entries, want at most 1024", n)
	}
	if ShellWrites("sw-bound-0") != nil {
		t.Fatal("oldest entry still present")
	}
	if ShellWrites("sw-bound-1099") == nil {
		t.Fatal("newest entry missing")
	}
}

func TestShellWritesHeredocText(t *testing.T) {
	enableTracking(t)
	dir := t.TempDir()
	runTracked(t, "sw-heredoc", "cat > notes.md <<'EOF'\nrun `echo x > other.txt`\nEOF", dir)
	want := []ShellWrite{{Path: filepath.Join(dir, "notes.md"), Created: true}}
	if got := ShellWrites("sw-heredoc"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The Bash handler must not ask the tool context for its call id while
// tracking is off (a mock context panics on FunctionCallID).
func TestTrackBashWritesOffDoesNotReadCallID(t *testing.T) {
	EnableShellWriteTracking(false)
	finish := trackBashWrites(panickyCallID{}, "echo a > x.txt", t.TempDir())
	finish()
}

func TestTrackBashWritesRecoversFromCallIDPanic(t *testing.T) {
	enableTracking(t)
	finish := trackBashWrites(panickyCallID{}, "echo a > x.txt", t.TempDir())
	finish()
}

type panickyCallID struct{ adk.ToolContext }

func (panickyCallID) FunctionCallID() string { panic("not implemented") }
