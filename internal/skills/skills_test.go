package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestToolsetCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")

	ts, err := Toolset(context.Background(), dir)
	if err != nil {
		t.Fatalf("Toolset() error = %v", err)
	}
	if ts == nil {
		t.Fatal("Toolset() returned nil toolset")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("skills directory missing after Toolset(): stat=%v err=%v", st, err)
	}
}