package agent

import (
	"context"

	"google.golang.org/adk/tool"

	mcpcfg "github.com/blouargant/yoke/internal/mcp"
	"github.com/blouargant/yoke/internal/skills"
	"github.com/blouargant/yoke/internal/softskills"
)

// buildLeaderToolsets resolves the leader's effective skills, soft-skills
// and MCP toolsets, applying per-agent overrides on top of the global
// runtime defaults. It returns the individual toolsets (so they can be
// passed to sub-agent wiring) and the aggregated slice destined for the
// leader's `Toolsets` field.
func buildLeaderToolsets(
	ctx context.Context,
	runtime RuntimeSettings,
	leaderCfg RuntimeAgentConfig,
) (skillTS, softSkillTS tool.Toolset, mcpToolsets, allToolsets []tool.Toolset) {
	leaderSkillsDir := runtime.SkillsDir
	if leaderCfg.SkillsDir != "" {
		leaderSkillsDir = leaderCfg.SkillsDir
	}
	leaderSoftSkillsDir := runtime.SoftSkillsDir
	if leaderCfg.SoftSkillsDir != "" {
		leaderSoftSkillsDir = leaderCfg.SoftSkillsDir
	}
	leaderMCPConfigPath := runtime.MCPConfigPath
	if leaderCfg.MCPConfigPath != "" {
		leaderMCPConfigPath = leaderCfg.MCPConfigPath
	}

	if ts, err := skills.Toolset(ctx, leaderSkillsDir); err == nil {
		skillTS = ts
		allToolsets = append(allToolsets, ts)
	}
	if sts, err := softskills.Toolset(ctx, leaderSoftSkillsDir); err == nil {
		softSkillTS = sts
		allToolsets = append(allToolsets, sts)
	}
	if mc, err := mcpcfg.Load(leaderMCPConfigPath); err == nil {
		if mts, err := mc.Toolsets(); err == nil {
			mcpToolsets = append(mcpToolsets, mts...)
			allToolsets = append(allToolsets, mts...)
		}
	}
	return
}
