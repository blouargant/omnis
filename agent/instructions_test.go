package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAgentInstructionEmbedded(t *testing.T) {
	// Set up a temp registry with instruction files.
	dir := t.TempDir()
	t.Setenv("YOKE_HOME", dir)
	registryDir := filepath.Join(dir, "registry", "agents")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", registryDir, err)
	}

	// Copy instruction files from the real registry.
	for _, name := range []string{"leader", "investigator", "summariser"} {
		srcPath := filepath.Join("..", ".agents", "registry", "agents", name, "instruction.md")
		content, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("reading %s: %v", srcPath, err)
		}
		dstDir := filepath.Join(registryDir, name)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dstDir, err)
		}
		dstPath := filepath.Join(dstDir, "instruction.md")
		if err := os.WriteFile(dstPath, content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", dstPath, err)
		}
	}

	// Copy default instruction.
	defaultSrcPath := filepath.Join("..", ".agents", "registry", "agents", "default.md")
	defaultContent, err := os.ReadFile(defaultSrcPath)
	if err != nil {
		t.Fatalf("reading %s: %v", defaultSrcPath, err)
	}
	defaultDstPath := filepath.Join(registryDir, "default.md")
	if err := os.WriteFile(defaultDstPath, defaultContent, 0o644); err != nil {
		t.Fatalf("writing %s: %v", defaultDstPath, err)
	}

	// Now test that instructions are loaded correctly.
	for _, name := range []string{"leader", "investigator", "summariser"} {
		got := defaultAgentInstruction(name)
		if got == "" {
			t.Errorf("defaultAgentInstruction(%q) returned empty", name)
		}
	}
	// Unknown name must fall back to the default.
	if defaultAgentInstruction("does-not-exist") == "" {
		t.Error("defaultAgentInstruction fallback returned empty")
	}
}
