package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/omnis/internal/claudeformat"
)

// resolveSkillDeps installs the commands and permission rule-sets a skill
// declares (in its SKILL.md frontmatter) but that are not yet present locally,
// browsing the configured `commands` and `permissions` registries. Mirrors
// resolveAgentDeps: best-effort, a dependency that cannot be located yields a
// warning rather than an error so the skill install is never rolled back.
// Returns the names installed (prefixed "command:" / "permission:") and
// warnings for anything not found.
func (d Deps) resolveSkillDeps(commands, perms []string) (installed, warnings []string) {
	if len(commands) == 0 && len(perms) == 0 {
		return nil, nil
	}
	var regs []Registry
	if d.ConfigPath != nil {
		regs, _ = LoadRegistries(d.ConfigPath())
	}

	var cmdInstalled map[string]bool
	if d.InstalledCommandNames != nil {
		cmdInstalled = d.InstalledCommandNames()
	}
	for _, name := range commands {
		if name == "" {
			continue
		}
		if cmdInstalled[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		if d.InstallCommand == nil {
			warnings = append(warnings, fmt.Sprintf("command %q is required but command install is unavailable in this surface", name))
			continue
		}
		found := false
		for _, reg := range regs {
			// Search every registry, not just commands-kind ones — a skill and
			// the command it depends on may live in the same repo registered
			// under a single kind. BrowseCommands finds nothing in a registry
			// without command markdown files.
			ref, err := ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			items, err := BrowseCommands(ref, reg.Token, nil)
			if err != nil {
				continue
			}
			for _, c := range items {
				if strings.EqualFold(c.Name, name) {
					if _, _, err := d.InstallCommand(ref, reg.Token, c.DirPath); err == nil {
						installed = append(installed, "command:"+name)
						found = true
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("command %q is required but was not found in any configured registry", name))
		}
	}

	for _, name := range perms {
		if name == "" {
			continue
		}
		if d.InstallPermission == nil {
			warnings = append(warnings, fmt.Sprintf("permission set %q is required but permission install is unavailable in this surface", name))
			continue
		}
		found := false
		for _, reg := range regs {
			// Search every registry, not just permissions-kind ones — see the
			// commands loop above. BrowsePermissions finds nothing in a registry
			// without permission rule-sets.
			ref, err := ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			items, err := BrowsePermissions(ref, reg.Token, nil)
			if err != nil {
				continue
			}
			for _, p := range items {
				if strings.EqualFold(p.Name, name) {
					if _, _, err := d.InstallPermission(ref, reg.Token, p.DirPath); err == nil {
						installed = append(installed, "permission:"+name)
						found = true
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("permission set %q is required but was not found in any configured registry", name))
		}
	}
	return installed, warnings
}

// cascadeSkillDeps fetches a just-installed skill's SKILL.md, parses its
// declared commands/permissions from the frontmatter, and installs the missing
// ones from the configured registries. Best-effort: a missing SKILL.md or
// unreadable frontmatter simply yields no cascade.
func (d Deps) cascadeSkillDeps(ref RepoRef, token, dirPath string) (installed, warnings []string) {
	raw, err := FetchSkillMD(ref, token, dirPath)
	if err != nil {
		return nil, nil
	}
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		return nil, nil
	}
	return d.resolveSkillDeps(fm.Commands, fm.Permissions)
}

// requestReload fires the hot-reload hook if the surface wired one and reports
// whether it did. A no-op (returns false) on surfaces without hot-reload
// (CLI/TUI), so callers can surface "reloaded" honestly.
func (d Deps) requestReload() bool {
	if d.RequestReload == nil {
		return false
	}
	return d.RequestReload()
}

// parseAgentDeps extracts the skills, mcp_servers and subagents dependency lists
// declared in a remote agent's manifest. AgentEntry accepts both the snake_case
// "mcp_servers" and the camelCase "mcpServers" alias, so both are read and
// merged.
//
// `subagents` (the agent's own delegable team) is the one dependency whose absence
// is FATAL rather than degrading: a dangling edge makes the whole runtime config
// fail to resolve. See resolveSubAgentDeps.
//
// A Claude-format markdown manifest (a .md file whose skills/mcpServers live in
// YAML frontmatter) is not valid JSON, so the native parse fails — we then fall
// back to the shared Claude-format parser so the dependency cascade also fires
// for markdown agents. That format has no `subagents` concept, so a markdown
// agent never declares a team. This mirrors the web-UI install route, which reads
// the deps from the normalised on-disk agent.json that InstallAgent writes after
// converting either format.
func parseAgentDeps(raw []byte) (skills, mcpServers, subAgents []string) {
	var entry struct {
		Skills        []string `json:"skills"`
		MCPServers    []string `json:"mcp_servers"`
		MCPServersAlt []string `json:"mcpServers"`
		SubAgents     []string `json:"subagents"`
	}
	if err := json.Unmarshal(raw, &entry); err == nil {
		return entry.Skills, append(entry.MCPServers, entry.MCPServersAlt...), entry.SubAgents
	}
	defs, err := claudeformat.Parse(raw)
	if err != nil || len(defs) == 0 || defs[0] == nil {
		return nil, nil, nil
	}
	return defs[0].Skills, defs[0].MCPServers, nil
}

// resolveAgentDeps installs the skills and MCP servers an agent declares but
// that are not yet present locally, browsing the configured registries. It is
// best-effort: a dependency that cannot be located yields a warning rather than
// an error, so an agent install is never rolled back over a missing dependency.
// Returns the names successfully installed (prefixed "skill:" / "mcp:") and
// human-readable warnings for anything not found.
func (d Deps) resolveAgentDeps(skills, mcpServers []string) (installed, warnings []string) {
	if len(skills) == 0 && len(mcpServers) == 0 {
		return nil, nil
	}
	var regs []Registry
	if d.ConfigPath != nil {
		regs, _ = LoadRegistries(d.ConfigPath())
	}

	skillsDir := ""
	if d.RegistryDir != nil {
		skillsDir = d.RegistryDir()
	}
	for _, name := range skills {
		if name == "" {
			continue
		}
		if skillsDir != "" {
			if _, err := os.Stat(filepath.Join(skillsDir, name, "SKILL.md")); err == nil {
				continue // already installed
			}
		}
		found := false
		for _, reg := range regs {
			// Search every registry, not just skills-kind ones: a multi-purpose
			// repo (e.g. one holding an agent alongside its skills) is commonly
			// registered under a single kind, so a kind filter would skip the
			// skill the agent depends on. BrowseSkills is a best-effort tree
			// walk that simply finds nothing in a registry without SKILL.md.
			ref, err := ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			items, err := BrowseSkills(ref, reg.Token, skillsDir)
			if err != nil {
				continue
			}
			for _, sk := range items {
				if sk.Name == name {
					if _, err := InstallSkill(ref, reg.Token, sk.DirPath, skillsDir); err == nil {
						installed = append(installed, "skill:"+name)
						found = true
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("skill %q is required but was not found in any configured registry", name))
		}
	}

	var configured map[string]bool
	if d.InstalledMCPNames != nil {
		configured = d.InstalledMCPNames()
	}
	for _, name := range mcpServers {
		if name == "" {
			continue
		}
		if configured[name] {
			continue
		}
		if d.InstallMCP == nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q is required but MCP install is unavailable in this surface", name))
			continue
		}
		found := false
		for _, reg := range regs {
			// Search every registry, not just mcp-kind ones — see the skills
			// loop above. A repo bundling an agent with its MCP server is often
			// registered under a single kind, so a kind filter would skip the
			// server the agent depends on. BrowseMCPTools returns nothing in a
			// registry without MCP manifests.
			ref, err := ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			tools, err := BrowseMCPTools(ref, reg.Token, nil)
			if err != nil {
				continue
			}
			for _, t := range tools {
				if t.Name == name {
					if _, _, err := d.InstallMCP(ref, reg.Token, t.DirPath); err == nil {
						installed = append(installed, "mcp:"+name)
						found = true
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("MCP server %q is required but was not found in any configured registry", name))
		}
	}
	return installed, warnings
}

// maxSubAgentInstallDepth bounds the recursive `subagents` cascade. A gatherer may
// itself have a team, but a deep chain almost certainly means a mis-authored
// registry rather than a real topology, and we must not walk one forever.
const maxSubAgentInstallDepth = 4

// resolveSubAgentDeps installs the agents an agent delegates to via `subagents`,
// recursively (a gatherer may have a team of its own).
//
// This cascade is NOT best-effort in the way the skills/MCP ones are. A dangling
// `subagents` edge is FATAL: validateSubAgentGraph rejects the whole runtime
// config, so a specialist installed without its team does not merely lose a
// capability — it breaks every squad until the config is fixed. The warning it
// emits therefore has to say so, loudly, rather than read as a soft note.
//
// Each installed sub-agent is ENABLED (the graph rejects a disabled target), and
// is left out of every squad's members: an agent reachable only through
// `subagents` is built for its caller without being handed to the coordinator.
func (d Deps) resolveSubAgentDeps(subAgents []string, depth int) (installed, warnings []string) {
	if len(subAgents) == 0 {
		return nil, nil
	}
	if depth >= maxSubAgentInstallDepth {
		return nil, []string{fmt.Sprintf("stopped resolving `subagents` at depth %d: the chain %v is suspiciously deep", depth, subAgents)}
	}
	if d.InstallAgent == nil {
		return nil, []string{fmt.Sprintf("agents %v are required as sub-agents but agent install is unavailable in this surface", subAgents)}
	}

	var regs []Registry
	if d.ConfigPath != nil {
		regs, _ = LoadRegistries(d.ConfigPath())
	}
	agentsDir := ""
	if d.AgentsRegistryDir != nil {
		agentsDir = d.AgentsRegistryDir()
	}

	for _, name := range subAgents {
		if name == "" {
			continue
		}
		if agentsDir != "" {
			if _, err := os.Stat(filepath.Join(agentsDir, name, "agent.json")); err == nil {
				continue // already installed
			}
		}
		found := false
		for _, reg := range regs {
			// Every registry, not just agents-kind ones — same reasoning as the
			// skills/MCP loops: a repo bundling a specialist with its gatherers is
			// commonly registered under one kind, and BrowseAgents simply finds
			// nothing in a registry without agent manifests.
			ref, err := ParseRepoRef(reg.URL, reg.Provider)
			if err != nil {
				continue
			}
			items, err := BrowseAgents(ref, reg.Token, agentsDir)
			if err != nil {
				continue
			}
			for _, it := range items {
				if it.Name != name {
					continue
				}
				// enable=true: the graph rejects a disabled target, so an installed
				// sub-agent that is not enabled would still break the config.
				if _, _, err := d.InstallAgent(ref, reg.Token, it.DirPath, true); err != nil {
					break
				}
				installed = append(installed, "agent:"+name)
				found = true

				// Its own dependencies, including its own team.
				if raw, ferr := FetchAgentJSON(ref, reg.Token, it.DirPath); ferr == nil {
					skills, mcpServers, nested := parseAgentDeps(raw)
					di, dw := d.resolveAgentDeps(skills, mcpServers)
					installed = append(installed, di...)
					warnings = append(warnings, dw...)
					ni, nw := d.resolveSubAgentDeps(nested, depth+1)
					installed = append(installed, ni...)
					warnings = append(warnings, nw...)
				}
				break
			}
			if found {
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf(
				"agent %q is required as a sub-agent but was not found in any configured registry — the config will FAIL to load until it is installed or the `subagents` edge is removed", name))
		}
	}
	return installed, warnings
}
