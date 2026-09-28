package softskills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSeedSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSeedSkill(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSeedBuiltinsCopiesMissing(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeSeedSkill(t, src, "wrap-session", "v1")
	if err := os.WriteFile(filepath.Join(src, "INDEX.md"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readSeedSkill(t, dst, "wrap-session"); got != "v1" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "INDEX.md")); err != nil {
		t.Fatalf("INDEX.md not seeded: %v", err)
	}
}

func TestSeedBuiltinsNeverOverwritesUserCopy(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeSeedSkill(t, src, "wrap-session", "shipped")
	writeSeedSkill(t, dst, "wrap-session", "mine")
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readSeedSkill(t, dst, "wrap-session"); got != "mine" {
		t.Fatalf("user copy overwritten: %q", got)
	}
}

func TestSeedBuiltinsRespectsDeletion(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeSeedSkill(t, src, "wrap-session", "v1")
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dst, "wrap-session")); err != nil {
		t.Fatal(err)
	}
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "wrap-session")); !os.IsNotExist(err) {
		t.Fatalf("deleted soft-skill was re-seeded (err=%v)", err)
	}
}

func TestSeedBuiltinsRefreshesUntouchedCopy(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeSeedSkill(t, src, "wrap-session", "v1")
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	writeSeedSkill(t, src, "wrap-session", "v2")
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readSeedSkill(t, dst, "wrap-session"); got != "v2" {
		t.Fatalf("untouched copy not refreshed: %q", got)
	}
}

func TestSeedBuiltinsKeepsEditedSeededCopy(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeSeedSkill(t, src, "wrap-session", "v1")
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	writeSeedSkill(t, dst, "wrap-session", "edited")
	writeSeedSkill(t, src, "wrap-session", "v2")
	if err := SeedBuiltins(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readSeedSkill(t, dst, "wrap-session"); got != "edited" {
		t.Fatalf("edited copy overwritten: %q", got)
	}
}

func TestSeedBuiltinsMissingSourceIsNoop(t *testing.T) {
	dst := t.TempDir()
	if err := SeedBuiltins(filepath.Join(dst, "absent"), dst); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dst); len(entries) != 0 {
		t.Fatalf("expected empty dst, got %d entries", len(entries))
	}
}

// The shipped tree must seed cleanly and yield loadable soft-skills.
func TestShippedBuiltinsSeed(t *testing.T) {
	dst := t.TempDir()
	if err := SeedBuiltins(filepath.Join("..", "..", "softskills"), dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wrap-session", "how-the-library-works"} {
		if _, err := os.Stat(filepath.Join(dst, name, "SKILL.md")); err != nil {
			t.Errorf("%s not seeded: %v", name, err)
		}
	}
}
