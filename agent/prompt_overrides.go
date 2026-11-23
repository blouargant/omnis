package agent

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/blouargant/omnis/internal/configedit"
)

// promptOverridesFile replaces omnis's shared prompt texts by stable id. It is
// merged across config layers like any other file ({"prompts": {...}}, keyed by
// id, tombstone "prompts_removed"). With no file in any layer every request is
// byte-identical to a build without overrides (testdata/prompts_golden).
const promptOverridesFile = "prompt_overrides.json"

// PromptIDs are the fixed overridable texts (v1). Each covers only fixed
// wording: a block's header, generated list and conditional sentence stay in
// code, and a conditional sentence has its own id, so an override can neither
// drop a generated part nor make a sentence appear where its condition is off.
var PromptIDs = []string{
	"contract.root",
	"contract.subagent",
	"block.subagents",
	"block.handoff",
	"block.language",
	"block.language.delegates",
	"block.language.evidence",
	"block.choice",
	"block.choice.handoff",
	"block.steering",
	"skills.rules",
	"memory.rules",
	"memory.reminder",
}

// OverridableTools are the tools whose description `tool.<Name>` may replace
// (v1: omnis's own tools; MCP, A2A and sub-agent tools are not included).
var OverridableTools = []string{
	"Read", "Write", "Edit", "MultiEdit", "Grep", "Glob", "Bash",
	"AskUserQuestion", "WebFetch", "WebSearch",
	"teammate_ask", "teammate_tell", "teammate_check", "teammate_list",
	"bash_background", "monitor", "bg_list", "bg_cancel", "bg_output",
	"todo_write", "todo_update", "todo_read",
}

func knownPromptID(id string) bool {
	for _, p := range PromptIDs {
		if p == id {
			return true
		}
	}
	if name, ok := strings.CutPrefix(id, "tool."); ok {
		for _, n := range OverridableTools {
			if n == name {
				return true
			}
		}
	}
	return false
}

// resolvePromptOverrides reads the merged prompt_overrides.json. An unknown id
// or an empty text is left out and reported as a warning; it never fails.
func resolvePromptOverrides() (map[string]string, []string) {
	merged, _, err := configedit.LoadMergedSection(promptOverridesFile)
	if err != nil {
		return nil, []string{fmt.Sprintf("prompt overrides: %v", err)}
	}
	raw, _ := merged["prompts"].(map[string]any)
	if len(raw) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out map[string]string
	var warns []string
	for _, id := range ids {
		text, _ := raw[id].(string)
		switch {
		case !knownPromptID(id):
			warns = append(warns, fmt.Sprintf("prompt override %q: unknown id", id))
		case strings.TrimSpace(text) == "":
			warns = append(warns, fmt.Sprintf("prompt override %q: empty text", id))
		default:
			if out == nil {
				out = map[string]string{}
			}
			out[id] = text
		}
	}
	return out, warns
}

// promptText returns the override for id, else def.
func promptText(runtime RuntimeSettings, id, def string) string {
	if t, ok := runtime.PromptOverrides[id]; ok {
		return t
	}
	return def
}

// applyPromptOverrides resolves the file into out, logging each warning.
func applyPromptOverrides(out *RuntimeSettings) {
	var warns []string
	out.PromptOverrides, warns = resolvePromptOverrides()
	for _, w := range warns {
		log.Printf("config: %s", w)
	}
	out.ConfigWarnings = append(out.ConfigWarnings, warns...)
}
