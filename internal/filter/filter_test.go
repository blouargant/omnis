package filter

import (
	"path/filepath"
	"testing"
)

func TestApplyForCommandMatchesAndFilters(t *testing.T) {
	dir := t.TempDir()
	rules := `
name: "printf-head"
version: 1
match:
  command: "printf"
pipeline:
  - action: "head"
    n: 1
on_error: "passthrough"
`
	path := filepath.Join(dir, "printf.yaml")
	if err := writeFile(path, []byte(rules)); err != nil {
		t.Fatalf("write rules: %v", err)
	}

	filters, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	reg := NewRegistry(filters)

	out, applied, err := ApplyForCommand(reg, "printf 'a\\nb\\n'", "a\nb\n")
	if err != nil {
		t.Fatalf("ApplyForCommand() error = %v", err)
	}
	if !applied {
		t.Fatal("ApplyForCommand() applied = false, want true")
	}
	if out != "a\n+1 more lines\n" {
		t.Fatalf("ApplyForCommand() output = %q, want %q", out, "a\\n+1 more lines\\n")
	}
}

func TestApplyForCommandPassthroughWhenNoMatch(t *testing.T) {
	reg := NewRegistry(nil)
	out, applied, err := ApplyForCommand(reg, "printf hello", "hello")
	if err != nil {
		t.Fatalf("ApplyForCommand() error = %v", err)
	}
	if applied {
		t.Fatal("ApplyForCommand() applied = true, want false")
	}
	if out != "hello" {
		t.Fatalf("ApplyForCommand() output = %q, want hello", out)
	}
}
