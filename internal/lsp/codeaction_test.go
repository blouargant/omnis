package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestActionKindMatches checks the LSP kind-hierarchy matching used to keep a
// round to exactly the requested code-action kind.
func TestActionKindMatches(t *testing.T) {
	cases := []struct {
		act, want string
		match     bool
	}{
		{"source.organizeImports", "source.organizeImports", true},
		{"source.organizeImports.go", "source.organizeImports", true}, // sub-kind
		{"quickfix", "quickfix", true},
		{"quickfix.import", "quickfix", true},
		{"source.fixAll", "quickfix", false},                         // different family
		{"refactor.inline", "quickfix", false},                       // unrelated
		{"", "quickfix", false},                                      // unkinded never matches
		{"source.organizeImportsX", "source.organizeImports", false}, // no dot boundary
	}
	for _, c := range cases {
		if got := actionKindMatches(c.act, c.want); got != c.match {
			t.Errorf("actionKindMatches(%q,%q)=%v, want %v", c.act, c.want, got, c.match)
		}
	}
}

// TestParseCodeActions decodes the (Command | CodeAction)[] union: a real code
// action with an edit, a resolvable action with only data, and a bare Command.
func TestParseCodeActions(t *testing.T) {
	raw := json.RawMessage(`[
		{"title":"Organize Imports","kind":"source.organizeImports","edit":{"changes":{}}},
		{"title":"Add import","kind":"quickfix","data":{"x":1}},
		{"title":"Run go vet","command":"gopls.run_vet","arguments":[]}
	]`)
	got := parseCodeActions(raw)
	if len(got) != 3 {
		t.Fatalf("parseCodeActions returned %d actions, want 3", len(got))
	}
	if got[0].Edit == nil || got[0].Kind != "source.organizeImports" {
		t.Errorf("action 0 not decoded as an edit action: %+v", got[0])
	}
	if got[1].Edit != nil || len(got[1].Data) == 0 {
		t.Errorf("action 1 should be resolvable (data, no edit): %+v", got[1])
	}
	if got[2].Kind != "" || got[2].Edit != nil {
		t.Errorf("bare command should surface with no kind/edit: %+v", got[2])
	}
	if s := parseCodeActions(json.RawMessage(`null`)); s != nil {
		t.Errorf("null result should parse to nil, got %v", s)
	}
	if s := parseCodeActions(json.RawMessage(`[]`)); s != nil {
		t.Errorf("empty result should parse to nil, got %v", s)
	}
}

// TestWholeFileRange checks the range spans the whole file, ending at the last
// line's UTF-16 length (surrogate pairs count as two units).
func TestWholeFileRange(t *testing.T) {
	r := wholeFileRange("ab\ncde\n")
	if r.Start != (Position{0, 0}) {
		t.Errorf("start = %+v, want {0,0}", r.Start)
	}
	if r.End != (Position{Line: 2, Character: 0}) { // trailing "" after final \n
		t.Errorf("end = %+v, want {2,0}", r.End)
	}
	r2 := wholeFileRange("x := \"😀\"") // no trailing newline; emoji is 2 UTF-16 units
	if r2.End.Line != 0 || r2.End.Character != utf16Len("x := \"😀\"") {
		t.Errorf("single-line end = %+v, want line 0 char %d", r2.End, utf16Len("x := \"😀\""))
	}
}

// TestFlattenWorkspaceEdit covers both the changes-map and documentChanges forms.
func TestFlattenWorkspaceEdit(t *testing.T) {
	we := &WorkspaceEdit{
		Changes: map[DocumentURI][]TextEdit{
			"file:///a.go": {{NewText: "x"}},
		},
		DocumentChanges: json.RawMessage(
			`[{"textDocument":{"uri":"file:///b.go","version":1},"edits":[{"newText":"y"},{"newText":"z"}]}]`),
	}
	got := flattenWorkspaceEdit(we)
	if len(got["file:///a.go"]) != 1 || len(got["file:///b.go"]) != 2 {
		t.Errorf("flatten wrong: %+v", got)
	}
	if flattenWorkspaceEdit(nil) != nil {
		t.Error("flattenWorkspaceEdit(nil) should be nil")
	}
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"a", "b", "a", "c", "b"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("dedupe = %v, want [a b c]", got)
	}
}

// TestCodeActionOrganizeImports drives source.organizeImports against real gopls:
// a file with an unused import must have it removed and then type-check clean.
func TestCodeActionOrganizeImports(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP code-action test")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("go.mod", "module catest\n\ngo 1.21\n")
	// "strings" is imported but unused → gopls offers source.organizeImports.
	src := write("a.go", "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc main() { fmt.Println(\"hi\") }\n")

	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ls, err := m.ResolveServer(ctx, src)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	actions, err := ls.RequestCodeActions(ctx, src, []string{"source.organizeImports"}, nil)
	if err != nil {
		t.Fatalf("code actions: %v", err)
	}
	var edits map[DocumentURI][]TextEdit
	for _, a := range actions {
		if actionKindMatches(a.Kind, "source.organizeImports") && a.Edit != nil {
			edits = flattenWorkspaceEdit(a.Edit)
			break
		}
	}
	if len(edits) == 0 {
		t.Fatalf("expected an organizeImports edit, got actions: %+v", actions)
	}

	// Apply (mirrors the tool's writeEditMap) and re-sync.
	for uri, tes := range edits {
		p := URIToPath(uri)
		data, _ := os.ReadFile(p)
		if err := os.WriteFile(p, []byte(applyTextEdits(string(data), tes)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for uri := range edits {
		m.NotifyChange(URIToPath(uri))
	}

	data, _ := os.ReadFile(src)
	if strings.Contains(string(data), "strings") {
		t.Errorf("unused import not removed:\n%s", data)
	}
	diags, err := ls.Diagnostics(ctx, src, 15*time.Second, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if hasErrorDiag(diags) {
		t.Errorf("expected clean diagnostics after organizeImports, got: %+v", diags)
	}
}
