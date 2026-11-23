package agent

import (
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
)

// describedTool gives a tool another description (prompt_overrides.json
// tool.<Name>). Only the text the model reads changes: the schema, the name and
// the dispatch are the inner tool's.
type describedTool struct {
	inner runnableTool
	desc  string
}

func (t *describedTool) Name() string        { return t.inner.Name() }
func (t *describedTool) Description() string { return t.desc }
func (t *describedTool) IsLongRunning() bool { return t.inner.IsLongRunning() }

func (t *describedTool) Declaration() *genai.FunctionDeclaration {
	d := t.inner.Declaration()
	if d == nil {
		return nil
	}
	c := *d
	c.Description = t.desc
	return &c
}

// ProcessRequest packs this wrapper so dispatch goes through its Run (ADK
// dispatches by the object registered in req.Tools).
func (t *describedTool) ProcessRequest(_ adk.ToolContext, req *model.LLMRequest) error {
	return toolutils.PackTool(req, t)
}

func (t *describedTool) Run(ctx adk.ToolContext, args any) (map[string]any, error) {
	return t.inner.Run(ctx, args)
}

// applyToolDescriptions wraps each overridable tool that has a tool.<Name>
// override, and records tool.<Name> as reaching cfg for every overridable tool
// cfg holds, overridden or not. Applied BEFORE applyReadOnly: a read-only
// agent's Write keeps the read-only note appended to the override, and its
// Bash is replaced by the read-only Bash, whose own text the tool.Bash override
// (written for the normal Bash) must not replace — so tool.Bash is not recorded
// for a read-only agent. With no override the list is returned unchanged.
func applyToolDescriptions(runtime RuntimeSettings, cfg RuntimeAgentConfig, tools []tool.Tool) []tool.Tool {
	var out []tool.Tool
	for i, t := range tools {
		name := t.Name()
		if !isOverridableTool(name) || (cfg.ReadOnly && name == "Bash") {
			if out != nil {
				out = append(out, t)
			}
			continue
		}
		runtime.reach.add("tool."+name, cfg.Name)
		runtime.reach.setDefault("tool."+name, t.Description())
		desc, ok := runtime.PromptOverrides["tool."+name]
		rt, runnable := t.(runnableTool)
		if !ok || !runnable {
			if out != nil {
				out = append(out, t)
			}
			continue
		}
		if out == nil {
			out = append(make([]tool.Tool, 0, len(tools)), tools[:i]...)
		}
		out = append(out, &describedTool{inner: rt, desc: desc})
	}
	if out == nil {
		return tools
	}
	return out
}

func isOverridableTool(name string) bool {
	for _, n := range OverridableTools {
		if n == name {
			return true
		}
	}
	return false
}
