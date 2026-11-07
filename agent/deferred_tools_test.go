package agent

import (
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/toolconfirmation"

	"github.com/blouargant/omnis/core/adk"
)

type echoIn struct {
	X string `json:"x"`
}

func echoTool(t *testing.T, name, desc string) tool.Tool {
	t.Helper()
	ft, err := functiontool.New(functiontool.Config{Name: name, Description: desc},
		func(_ adk.ToolContext, in echoIn) (map[string]any, error) { return map[string]any{"name": name}, nil })
	if err != nil {
		t.Fatal(err)
	}
	return ft
}

// fakeToolCtx is the minimum of a tool context the deferral reads.
type fakeToolCtx struct {
	adk.ToolContext
	sid string
}

func (f fakeToolCtx) SessionID() string { return f.sid }

func (f fakeToolCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }

func declared(req *model.LLMRequest) map[string]bool {
	out := map[string]bool{}
	if req.Config == nil {
		return out
	}
	for _, t := range req.Config.Tools {
		for _, fd := range t.FunctionDeclarations {
			out[fd.Name] = true
		}
	}
	return out
}

func buildReq(t *testing.T, tools []tool.Tool, sid string) *model.LLMRequest {
	t.Helper()
	req := &model.LLMRequest{}
	for _, tl := range tools {
		rp, ok := tl.(interface {
			ProcessRequest(adk.ToolContext, *model.LLMRequest) error
		})
		if !ok {
			t.Fatalf("%s has no ProcessRequest", tl.Name())
		}
		if err := rp.ProcessRequest(fakeToolCtx{sid: sid}, req); err != nil {
			t.Fatal(err)
		}
	}
	return req
}

func TestDeferredToolsAreCallableButUndeclaredUntilLoaded(t *testing.T) {
	tools := applyDeferredTools([]string{"lsp_*", "worktree_create"}, []tool.Tool{
		echoTool(t, "Read", "read a file"),
		echoTool(t, "lsp_rename", "rename a symbol across the workspace"),
		echoTool(t, "lsp_hover", "type information for a symbol"),
		echoTool(t, "worktree_create", "create a git worktree"),
	})
	req := buildReq(t, tools, "s1")
	d := declared(req)
	if !d["Read"] || !d[toolSearchName] {
		t.Fatalf("Read and tool_search must be declared: %v", d)
	}
	for _, n := range []string{"lsp_rename", "lsp_hover", "worktree_create"} {
		if d[n] {
			t.Errorf("%s declared before being loaded", n)
		}
		if req.Tools[n] == nil {
			t.Errorf("%s not dispatchable", n)
		}
	}

	// tool_search("rename") loads lsp_rename only, for this session only.
	var search tool.Tool
	for _, tl := range tools {
		if tl.Name() == toolSearchName {
			search = tl
		}
	}
	out, err := search.(runnableTool).Run(fakeToolCtx{sid: "s1"}, map[string]any{"query": "rename"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded, _ := out["loaded"].([]any); len(loaded) != 1 {
		t.Fatalf("loaded = %v, want only lsp_rename", out["loaded"])
	}
	d = declared(buildReq(t, tools, "s1"))
	if !d["lsp_rename"] || d["lsp_hover"] {
		t.Fatalf("after search: %v", d)
	}
	if declared(buildReq(t, tools, "s2"))["lsp_rename"] {
		t.Fatal("a load in one session leaked into another")
	}

	// Calling a deferred tool by name runs it and loads it.
	if _, err := req.Tools["worktree_create"].(runnableTool).Run(fakeToolCtx{sid: "s1"}, map[string]any{"x": "1"}); err != nil {
		t.Fatal(err)
	}
	if !declared(buildReq(t, tools, "s1"))["worktree_create"] {
		t.Fatal("a direct call did not load the tool")
	}
}

func TestNoDeferralIsANoOp(t *testing.T) {
	in := []tool.Tool{echoTool(t, "Read", "read")}
	if out := applyDeferredTools(nil, in); len(out) != 1 {
		t.Fatalf("got %d tools", len(out))
	}
	if out := applyDeferredTools([]string{"lsp_*"}, in); len(out) != 1 || out[0].Name() != "Read" {
		t.Fatal("no matching tool must leave the list unchanged and add no tool_search")
	}
}

func TestSearchDeferredRanksNamesFirst(t *testing.T) {
	set := &deferredSet{}
	for _, n := range [][2]string{
		{"lsp_references", "find every reference to a symbol"},
		{"lsp_rename", "rename a symbol"},
		{"worktree_create", "create a worktree for a symbol branch"},
	} {
		set.tools = append(set.tools, &deferredTool{inner: echoTool(t, n[0], n[1]).(runnableTool), set: set})
	}
	hits := searchDeferred(set.tools, "lsp_rename")
	if len(hits) != 1 || hits[0].Name() != "lsp_rename" {
		t.Fatalf("exact name: %v", hits)
	}
	hits = searchDeferred(set.tools, "references symbol")
	if len(hits) == 0 || hits[0].Name() != "lsp_references" {
		t.Fatalf("ranking: first hit %v", hits)
	}
	if searchDeferred(set.tools, "  ") != nil {
		t.Fatal("empty query must load nothing")
	}
}

var _ runnableTool = (*deferredTool)(nil)
