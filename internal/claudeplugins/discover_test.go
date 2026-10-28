package claudeplugins

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtureRoot = "testdata/agentskills/claude"

func TestDiscoverReadsTheDemoPlugin(t *testing.T) {
	ps, warns := Discover(fixtureRoot)
	// Only "demo" loads: "escape" leaves the root, "remote" is not a local path.
	if len(ps) != 1 || ps[0].Name != "demo" {
		t.Fatalf("plugins = %+v", ps)
	}
	if len(warns) != 2 {
		t.Fatalf("want 2 warnings (escape, remote), got %v", warns)
	}
	if !strings.Contains(warns[0], "escape") || !strings.Contains(warns[1], "remote") {
		t.Errorf("warnings should name the skipped entries in marketplace order: %v", warns)
	}

	p := ps[0]
	if p.Version != "1.2.3" || p.Description == "" {
		t.Errorf("plugin metadata = %+v", p)
	}
	if !filepath.IsAbs(p.Dir) {
		t.Errorf("Dir %q is not absolute", p.Dir)
	}
	if p.SkillsDir != filepath.Join(p.Dir, "skills") {
		t.Errorf("SkillsDir = %q", p.SkillsDir)
	}
	if p.HooksPath != filepath.Join(p.Dir, "hooks", "hooks.json") {
		t.Errorf("HooksPath = %q", p.HooksPath)
	}
	if !reflect.DeepEqual(p.SkillNames, []string{"demo-skill"}) {
		t.Errorf("SkillNames = %v", p.SkillNames)
	}

	var names []string
	for _, a := range p.Agents {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, []string{"lead", "reader", "worker"}) {
		t.Fatalf("agents (want sorted by name) = %v", names)
	}
	lead, reader, worker := p.Agents[0], p.Agents[1], p.Agents[2]

	if want := []string{"Bash", "Read", "Agent", "AskUserQuestion", "SendMessage"}; !reflect.DeepEqual(lead.Tools, want) {
		t.Errorf("lead tools = %v, want %v", lead.Tools, want)
	}
	if lead.ModelAlias != "sonnet" {
		t.Errorf("lead ModelAlias = %q", lead.ModelAlias)
	}
	for _, want := range []string{"demo:worker", "demo:reader", "other:automation", "${CLAUDE_PLUGIN_ROOT}/skills/demo-skill/"} {
		if !strings.Contains(lead.Body, want) {
			t.Errorf("lead body lost %q: %q", want, lead.Body)
		}
	}
	if lead.Description == "" {
		t.Error("lead description is empty")
	}

	if want := []string{"Bash", "Read", "Write", "Edit", "MultiEdit", "NotebookEdit"}; !reflect.DeepEqual(worker.Tools, want) {
		t.Errorf("worker tools = %v, want %v", worker.Tools, want)
	}
	if worker.ModelAlias != "haiku" {
		t.Errorf("worker ModelAlias = %q", worker.ModelAlias)
	}

	if len(reader.Tools) != 0 {
		t.Errorf("reader declares no tools, got %v", reader.Tools)
	}
	if reader.ModelAlias != "opus" || !reflect.DeepEqual(reader.Skills, []string{"demo-skill"}) {
		t.Errorf("reader = %+v", reader)
	}
}

func TestDiscoverMissingRootIsEmpty(t *testing.T) {
	ps, warns := Discover(filepath.Join(t.TempDir(), "nope"))
	if ps != nil || warns != nil {
		t.Fatalf("missing root: plugins=%v warnings=%v, want nil, nil", ps, warns)
	}
}

func TestDiscoverSkipsMalformedAgent(t *testing.T) {
	root := copyFixture(t)
	bad := filepath.Join(root, "demo", "agents", "bad.md")
	if err := os.WriteFile(bad, []byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, warns := Discover(root)
	if len(ps) != 1 || len(ps[0].Agents) != 3 {
		t.Fatalf("demo must still load its 3 good agents: %+v", ps)
	}
	if len(warns) != 3 {
		t.Fatalf("want 3 warnings (escape, remote, bad.md), got %v", warns)
	}
	var found bool
	for _, w := range warns {
		if strings.Contains(w, "bad.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning names bad.md: %v", warns)
	}
}

func TestDiscoverMalformedMarketplace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, warns := Discover(root)
	if len(ps) != 0 || len(warns) != 1 {
		t.Fatalf("plugins=%v warnings=%v, want none and 1 warning", ps, warns)
	}
}

// A plugin name is used as a namespace and a path segment downstream, so one
// that does not look like a slug is refused rather than sanitised.
func TestDiscoverRefusesBadPluginName(t *testing.T) {
	root := copyFixture(t)
	pj := filepath.Join(root, "demo", ".claude-plugin", "plugin.json")
	if err := os.WriteFile(pj, []byte(`{"name":"../Demo Plugin"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, warns := Discover(root)
	if len(ps) != 0 {
		t.Fatalf("a plugin named %q must be refused: %+v", "../Demo Plugin", ps)
	}
	if len(warns) != 3 {
		t.Fatalf("want 3 warnings, got %v", warns)
	}
}

// With no plugin.json the marketplace entry's own name stands in.
func TestDiscoverFallsBackToMarketplaceName(t *testing.T) {
	root := copyFixture(t)
	if err := os.Remove(filepath.Join(root, "demo", ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	ps, _ := Discover(root)
	if len(ps) != 1 || ps[0].Name != "demo" {
		t.Fatalf("plugins = %+v", ps)
	}
}

// Two marketplace entries that end up with the same plugin name would clash on
// every `<plugin>:<agent>` downstream, so only the first is kept.
func TestDiscoverKeepsOnlyTheFirstOfDuplicateNames(t *testing.T) {
	root := copyFixture(t)
	mp := `{"plugins":[{"name":"demo","source":"./demo"},{"name":"again","source":"./demo"}]}`
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte(mp), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, warns := Discover(root)
	if len(ps) != 1 || ps[0].Name != "demo" {
		t.Fatalf("plugins = %+v", ps)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "again") {
		t.Fatalf("want one warning naming the duplicate entry, got %v", warns)
	}
}

// A symlink inside the root that points outside it must not be followed.
func TestDiscoverRefusesSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mp := `{"plugins":[{"name":"link","source":"./link"}]}`
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte(mp), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, warns := Discover(root)
	if len(ps) != 0 || len(warns) != 1 {
		t.Fatalf("plugins=%v warnings=%v, want the symlink escape refused", ps, warns)
	}
}

// copyFixture copies the shared fixture into a temp dir (preserving file
// modes) so a test can mutate it.
func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(fixtureRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
