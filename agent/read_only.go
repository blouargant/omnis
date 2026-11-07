package agent

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
	fstools "github.com/blouargant/omnis/core/tools"
)

// Read-only agents (agent.json `read_only: true`).
//
// The first Harbor smoke showed why this has to be enforced rather than asked
// for: the `reviewer`, whose description and instruction both said
// "read-only, never modifies code", ran `sed -i` on project files, added a
// probe test file to the project, and `git stash`ed the coder's uncommitted
// work to compare against the base revision. A read-only agent therefore:
//   - runs Bash under the Landlock sandbox (fstools.NewReadOnlyBash): the
//     kernel refuses every write outside the scratch directories;
//   - may Write / Edit / MultiEdit only under those scratch directories;
//   - does not get the tools that change a project by other means.
// See CLAUDE.md "Read-only agents".

// readOnlyDropped are the tools a read-only agent never gets. Globs as in
// deferred_tools.
var readOnlyDropped = []string{
	"revert", "ast_grep_rewrite", "lsp_rename", "lsp_code_action",
	"worktree_*", "bash_background", "monitor", "bg_cancel", "run_tests",
	"spawn_session",
	"set_preference", "set_agent", "set_model", "update_config", "remove_config", "rollback_settings",
	"install_remote_item", "install_remote_skill", "link_skill_to_agent",
}

// scratchOnlyTools may run, but only on paths under the scratch directories.
var scratchOnlyTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true}

// applyReadOnly enforces read_only on an agent's tools. A no-op for an agent
// that is not read-only.
//
// MCP toolsets are filtered where they are mounted (toolsForAgentConfig): only a
// server declared read_only in mcp_config.json reaches a read-only agent.
func applyReadOnly(cfg RuntimeAgentConfig, tools []tool.Tool, _ []tool.Toolset) []tool.Tool {
	if !cfg.ReadOnly {
		return tools
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		name := t.Name()
		switch {
		case matchesDeferred(readOnlyDropped, name):
			continue
		case name == "Bash":
			out = append(out, fstools.NewReadOnlyBash())
		case scratchOnlyTools[name]:
			if rt, ok := t.(runnableTool); ok {
				out = append(out, &scratchOnlyTool{inner: rt})
			}
			// a file tool we cannot guard is dropped, never passed through
		default:
			out = append(out, t)
		}
	}
	return out
}

// scratchOnlyTool refuses a file write outside the scratch directories.
type scratchOnlyTool struct{ inner runnableTool }

func (t *scratchOnlyTool) Name() string        { return t.inner.Name() }
func (t *scratchOnlyTool) IsLongRunning() bool { return t.inner.IsLongRunning() }
func (t *scratchOnlyTool) Description() string {
	return t.inner.Description() + " READ-ONLY AGENT: only paths under " +
		strings.Join(fstools.ReadOnlyWritable(), ", ") + " may be written; anything else is refused."
}

func (t *scratchOnlyTool) Declaration() *genai.FunctionDeclaration {
	d := t.inner.Declaration()
	if d == nil {
		return nil
	}
	c := *d
	c.Description = t.Description()
	return &c
}

// ProcessRequest packs this wrapper so dispatch goes through its Run.
func (t *scratchOnlyTool) ProcessRequest(_ adk.ToolContext, req *model.LLMRequest) error {
	return toolutils.PackTool(req, t)
}

func (t *scratchOnlyTool) Run(ctx adk.ToolContext, args any) (map[string]any, error) {
	cwd := fstools.CwdForContext(ctx)
	for _, p := range writeTargets(args) {
		if !fstools.UnderWritable(cwd, p) {
			return map[string]any{"result": fmt.Sprintf(
				"Error: refused — this agent is read-only and may write only under %s, not %q. "+
					"Report the change instead of making it, or work on a copy under /tmp.",
				strings.Join(fstools.ReadOnlyWritable(), ", "), p)}, nil
		}
	}
	return t.inner.Run(ctx, args)
}

// writeTargets lists the paths a Write/Edit/MultiEdit call would write. A
// call naming no path yields one empty path, which is refused.
func writeTargets(args any) []string {
	m, _ := args.(map[string]any)
	var out []string
	if p, ok := m["file_path"].(string); ok {
		out = append(out, p)
	}
	if files, ok := m["files"].([]any); ok {
		for _, f := range files {
			fm, _ := f.(map[string]any)
			p, _ := fm["file_path"].(string)
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}
