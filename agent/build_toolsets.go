package agent

import (
	"context"
	"strings"

	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/embed"
	mcpcfg "github.com/blouargant/omnis/internal/mcp"
	"github.com/blouargant/omnis/internal/skills"
	"github.com/blouargant/omnis/internal/softskills"
)

// buildLeaderToolsets resolves the leader's effective skills, soft-skills
// and MCP toolsets, applying per-agent overrides on top of the global
// runtime defaults. It returns the individual toolsets (so they can be
// passed to sub-agent wiring), the aggregated slice destined for the
// leader's `Toolsets` field, and the MCP pool handles that the calling
// Instance must release on Close.
func buildLeaderToolsets(
	ctx context.Context,
	runtime RuntimeSettings,
	leaderCfg RuntimeAgentConfig,
	pool *mcpcfg.Pool,
	emb embed.Embedder,
) (skillTS, softSkillTS tool.Toolset, mcpToolsets, allToolsets []tool.Toolset, mcpHandles []*mcpcfg.Handle) {
	leaderSoftSkillsDir := runtime.SoftSkillsDir
	if leaderCfg.SoftSkillsDir != "" {
		leaderSoftSkillsDir = leaderCfg.SoftSkillsDir
	}
	leaderMCPConfigPath := runtime.MCPConfigPath
	if leaderCfg.MCPConfigPath != "" {
		leaderMCPConfigPath = leaderCfg.MCPConfigPath
	}

	if ts, err := skills.Toolset(ctx, leaderCfg.Skills); err == nil {
		skillTS = ts
		allToolsets = append(allToolsets, ts)
	}
	if sts, err := softskills.Toolset(ctx, leaderSoftSkillsDir, emb); err == nil {
		softSkillTS = sts
		allToolsets = append(allToolsets, sts)
	}
	if mc, err := mcpcfg.Load(leaderMCPConfigPath); err == nil && pool != nil {
		if _, hs, err := pool.AcquireAll(mc); err == nil {
			mcpHandles = append(mcpHandles, hs...)
			// Per-agent whitelist (explicit opt-in): the leader sees only
			// the MCP servers it explicitly enables. An empty/unset
			// MCPServers list means no servers are attached.
			selected := filterMCPHandles(hs, leaderCfg.MCPServers)
			for _, h := range selected {
				mcpToolsets = append(mcpToolsets, h.Toolset)
				allToolsets = append(allToolsets, h.Toolset)
			}
		}
	}
	return
}

// filterMCPHandles returns the subset of handles whose Name appears in the
// allow list (case-insensitive, whitespace-trimmed). An empty/nil allow list
// returns no handles — explicit opt-in is required.
func filterMCPHandles(handles []*mcpcfg.Handle, allow []string) []*mcpcfg.Handle {
	if len(handles) == 0 || len(allow) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(allow))
	for _, n := range allow {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			set[n] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]*mcpcfg.Handle, 0, len(handles))
	for _, h := range handles {
		if _, ok := set[strings.ToLower(strings.TrimSpace(h.Name))]; ok {
			out = append(out, h)
		}
	}
	return out
}
