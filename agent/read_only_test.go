package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

func (f fakeToolCtx) Value(any) any { return nil }

func toolsByName(ts []tool.Tool) map[string]tool.Tool {
	m := map[string]tool.Tool{}
	for _, t := range ts {
		m[t.Name()] = t
	}
	return m
}

func TestReadOnlyAgentLosesProjectChangingTools(t *testing.T) {
	in := []tool.Tool{
		echoTool(t, "Bash", "shell"), echoTool(t, "Read", "read"), echoTool(t, "Write", "write"),
		echoTool(t, "Edit", "edit"), echoTool(t, "revert", "revert"), echoTool(t, "lsp_rename", "rename"),
		echoTool(t, "ast_grep_rewrite", "rewrite"), echoTool(t, "worktree_create", "wt"),
		echoTool(t, "run_tests", "tests"), echoTool(t, "lsp_references", "refs"),
	}
	if got := applyReadOnly(RuntimeAgentConfig{Name: "x"}, in, nil); len(got) != len(in) {
		t.Fatal("an agent that is not read-only must keep every tool")
	}
	got := toolsByName(applyReadOnly(RuntimeAgentConfig{Name: "reviewer", ReadOnly: true}, in, nil))
	for _, gone := range []string{"revert", "lsp_rename", "ast_grep_rewrite", "worktree_create", "run_tests"} {
		if got[gone] != nil {
			t.Errorf("%s kept on a read-only agent", gone)
		}
	}
	for _, kept := range []string{"Bash", "Read", "Write", "Edit", "lsp_references"} {
		if got[kept] == nil {
			t.Errorf("%s missing", kept)
		}
	}
	if !strings.Contains(got["Bash"].Description(), "READ-ONLY") {
		t.Error("Bash was not replaced by the read-only Bash")
	}
	if _, ok := got["Write"].(*scratchOnlyTool); !ok {
		t.Error("Write is not limited to scratch paths")
	}
}

func TestScratchOnlyWriteRefusesProjectPaths(t *testing.T) {
	proj, err := os.MkdirTemp(".", "ro-proj-")
	if err != nil {
		t.Fatal(err)
	}
	proj, _ = filepath.Abs(proj)
	defer os.RemoveAll(proj)
	type writeIn struct {
		FilePath string `json:"file_path,omitempty"`
		Files    []any  `json:"files,omitempty"`
	}
	inner, err := functiontool.New(functiontool.Config{Name: "Write", Description: "write"},
		func(_ adk.ToolContext, in writeIn) (map[string]any, error) {
			return map[string]any{"name": "Write"}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	w := &scratchOnlyTool{inner: inner.(runnableTool)}

	out, err := w.Run(fakeToolCtx{sid: "s"}, map[string]any{"file_path": filepath.Join(proj, "main.go")})
	if err != nil || !strings.Contains(out["result"].(string), "refused") {
		t.Fatalf("project write not refused: %v %v", out, err)
	}
	out, err = w.Run(fakeToolCtx{sid: "s"}, map[string]any{"files": []any{
		map[string]any{"file_path": filepath.Join(t.TempDir(), "ok.txt")},
		map[string]any{"file_path": filepath.Join(proj, "main.go")},
	}})
	if err != nil || !strings.Contains(out["result"].(string), "refused") {
		t.Fatalf("a MultiEdit touching one project file must be refused: %v %v", out, err)
	}
	out, err = w.Run(fakeToolCtx{sid: "s"}, map[string]any{"file_path": filepath.Join(t.TempDir(), "notes.md")})
	if err != nil || out["name"] != "Write" {
		t.Fatalf("a scratch write must go through: %v %v", out, err)
	}
}

// A shipped agent that SAYS it is read-only and holds a tool able to change a
// project must BE read_only. The first Harbor smoke caught the reviewer —
// "read-only, never modifies code" — running sed -i and git stash in the
// project; code_scout's instruction even noted that "some tool groups expose
// edit operations". The description is a promise; read_only keeps it.
func TestShippedReadOnlyClaimsAreEnforced(t *testing.T) {
	writers := map[string]bool{"Bash": true, "Write": true, "Edit": true, "MultiEdit": true, "revert": true,
		"fs": true, "lsp": true, "astgrep": true, "worktree": true, "bg": true, "tests": true}
	claims := []string{"read-only", "read only", "never edits", "does not modify", "never modif"}
	for _, name := range shippedAgentNames(t) {
		var e struct {
			Description string   `json:"description"`
			Tools       []string `json:"tools"`
			ReadOnly    bool     `json:"read_only"`
		}
		if err := json.Unmarshal([]byte(readShippedAgentJSON(t, name)), &e); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// The first sentence is where an agent says what IT is; later ones may
		// describe what it delegates ("delegating read-only kubectl evidence
		// gathering to …").
		desc := strings.ToLower(e.Description)
		if i := strings.Index(desc, ". "); i >= 0 {
			desc = desc[:i]
		}
		claimed := false
		for _, c := range claims {
			if strings.Contains(desc, c) {
				claimed = true
			}
		}
		canWrite := false
		for _, tl := range e.Tools {
			if writers[tl] {
				canWrite = true
			}
		}
		if claimed && canWrite && !e.ReadOnly {
			t.Errorf("%s claims to be read-only and holds a writing tool, but is not read_only", name)
		}
	}
}
