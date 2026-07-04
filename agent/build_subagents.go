package agent

import (
	"context"
	"fmt"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/codeindex"
	"github.com/blouargant/omnis/internal/docindex"
	mcpcfg "github.com/blouargant/omnis/internal/mcp"
	"github.com/blouargant/omnis/internal/regindex"
	"github.com/blouargant/omnis/internal/steer"
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
	regIdx *regindex.Index,
	docIdx *docindex.Index,
	steerStore *steer.Store,
	permGate llmagent.BeforeToolCallback,
	hooksBeforeTool llmagent.BeforeToolCallback,
	hooksAfterTool llmagent.AfterToolCallback,
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
	return buildSubAgentsFromConfigs(ctx, filtered, runtime, skillTS, softSkillTS, leaderMCPHandles, pool, modelForAgent, callbacks, codeIdx, regIdx, docIdx, steerStore, permGate, hooksBeforeTool, hooksAfterTool)
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
// given config. Because sub-agents run in their own internal runner that does
// NOT inherit runner-level plugins, everything a sub-agent needs is attached as
// agent-level callbacks here: `callbacks` (its tool/model activity reaches the
// shared event bus), `permGate` (the permission gate — its tool calls are
// asked/denied like the leader's), and `hooksBeforeTool`/`hooksAfterTool` (the
// PreToolUse/PostToolUse lifecycle hooks). The last three are nil-safe: nil ⇒
// that layer is skipped, byte-identical to a sub-agent built without it.
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
	regIdx *regindex.Index,
	docIdx *docindex.Index,
	steerStore *steer.Store,
	permGate llmagent.BeforeToolCallback,
	hooksBeforeTool llmagent.BeforeToolCallback,
	hooksAfterTool llmagent.AfterToolCallback,
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

		subTools, subToolsets, extraInstr, subHandles := toolsForAgentConfig(ctx, cfg, runtime, skillTS, softSkillTS, leaderMCPHandles, pool, codeIdx, regIdx, docIdx, false, nil)
		mcpHandles = append(mcpHandles, subHandles...)
		instr = extraInstr + instr

		// Mid-turn steering for sub-agents: while a sub-agent runs the leader is
		// parked, so this callback yields control BACK to the leader the moment a
		// steering note is pending (without consuming it) — the leader, not the
		// sub-agent, decides what to do with it. It resolves the real session id
		// via the context value the surface planted (steerSessionID), not the
		// ephemeral agenttool session.
		beforeModel := []llmagent.BeforeModelCallback{callbacks.BeforeModel}
		if steerStore != nil {
			beforeModel = append(beforeModel, subAgentSteerYield(steerStore))
		}

		// A sub-agent runs in agenttool's own plugin-less runner, so the
		// runner-level permissions AND hooks plugins never see its tool calls —
		// attach their tool-level callbacks here so a sub-agent's
		// Edit/Write/Bash/MCP calls are gated + hooked exactly like the leader's.
		// Before-tool order mirrors the leader's plugin order (events → perms →
		// hooks PreToolUse); the first non-nil return short-circuits the tool.
		// After-tool: events → hooks PostToolUse. Nil ⇒ that layer is skipped, so
		// a sub-agent built with no gate/hooks is byte-identical to before.
		beforeTool := []llmagent.BeforeToolCallback{callbacks.BeforeTool}
		if permGate != nil {
			beforeTool = append(beforeTool, permGate)
		}
		if hooksBeforeTool != nil {
			beforeTool = append(beforeTool, hooksBeforeTool)
		}
		afterTool := []llmagent.AfterToolCallback{callbacks.AfterTool}
		if hooksAfterTool != nil {
			afterTool = append(afterTool, hooksAfterTool)
		}

		sa, sErr := agentkit.New(agentkit.AgentConfig{
			Name:                 cfg.Name,
			Description:          desc,
			Instruction:          instr,
			Model:                agentLLM,
			Tools:                subTools,
			Toolsets:             subToolsets,
			BeforeToolCallbacks:  beforeTool,
			AfterToolCallbacks:   afterTool,
			OnToolErrorCallbacks: []llmagent.OnToolErrorCallback{callbacks.OnToolError},
			BeforeModelCallbacks: beforeModel,
			AfterModelCallbacks:  []llmagent.AfterModelCallback{callbacks.AfterModel},
		})
		if sErr != nil {
			return nil, nil, nil, nil, sErr
		}

		subAgents = append(subAgents, sa)
		subAgentMap[cfg.Name] = sa

		// resumable_sessions swaps the throwaway per-call agenttool for one that
		// keeps durable, re-attachable sessions (the leader resumes via a returned
		// handle). It implements the same runnableTool interface, so the parallel /
		// non-concurrent wrappers below are unchanged — each parallel task still
		// gets its own handle, so durability and fan-out compose.
		var wrapped runnableTool
		if cfg.ResumableSessions {
			rt, rErr := newResumableAgentTool(sa)
			if rErr != nil {
				return nil, nil, nil, nil, fmt.Errorf("resumable sub-agent %q: %w", cfg.Name, rErr)
			}
			wrapped = rt
		} else {
			w, ok := agenttool.New(sa, &agenttool.Config{}).(runnableTool)
			if !ok {
				return nil, nil, nil, nil, fmt.Errorf("agenttool for %q is not runnable", cfg.Name)
			}
			wrapped = w
		}
		// max_instances > 1 exposes a batch/fan-out tool that runs several
		// independent invocations of this sub-agent in parallel; <= 1 keeps the
		// single-task, one-at-a-time tool (today's behaviour).
		if cfg.MaxInstances > 1 {
			leaderSubTools = append(leaderSubTools, newParallelAgentTool(wrapped, cfg.MaxInstances))
		} else {
			leaderSubTools = append(leaderSubTools, newNonConcurrentTool(wrapped))
		}
	}
	return subAgentMap, subAgents, leaderSubTools, mcpHandles, nil
}
