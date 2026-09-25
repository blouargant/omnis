package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/v2/tool/skilltoolset/skill"

	"github.com/blouargant/omnis/internal/paths"
)

func TestToolsetCreatesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)

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

// writeSkill creates <dir>/SKILL.md with the given frontmatter name.
func writeSkill(t *testing.T, dir, frontmatterName string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	body := "---\nname: " + frontmatterName + "\ndescription: A test skill.\n---\n# " + frontmatterName + "\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md in %q: %v", dir, err)
	}
}

// TestResilientSourceSkipsMalformedSkill reproduces the real-world failure
// where a skill directory's name does not match its SKILL.md frontmatter name
// (dir "iaparc" / name "iapcli"). The upstream ADK source fails the whole scan
// atomically; resilientSource must skip the bad skill and still surface the
// valid ones so the agent keeps working.
func TestResilientSourceSkipsMalformedSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)

	base := filepath.Join(home, "registry/skills")
	writeSkill(t, filepath.Join(base, "good"), "good")     // valid
	writeSkill(t, filepath.Join(base, "iaparc"), "iapcli") // dir != frontmatter name

	fsys := newMultiDirSkillsFS(paths.SkillsAllSearchDirs())
	ctx := context.Background()

	// Sanity: the plain upstream source fails atomically on the bad skill,
	// which is exactly the bug this wrapper fixes.
	if _, err := skill.NewFileSystemSource(fsys).ListFrontmatters(ctx); err == nil {
		t.Fatal("expected upstream ListFrontmatters to fail on the mismatched skill")
	}

	src := resilientSource{Source: skill.NewFileSystemSource(fsys), fsys: fsys}
	fms, err := src.ListFrontmatters(ctx)
	if err != nil {
		t.Fatalf("resilientSource.ListFrontmatters() error = %v, want nil", err)
	}
	names := make(map[string]bool, len(fms))
	for _, fm := range fms {
		names[fm.Name] = true
	}
	if !names["good"] {
		t.Errorf("valid skill %q missing from listing; got %v", "good", names)
	}
	if names["iapcli"] || names["iaparc"] {
		t.Errorf("malformed skill should have been skipped; got %v", names)
	}
}
