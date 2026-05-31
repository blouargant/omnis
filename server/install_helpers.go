package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
)

// tryAutoInstallSkills checks which skills in skillNames are not locally installed
// and attempts to install them from configured skill registries. Returns lists of
// successfully auto-installed names and warning messages for skills not found in
// any registry.
func tryAutoInstallSkills(skillNames []string, skillsReadDir, skillsWriteDir, remoteRegistriesPath string) (installed []string, warnings []string) {
	if len(skillNames) == 0 {
		return
	}

	regs, _ := registries.LoadRegistries(remoteRegistriesPath)

	for _, skillName := range skillNames {
		if _, err := os.Stat(filepath.Join(skillsReadDir, skillName, "SKILL.md")); err == nil {
			continue
		}

		found := false
		for _, reg := range regs {
			if !reg.Serves(registries.KindSkills) {
				continue
			}
			ref, err := registries.ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			skills, err := registries.BrowseSkills(ref, reg.Token, skillsReadDir)
			if err != nil {
				continue
			}
			for _, sk := range skills {
				if sk.Name == skillName {
					if mkErr := os.MkdirAll(skillsWriteDir, 0o755); mkErr == nil {
						if _, instErr := registries.InstallSkill(ref, reg.Token, sk.DirPath, skillsWriteDir); instErr == nil {
							installed = append(installed, skillName)
							found = true
						}
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("skill %q is required but not installed and was not found in any configured registry", skillName))
		}
	}
	return
}

// checkMCPServerDeps returns a warning for each server name in serverNames
// that is not currently configured in mcp_config.json.
func checkMCPServerDeps(serverNames []string, mcpConfigPath string) []string {
	if len(serverNames) == 0 {
		return nil
	}
	installed := readInstalledMCPNames(mcpConfigPath)
	var warnings []string
	for _, name := range serverNames {
		if !installed[name] {
			warnings = append(warnings, fmt.Sprintf("MCP server %q is required but not configured in mcp_config.json", name))
		}
	}
	return warnings
}

// tryAutoInstallMCP checks which MCP servers in serverNames are not configured
// in mcp_config.json and attempts to install them from configured MCP registries.
// Returns the successfully auto-installed names and warning messages for servers
// not found in any registry (so an agent that references an MCP server is usable
// after install, mirroring tryAutoInstallSkills).
func tryAutoInstallMCP(serverNames []string, mcpConfigRead, mcpConfigWrite, remoteRegistriesPath string) (installed []string, warnings []string) {
	if len(serverNames) == 0 {
		return
	}

	regs, _ := registries.LoadRegistries(remoteRegistriesPath)
	configured := readInstalledMCPNames(mcpConfigRead)

	for _, name := range serverNames {
		if name == "" {
			continue
		}
		if configured[name] {
			continue
		}

		found := false
		for _, reg := range regs {
			if !reg.Serves(registries.KindMCP) {
				continue
			}
			ref, err := registries.ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			tools, err := registries.BrowseMCPTools(ref, reg.Token, nil)
			if err != nil {
				continue
			}
			for _, t := range tools {
				if t.Name != name {
					continue
				}
				resolved, srv, inputs, _, resErr := registries.ResolveMCPServer(ref, reg.Token, t.DirPath)
				if resErr != nil {
					break
				}
				if _, mergeErr := registries.MergeMCPServer(mcpConfigRead, mcpConfigWrite, resolved, srv, inputs); mergeErr == nil {
					installed = append(installed, name)
					found = true
				}
				break
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("MCP server %q is required but not configured and was not found in any configured MCP registry", name))
		}
	}
	return
}

// tryAutoInstallAgents checks which agents in agentNames are not installed/enabled
// and attempts to install and enable them from configured agent registries. Returns
// lists of successfully auto-installed names and warning messages for agents not
// found in any registry.
func tryAutoInstallAgents(agentNames []string, agentsRegistryDir string, agentsConfigRead func() string, agentsConfigWrite, remoteRegistriesPath string) (installed []string, warnings []string) {
	if len(agentNames) == 0 {
		return
	}

	regs, _ := registries.LoadRegistries(remoteRegistriesPath)
	configured := readConfiguredAgentNames(agentsConfigRead())

	for _, agentName := range agentNames {
		if agentName == "" {
			continue
		}
		// If the agent is already listed in agents.json it is available to the
		// runtime (builtin agents live in ./registry/agents/, not in the write
		// dir, so a filesystem check against agentsRegistryDir would miss them).
		if configured[agentName] {
			continue
		}

		found := false
		for _, reg := range regs {
			if !reg.Serves(registries.KindAgents) {
				continue
			}
			ref, err := registries.ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			agents, err := registries.BrowseAgents(ref, reg.Token, agentsRegistryDir)
			if err != nil {
				continue
			}
			for _, ag := range agents {
				if ag.Name == agentName {
					if mkErr := os.MkdirAll(agentsRegistryDir, 0o755); mkErr == nil {
						if _, instErr := registries.InstallAgent(ref, reg.Token, ag.DirPath, agentsRegistryDir); instErr == nil {
							// If the install target sits under .agents/, route
							// the agents.json edit there too — otherwise we'd
							// reference a local-only agent from $HOME/.yoke.
							configWrite := agentsConfigWrite
							if paths.Layer(agentsRegistryDir) == "local" {
								configWrite = filepath.Join(paths.LocalWriteDir(), "agents.json")
							}
							_, _ = appendAgentToConfig(agentsConfigRead(), configWrite, agentName)
							installed = append(installed, agentName)
							found = true
						}
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("agent %q is required but not installed and was not found in any configured registry", agentName))
		}
	}
	return
}

// parseAgentJSONDeps extracts the skills and mcp_servers dependency lists
// from an on-disk agent.json file.
func parseAgentJSONDeps(agentJSONPath string) (skills, mcpServers []string) {
	data, err := os.ReadFile(agentJSONPath)
	if err != nil {
		return
	}
	// AgentEntry accepts both the snake_case "mcp_servers" and the camelCase
	// "mcpServers" alias, so read both and merge to avoid missing a dependency
	// declared in a Claude-format / converted agent.json.
	var entry struct {
		Skills        []string `json:"skills"`
		MCPServers    []string `json:"mcp_servers"`
		MCPServersAlt []string `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return
	}
	mcpServers = append(entry.MCPServers, entry.MCPServersAlt...)
	return entry.Skills, mcpServers
}
