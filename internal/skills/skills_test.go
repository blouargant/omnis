package skills

import (
	"context"
	"os"
	"testing"

	"github.com/blouargant/yoke/internal/paths"
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
	// Verify the registry dir that Toolset() resolved and created/used exists.
	// With the 3-layer search chain the dir may be the system layer when it
	// pre-exists; the important property is that Toolset() always ensures the
	// resolved path is a valid directory.
	registryDir := paths.SkillsRegistryDir()
	if st, err := os.Stat(registryDir); err != nil || !st.IsDir() {
		t.Fatalf("skills registry directory missing after Toolset(): path=%q stat=%v err=%v", registryDir, st, err)
	}
}
