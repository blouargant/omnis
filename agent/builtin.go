package agent

import "github.com/blouargant/omnis/internal/softskills"

// BuiltinAgentNames lists agents that are wired into the binary. Their
// default descriptions and system instructions ship as embedded resources;
// the web UI surfaces them read-only and refuses to delete them — users may
// only enable or disable them.
var BuiltinAgentNames = []string{
	"leader", "investigator", "web_agent", "summariser", "curator",
}

// IsBuiltinAgent reports whether name is one of the built-in agents.
func IsBuiltinAgent(name string) bool {
	for _, n := range BuiltinAgentNames {
		if n == name {
			return true
		}
	}
	return false
}

// BuiltinAgentDefault returns the embedded default description and system
// instruction for a built-in agent. Returns empty strings for unknown names.
// Curator has no .md instruction file — its prompt lives as a constant in
// internal/softskills, which the curator hook feeds into the agent at run
// time.
func BuiltinAgentDefault(name string) (description, instruction string) {
	if !IsBuiltinAgent(name) {
		return "", ""
	}
	description = defaultAgentDescription(name)
	if name == "curator" {
		instruction = softskills.CuratorPrompt
		return
	}
	instruction = defaultAgentInstruction(name)
	return
}
