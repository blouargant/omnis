package compress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendMemoryCreatesAndAppends(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "memory.md")
	if err := appendMemory(path, "first"); err != nil {
		t.Fatalf("appendMemory(first) error = %v", err)
	}
	if err := appendMemory(path, "second"); err != nil {
		t.Fatalf("appendMemory(second) error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "first\nsecond\n" {
		t.Fatalf("file contents = %q", got)
	}
}

func TestPluginAppliesDefaults(t *testing.T) {
	t.Parallel()

	p, wait, err := Plugin("compress", Config{})
	if err != nil {
		t.Fatalf("Plugin() error = %v", err)
	}
	if p == nil {
		t.Fatal("Plugin() returned nil plugin")
	}
	if wait == nil {
		t.Fatal("Plugin() returned nil wait function")
	}
	wait()
	if !strings.Contains(DefaultMemoryPath, ".agent_memory") {
		t.Fatalf("DefaultMemoryPath = %q, want default file name", DefaultMemoryPath)
	}
}