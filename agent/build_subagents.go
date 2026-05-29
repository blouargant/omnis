package agent

import (
	"context"
	"fmt"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/codeindex"
	mcpcfg "github.com/blouargant/yoke/internal/mcp"
)

// buildSubAgents constructs every enabled sub-agent (skipping leader and
// curator) declared in the runtime config. Equivalent to
// buildSubAgentsFromConfigs called with every non-leader / non-curator
// enabled agent. Kept for any callers that want the full default behaviour.
func buildSubAgents(
	ctx context.Context,
	runtime RuntimeSettings,
	skillTS, softSkillTS tool.Toolset,
	leaderMCPHandles []*mcpcfg.Handle,
	pool *mcpcfg.Pool,
	modelForAgent func(RuntimeAgentConfig) (model.LLM, error),
	callbacks events.AgentCallbacks,
	codeIdx *codeindex.Index,
) (
	map[string]adkagent.Agent,
	[]adkagent.Agent,
	[]tool.Tool,
	[]*mcpcfg.Handle,
	error,
) {
	filtered := make([]RuntimeAgentConfig, 0, len(runtime.Agents))
	for _, cfg := range runtime.Agents {
		if cfg.Name == "leader" || cfg.Name == "curator" || !cfg.Enabled {
			continue
		}
		filtered = append(filtered, cfg)
	}
	return buildSubAgentsFromConfigs(ctx, filtered, runtime, skillTS, softSkillTS, leaderMCPHandles, pool, modelForAgent, callbacks, codeIdx)
}

// buildSubAgentsFromConfigs wires every passed-in agent configuration as a
// sub-agent. Returns:
//   - subAgentMap   : name → agent
//   - subAgents     : ordered slice for AgentLoader
//   - leaderSubTools: agenttool wrappers (non-concurrent) to append to the
//     leader's tool list, in declaration order.
//   - mcpHandles   : pooled MCP handles acquired for sub-agent overrides,
//     to be released by the calling Instance on Close.
//
// The caller is responsible for filtering out the leader and any agent it
// does not want exposed. modelForAgent must instantiate an LLM for the
// given config. callbacks are attached to every sub-agent so its tool/model
// activity reaches the shared event bus (sub-agents run in their own
// internal runner that does not inherit runner-level plugins).
func buildSubAgentsFromConfigs(
	ctx context.Context,
	configs []RuntimeAgentConfig,
	runtime RuntimeSettings,
	skillTS, softSkillTS tool.Toolset,
	leaderMCPHandles []*mcpcfg.Handle,
	pool *mcpcfg.Pool,
	modelForAgent func(RuntimeAgentConfig) (model.LLM, error),
	callbacks events.AgentCallbacks,
	codeIdx *codeindex.Index,
) (
	subAgentMap map[string]adkagent.Agent,
	subAgents []adkagent.Agent,
	leaderSubTools []tool.Tool,
	mcpHandles []*mcpcfg.Handle,
	err error,
) {
	subAgentMap = map[string]adkagent.Agent{}
	seenNames := map[string]bool{}

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		if seenNames[cfg.Name] {
			continue
		}
		seenNames[cfg.Name] = true

		agentLLM, mErr := modelForAgent(cfg)
		if mErr != nil {
			return nil, nil, nil, nil, mErr
		}

		desc := cfg.Description
		if desc == "" {
			desc = defaultAgentDescription(cfg.Name)
		}
		instr := cfg.Instruction
		if instr == "" {
			instr = defaultAgentInstruction(cfg.Name)
		}

		subTools, subToolsets, extraInstr, subHandles := toolsForAgentConfig(ctx, cfg, runtime, skillTS, softSkillTS, leaderMCPHandles, pool, codeIdx)
		mcpHandles = append(mcpHandles, subHandles...)
		instr = extraInstr + instr

		sa, sErr := agentkit.New(agentkit.AgentConfig{
			Name:                 cfg.Name,
			Description:          desc,
			Instruction:          instr,
			Model:                agentLLM,
			Tools:                subTools,
			Toolsets:             subToolsets,
			BeforeToolCallbacks:  []llmagent.BeforeToolCallback{callbacks.BeforeTool},
			AfterToolCallbacks:   []llmagent.AfterToolCallback{callbacks.AfterTool},
			OnToolErrorCallbacks: []llmagent.OnToolErrorCallback{callbacks.OnToolError},
			BeforeModelCallbacks: []llmagent.BeforeModelCallback{callbacks.BeforeModel},
			AfterModelCallbacks:  []llmagent.AfterModelCallback{callbacks.AfterModel},
		})
		if sErr != nil {
			return nil, nil, nil, nil, sErr
		}

		subAgents = append(subAgents, sa)
		subAgentMap[cfg.Name] = sa

		wrapped, ok := agenttool.New(sa, &agenttool.Config{}).(runnableTool)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("agenttool for %q is not runnable", cfg.Name)
		}
		leaderSubTools = append(leaderSubTools, newNonConcurrentTool(wrapped))
	}
	return subAgentMap, subAgents, leaderSubTools, mcpHandles, nil
}
