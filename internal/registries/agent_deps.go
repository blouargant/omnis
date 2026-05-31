package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// requestReload fires the hot-reload hook if the surface wired one and reports
// whether it did. A no-op (returns false) on surfaces without hot-reload
// (CLI/TUI), so callers can surface "reloaded" honestly.
func (d Deps) requestReload() bool {
	if d.RequestReload == nil {
		return false
	}
	return d.RequestReload()
}

// parseAgentDeps extracts the skills and mcp_servers dependency lists declared
// in a remote agent's manifest. AgentEntry accepts both the snake_case
// "mcp_servers" and the camelCase "mcpServers" alias, so both are read and
// merged. A Claude-format markdown manifest (which carries no JSON deps) parses
// to empty lists.
func parseAgentDeps(raw []byte) (skills, mcpServers []string) {
	var entry struct {
		Skills        []string `json:"skills"`
		MCPServers    []string `json:"mcp_servers"`
		MCPServersAlt []string `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, nil
	}
	return entry.Skills, append(entry.MCPServers, entry.MCPServersAlt...)
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
			if !reg.Serves(KindSkills) {
				continue
			}
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
			if !reg.Serves(KindMCP) {
				continue
			}
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
