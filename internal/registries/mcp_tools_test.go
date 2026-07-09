package registries

import (
	"sort"
	"testing"
)

// mapRef serves a per-path body so a browse test can mix real manifests with
// unrelated files. Every listed path is a blob.
type mapRef struct{ files map[string]string }

func (m mapRef) ProviderName() string { return "fake" }
func (m mapRef) AutoName() string     { return "fake" }
func (m mapRef) TreeEntries(string) ([]TreeEntry, error) {
	out := make([]TreeEntry, 0, len(m.files))
	for p := range m.files {
		out = append(out, TreeEntry{Path: p, Type: "blob"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (m mapRef) RawFile(relPath, _ string) ([]byte, int, error) {
	if b, ok := m.files[relPath]; ok {
		return []byte(b), 200, nil
	}
	return nil, 404, nil
}
func (m mapRef) DirFiles(string, string) ([]InstallableFile, error) { return nil, nil }

// TestBrowseMCPToolsRequiresManifest reproduces the reported bug: a directory
// whose only JSON is unrelated metadata (no command/url) must NOT be listed as
// an MCP server, while real manifests (mcp.json, mcp.md, or a differently-named
// json that declares a transport) are listed, and another kind's markers are
// excluded.
func TestBrowseMCPToolsRequiresManifest(t *testing.T) {
	ref := mapRef{files: map[string]string{
		// Not an MCP manifest — group/plugin metadata (the reported case):
		"architecture/plugin.json": `{"name":"architecture","description":"arch plugin"}`,
		// Another kind's marker under its own dir — must be excluded:
		"architecture/agents/planner/agent.json": `{"name":"planner"}`,
		// Real MCP servers:
		"mcp/tokensave/mcp.json": `{"name":"tokensave","command":"tokensave"}`,
		"mcp/fetch/mcp.md":       "---\nname: fetch\ncommand: uvx\nargs:\n  - mcp-server-fetch\n---\nDocs.\n",
		// Non-standard filename but a genuine http server (has url):
		"integrations/github/server.json": `{"name":"github","type":"http","url":"https://example/mcp"}`,
	}}

	got, err := BrowseMCPTools(ref, "", nil)
	if err != nil {
		t.Fatalf("BrowseMCPTools: %v", err)
	}
	var names []string
	for _, tl := range got {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	want := []string{"fetch", "github", "tokensave"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}
