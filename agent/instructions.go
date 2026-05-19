package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/yoke/internal/paths"
)

// ReadAgentInstruction returns the trimmed contents of the agent's instruction file
// from registry/agents/<name>/instruction.md, or falls back to registry/agents/default.md,
// or "" if neither exist.
func ReadAgentInstruction(name string) string {
	agentsDir := paths.AgentsRegistryDir()

	// Try agent-specific instruction
	instructionPath := filepath.Join(agentsDir, name, "instruction.md")
	if b, err := os.ReadFile(instructionPath); err == nil {
		return strings.TrimRight(string(b), "\n")
	}

	// Fall back to default instruction
	defaultPath := filepath.Join(agentsDir, "default.md")
	if b, err := os.ReadFile(defaultPath); err == nil {
		return strings.TrimRight(string(b), "\n")
	}

	return ""
}
