package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestToolsetCreatesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)

	ts, err := Toolset(context.Background(), nil)
	if err != nil {
		t.Fatalf("Toolset() error = %v", err)
	}
	if ts == nil {
		t.Fatal("Toolset() returned nil toolset")
	}
	registryDir := filepath.Join(home, "registry", "skills")
	if st, err := os.Stat(registryDir); err != nil || !st.IsDir() {
		t.Fatalf("skills registry directory missing after Toolset(): stat=%v err=%v", st, err)
	}
}
