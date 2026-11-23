package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

func TestNoToolOverrideReturnsToolsUnchanged(t *testing.T) {
	tools := []tool.Tool{echoTool(t, "Read", "read a file"), echoTool(t, "lsp_hover", "hover")}
	got := applyToolDescriptions(RuntimeSettings{}, RuntimeAgentConfig{Name: "a"}, tools)
	if !reflect.DeepEqual(got, tools) {
		t.Errorf("tools changed with no override")
	}
}

func TestDescribedToolOverridesDeclaration(t *testing.T) {
	tools := []tool.Tool{echoTool(t, "Read", "read a file"), echoTool(t, "Grep", "search")}
	got := applyToolDescriptions(overrides("tool.Read", "NEW READ TEXT"), RuntimeAgentConfig{Name: "a"}, tools)
	rd := got[0].(runnableTool)
	if rd.Description() != "NEW READ TEXT" || rd.Declaration().Description != "NEW READ TEXT" {
		t.Errorf("Read description = %q / %q", rd.Description(), rd.Declaration().Description)
	}
	if rd.Declaration().Name != "Read" || rd.Declaration().ParametersJsonSchema == nil {
		t.Errorf("declaration lost its name or schema: %+v", rd.Declaration())
	}
	if got[1] != tools[1] {
		t.Errorf("a tool without override was wrapped")
	}
	if tools[0].Description() != "read a file" {
		t.Errorf("the inner tool was modified")
	}
}

func TestDescribedToolDispatchesToInner(t *testing.T) {
	got := applyToolDescriptions(overrides("tool.Read", "X"), RuntimeAgentConfig{Name: "a"}, []tool.Tool{echoTool(t, "Read", "d")})
	req := &model.LLMRequest{}
	if err := got[0].(interface {
		ProcessRequest(adk.ToolContext, *model.LLMRequest) error
	}).ProcessRequest(fakeToolCtx{sid: "s"}, req); err != nil {
		t.Fatal(err)
	}
	if req.Tools["Read"] != got[0] {
		t.Errorf("dispatch map holds %T, want the wrapper", req.Tools["Read"])
	}
	if !declared(req)["Read"] {
		t.Errorf("Read not declared")
	}
	out, err := got[0].(runnableTool).Run(fakeToolCtx{sid: "s"}, map[string]any{"x": "1"})
	if err != nil || out["name"] != "Read" {
		t.Errorf("Run = %v, %v", out, err)
	}
}

func TestToolOverrideReachesDeferredAndReadOnly(t *testing.T) {
	rt := overrides("tool.Grep", "NEW GREP", "tool.Write", "NEW WRITE", "tool.Bash", "NEW BASH")
	rt.reach = newPromptReach()
	cfg := RuntimeAgentConfig{Name: "ro", ReadOnly: true, DeferredTools: []string{"Grep"}}
	tools := []tool.Tool{echoTool(t, "Grep", "g"), echoTool(t, "Write", "w"), echoTool(t, "Bash", "b")}
	tools = applyToolDescriptions(rt, cfg, tools)
	tools = applyReadOnly(cfg, tools, nil)
	tools = applyDeferredTools(cfg.DeferredTools, tools)
	byName := map[string]tool.Tool{}
	for _, tl := range tools {
		byName[tl.Name()] = tl
	}
	if d := byName["Grep"].Description(); d != "NEW GREP" {
		t.Errorf("deferred Grep description = %q", d)
	}
	if d := byName["Write"].Description(); !strings.HasPrefix(d, "NEW WRITE") || !strings.Contains(d, "READ-ONLY AGENT") {
		t.Errorf("read-only Write lost its note or the override: %q", d)
	}
	if d := byName["Bash"].Description(); strings.Contains(d, "NEW BASH") || !strings.Contains(d, "READ-ONLY") {
		t.Errorf("read-only Bash must keep its own text: %q", d)
	}
	reach := rt.reach.snapshot()
	if len(reach["tool.Bash"]) != 0 {
		t.Errorf("tool.Bash recorded for a read-only agent: %v", reach["tool.Bash"])
	}
	if !reflect.DeepEqual(reach["tool.Grep"], []string{"ro"}) || !reflect.DeepEqual(reach["tool.Write"], []string{"ro"}) {
		t.Errorf("reach = %v", reach)
	}
}

func TestToolReachRecordedWithoutOverride(t *testing.T) {
	rt := RuntimeSettings{reach: newPromptReach()}
	applyToolDescriptions(rt, RuntimeAgentConfig{Name: "a"}, []tool.Tool{echoTool(t, "Bash", "b"), echoTool(t, "lsp_hover", "h")})
	reach := rt.reach.snapshot()
	if !reflect.DeepEqual(reach["tool.Bash"], []string{"a"}) || len(reach["tool.lsp_hover"]) != 0 {
		t.Errorf("reach = %v", reach)
	}
}

func TestToolOverrideReachesBuiltRootAndSubAgent(t *testing.T) {
	_, lo := promptLayers(t)
	writeAgentDef(t, lo, "leader", `{"name": "leader", "enabled": true, "leader": true, "tools": ["fs"]}`)
	writeAgentDef(t, lo, "worker", `{"name": "worker", "enabled": true, "tools": ["fs"]}`)
	if err := os.WriteFile(filepath.Join(lo, "agents.json"), []byte(`{"agents": ["leader", "worker"], "router_squad": "none", "squads": [{"name": "system", "leader": "leader", "members": ["worker"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writePromptFile(t, lo, `{"prompts": {"tool.Read": "OVERRIDDEN READ", "tool.AskUserQuestion": "OVERRIDDEN ASK"}}`)
	inst := buildTestInstance(t)
	sq := inst.Squad("system")
	desc := func(a adkagent.Agent, name string) string {
		for _, tl := range agentTools(t, a) {
			if tl.Name() == name {
				return tl.Description()
			}
		}
		return "<absent>"
	}
	if d := desc(sq.Leader, "Read"); d != "OVERRIDDEN READ" {
		t.Errorf("root Read = %q", d)
	}
	if d := desc(sq.Leader, "AskUserQuestion"); d != "OVERRIDDEN ASK" {
		t.Errorf("root AskUserQuestion = %q", d)
	}
	if d := desc(sq.SubAgents["worker"], "Read"); d != "OVERRIDDEN READ" {
		t.Errorf("sub-agent Read = %q", d)
	}
	if got := inst.PromptReach["tool.Read"]; !reflect.DeepEqual(got, []string{"leader", "worker"}) {
		t.Errorf("tool.Read reach = %v", got)
	}
}
