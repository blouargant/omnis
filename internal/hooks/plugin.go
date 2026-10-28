package hooks

import (
	"strings"
)

// PluginLayer is one Claude Code plugin's hooks declaration: the plugin's name
// and install directory (what ${CLAUDE_PLUGIN_ROOT} stands for) and the path of
// its hooks/hooks.json.
type PluginLayer struct {
	Name string
	Root string
	Path string
}

// PluginRef identifies the plugin a matcher came from.
type PluginRef struct {
	Name string
	Root string
}

// claudeToolName maps an omnis tool name to the name a Claude Code plugin's
// matcher expects: the background shell tools are Bash to a plugin.
func claudeToolName(name string) string {
	switch name {
	case "bash_background", "monitor":
		return "Bash"
	}
	return name
}

// claudeInputs adapts a tool-event input to Claude's shape for a plugin hook.
// It returns one input per hook invocation: a MultiEdit with several files
// becomes one Edit-like input per file, since a Claude hook script expects a
// single file_path.
func claudeInputs(in Input) []Input {
	if in.ToolName == "" {
		return []Input{in}
	}
	switch in.ToolName {
	case "bash_background", "monitor":
		out := in
		out.ToolName = "Bash"
		out.ToolInput = map[string]any{"command": in.ToolInput["command"]}
		return []Input{out}
	case "MultiEdit":
		files, _ := in.ToolInput["files"].([]any)
		var outs []Input
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			path, _ := fm["file_path"].(string)
			if path == "" {
				continue
			}
			one := in
			one.ToolInput = map[string]any{"file_path": path, "edits": fm["edits"]}
			outs = append(outs, one)
		}
		if len(outs) > 0 {
			return outs
		}
	}
	return []Input{in}
}

// expandPluginRoot replaces ${CLAUDE_PLUGIN_ROOT} in a plugin hook command.
func expandPluginRoot(command, root string) string {
	return strings.ReplaceAll(command, "${CLAUDE_PLUGIN_ROOT}", root)
}
