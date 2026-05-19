package claudeformat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// yokeAgentEntry mirrors the subset of agent.AgentEntry fields that the
// runtime config loader reads from registry/agents/<name>/agent.json.
// Defined here so this package does not import the agent package (which
// would create an import cycle via registries).
type yokeAgentEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
	Skills      []string `json:"skills,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
}

// InstallAgent writes a parsed AgentDef as agent.json (+ optional
// instruction.md) under agentsRegistryDir/<name>/. It does not add the agent
// to config/agents.json; callers that want the agent to be loaded must append
// the name themselves.
func InstallAgent(def *AgentDef, agentsRegistryDir string) error {
	if def.Name == "" {
		return fmt.Errorf("agent name is empty")
	}

	agentDir := filepath.Join(agentsRegistryDir, def.Name)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", agentDir, err)
	}

	entry := yokeAgentEntry{
		Name:        def.Name,
		Description: def.Description,
		Tools:       def.Tools,
		Model:       def.Model,
		Skills:      def.Skills,
		MCPServers:  def.MCPServers,
	}
	agentJSON, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	agentJSON = append(agentJSON, '\n')
	if err := os.WriteFile(filepath.Join(agentDir, "agent.json"), agentJSON, 0o644); err != nil {
		return fmt.Errorf("write agent.json: %w", err)
	}

	if def.Instruction != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "instruction.md"), []byte(def.Instruction), 0o644); err != nil {
			return fmt.Errorf("write instruction.md: %w", err)
		}
	}
	return nil
}
