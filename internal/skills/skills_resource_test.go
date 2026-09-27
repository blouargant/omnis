package skills

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/adk/v2/tool/skilltoolset/skill"
)

// writeFile creates dir/rel (and its parents) with body.
func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resourceSkillSource builds a resilientSource over one skill laid out the way
// many real skills are: detail files at the ROOT and in arbitrary subfolders,
// not only under references/ — the layout ADK's own source refuses to serve.
func resourceSkillSource(t *testing.T) resilientSource {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, "iaparc")
	writeSkill(t, skillDir, "iaparc")
	writeFile(t, skillDir, "projects.md", "projects doc")
	writeFile(t, skillDir, "apply/hub.md", "apply hub doc")
	writeFile(t, skillDir, "references/legacy.md", "references doc")
	writeFile(t, root, "other/secret.md", "not part of the skill")
	fsys := os.DirFS(root)
	return resilientSource{Source: skill.NewFileSystemSource(fsys), fsys: fsys}
}

func readResource(t *testing.T, src resilientSource, rel string) (string, error) {
	t.Helper()
	rc, err := src.LoadResource(context.Background(), "iaparc", rel)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return string(b), err
}

// A skill's detail files are readable wherever they sit inside the skill
// directory. ADK alone only serves references/, assets/ and scripts/, so an
// agent loading the iaparc skill could read SKILL.md but none of the per-group
// files it points to, and guessed commands that do not exist.
func TestLoadResourceAnywhereInsideTheSkill(t *testing.T) {
	src := resourceSkillSource(t)
	for rel, want := range map[string]string{
		"projects.md":          "projects doc",
		"apply/hub.md":         "apply hub doc",
		"./apply/hub.md":       "apply hub doc",
		"references/legacy.md": "references doc",
	} {
		got, err := readResource(t, src, rel)
		if err != nil || got != want {
			t.Errorf("LoadResource(%q) = %q, %v; want %q", rel, got, err, want)
		}
	}
}

// Nothing outside the skill directory is reachable.
func TestLoadResourceRefusesEscapes(t *testing.T) {
	src := resourceSkillSource(t)
	for _, rel := range []string{
		"../other/secret.md",
		"apply/../../other/secret.md",
		"/etc/passwd",
		"",
		".",
	} {
		if _, err := readResource(t, src, rel); !errors.Is(err, skill.ErrInvalidResourcePath) {
			t.Errorf("LoadResource(%q) error = %v, want ErrInvalidResourcePath", rel, err)
		}
	}
	if _, err := readResource(t, src, "missing.md"); !errors.Is(err, skill.ErrResourceNotFound) {
		t.Errorf("missing file: error = %v, want ErrResourceNotFound", err)
	}
	if _, err := src.LoadResource(context.Background(), "nope", "projects.md"); !errors.Is(err, skill.ErrSkillNotFound) {
		t.Errorf("unknown skill: error = %v, want ErrSkillNotFound", err)
	}
}

// Listing the root shows every file of the skill except SKILL.md itself.
func TestListResourcesWholeSkill(t *testing.T) {
	src := resourceSkillSource(t)
	got, err := src.ListResources(context.Background(), "iaparc", "")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{"apply/hub.md", "projects.md", "references/legacy.md"}
	if !slices.Equal(got, want) {
		t.Errorf("ListResources = %v, want %v", got, want)
	}
	sub, err := src.ListResources(context.Background(), "iaparc", "apply")
	if err != nil || !slices.Equal(sub, []string{"apply/hub.md"}) {
		t.Errorf("ListResources(apply) = %v, %v", sub, err)
	}
	if _, err := src.ListResources(context.Background(), "iaparc", "../other"); !errors.Is(err, skill.ErrInvalidResourcePath) {
		t.Errorf("ListResources(../other) error = %v, want ErrInvalidResourcePath", err)
	}
}
