package registries

import (
	"sort"
	"testing"
)

func TestBelongsToForeignKind(t *testing.T) {
	cases := []struct {
		path string
		self string
		want bool
	}{
		// Commands view of a multi-kind marketplace (rooted at plugins/).
		{"architecture/commands/deploy.md", KindCommands, false},
		{"stack-toolkit/commands/build.md", KindCommands, false},
		{"architecture/agents/planner.md", KindCommands, true},
		{"stack-toolkit/agents/coder.md", KindCommands, true},
		{"stack-toolkit/skills/java/maven/SKILL.md", KindCommands, true},
		{"security-toolkit/skills/shannon-audit-methodology/references/x.md", KindCommands, true},
		// Agents view: its own files stay, others are foreign.
		{"architecture/agents/planner.md", KindAgents, false},
		{"architecture/agents/planner/agent.json", KindAgents, false},
		{"architecture/commands/deploy.md", KindAgents, true},
		{"stack-toolkit/skills/java/maven/SKILL.md", KindAgents, true},
		// MCP view: sibling markers must be foreign even without a kind dir.
		{"mcp/github/mcp.json", KindMCP, false},
		{"mcp-servers/github/mcp.json", KindMCP, false},
		{"architecture/agents/planner/agent.json", KindMCP, true},
		{"squads/team/squad.json", KindMCP, true},
		{"perm/set/permissions.json", KindMCP, true}, // marker wins even off-convention dir
		// Single-purpose layout (URL points straight at the kind dir): no kind
		// segments, so nothing is foreign.
		{"deploy.md", KindCommands, false},
		{"triage/repro.md", KindCommands, false},
		{"github/mcp.json", KindMCP, false},
	}
	for _, tc := range cases {
		if got := belongsToForeignKind(tc.path, tc.self); got != tc.want {
			t.Errorf("belongsToForeignKind(%q, %q) = %v, want %v", tc.path, tc.self, got, tc.want)
		}
	}
}

// fakeRef serves a fixed tree and a canned body for every file.
type fakeRef struct{ tree []TreeEntry }

func (f fakeRef) ProviderName() string { return "fake" }
func (f fakeRef) AutoName() string     { return "fake" }
func (f fakeRef) TreeEntries(string) ([]TreeEntry, error) {
	return f.tree, nil
}
func (f fakeRef) RawFile(string, string) ([]byte, int, error) {
	return []byte("---\ndescription: d\n---\nbody\n"), 200, nil
}
func (f fakeRef) DirFiles(string, string) ([]InstallableFile, error) { return nil, nil }

// TestBrowseCommandsFiltersForeignKinds reproduces the reported bug: a
// multi-kind marketplace repo must not list agent/skill/reference markdown under
// Commands.
func TestBrowseCommandsFiltersForeignKinds(t *testing.T) {
	tree := []TreeEntry{
		{Path: "architecture/commands/deploy.md", Type: "blob"},
		{Path: "chaps-capabilities/commands/audit.md", Type: "blob"},
		{Path: "stack-toolkit/commands/build.md", Type: "blob"},
		// Foreign — must be excluded:
		{Path: "architecture/agents/planner.md", Type: "blob"},
		{Path: "stack-toolkit/agents/coder.md", Type: "blob"},
		{Path: "stack-toolkit/skills/java/maven/SKILL.md", Type: "blob"},
		{Path: "security-toolkit/skills/shannon-audit-methodology/references/note.md", Type: "blob"},
	}
	got, err := BrowseCommands(fakeRef{tree: tree}, "", nil)
	if err != nil {
		t.Fatalf("BrowseCommands: %v", err)
	}
	var paths []string
	for _, c := range got {
		paths = append(paths, c.DirPath)
	}
	sort.Strings(paths)
	want := []string{
		"architecture/commands/deploy.md",
		"chaps-capabilities/commands/audit.md",
		"stack-toolkit/commands/build.md",
	}
	if len(paths) != len(want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("got %v, want %v", paths, want)
		}
	}
}
