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
	"github.com/blouargant/omnis/internal/attest"
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
	sessIdx sessionIndexFn,
	attestStore *attest.Store,
	steerStore *steer.Store,
	permGate llmagent.BeforeToolCallback,
	hooksBeforeTool llmagent.BeforeToolCallback,
	hooksAfterTool llmagent.AfterToolCallback,
	budgetBeforeTool llmagent.BeforeToolCallback,
	budgetAfterModel llmagent.AfterModelCallback,
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
	return buildSubAgentsFromConfigs(ctx, filtered, runtime, skillTS, softSkillTS, leaderMCPHandles, pool, modelForAgent, callbacks, codeIdx, regIdx, docIdx, sessIdx, attestStore, steerStore, permGate, hooksBeforeTool, hooksAfterTool, budgetBeforeTool, budgetAfterModel)
}

// buildSubAgentsFromConfigs wires every passed-in agent configuration as a
// sub-agent, plus every agent reachable from one through `subagents` (nested
// delegation). Returns:
//   - subAgentMap   : name → agent (the whole closure, nested agents included)
//   - subAgents     : ordered slice for AgentLoader
//   - leaderSubTools: agenttool wrappers to append to the leader's tool list, for
//     the DIRECT members only, in declaration order.
//   - mcpHandles   : pooled MCP handles acquired for sub-agent overrides,
//     to be released by the calling Instance on Close.
//
// The caller is responsible for filtering out the leader and any agent it
// does not want exposed. modelForAgent must instantiate an LLM for the
// given config. Because sub-agents run in their own internal runner that does
// NOT inherit runner-level plugins, everything a sub-agent needs is attached as
// agent-level callbacks here: `callbacks` (its tool/model activity reaches the
// shared event bus), `permGate` (the permission gate — its tool calls are
// asked/denied like the leader's), `hooksBeforeTool`/`hooksAfterTool` (the
// PreToolUse/PostToolUse lifecycle hooks), and `budgetBeforeTool`/`budgetAfterModel`
// (the per-turn spend ceiling — a sub-agent's tool calls and tokens are charged to
// the same session bucket as the leader's, so a max_instances fan-out cannot run an
// unbounded search loop behind the leader's back). All are nil-safe: nil ⇒ that
// layer is skipped, byte-identical to a sub-agent built without it. Because every
// one of them is attached PER AGENT in this loop, they all reach a nested agent
// too — the budget, the per-agent cap, the shaper, the permission gate, the hooks
// and the steering yield compose at any depth with no extra wiring.
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
	sessIdx sessionIndexFn,
	attestStore *attest.Store,
	steerStore *steer.Store,
	permGate llmagent.BeforeToolCallback,
	hooksBeforeTool llmagent.BeforeToolCallback,
	hooksAfterTool llmagent.AfterToolCallback,
	budgetBeforeTool llmagent.BeforeToolCallback,
	budgetAfterModel llmagent.AfterModelCallback,
) (
	subAgentMap map[string]adkagent.Agent,
	subAgents []adkagent.Agent,
	leaderSubTools []tool.Tool,
	mcpHandles []*mcpcfg.Handle,
	err error,
) {
	// A squad's `members` are what the LEADER may delegate to; an agent's own
	// `subagents` are what IT may delegate to. Expand the member set with every
	// agent reachable through those edges, then order the closure so each agent is
	// built AFTER the agents it delegates to — wiring a nested delegation tool
	// needs the target's agent object to already exist.
	closure, cErr := subAgentClosure(configs, runtime.Agents)
	if cErr != nil {
		return nil, nil, nil, nil, cErr
	}
	ordered, oErr := topoOrderSubAgents(closure)
	if oErr != nil {
		return nil, nil, nil, nil, oErr
	}
	closureByName := make(map[string]RuntimeAgentConfig, len(closure))
	for _, c := range closure {
		closureByName[c.Name] = c
	}

	subAgentMap = map[string]adkagent.Agent{}

	for _, cfg := range ordered {
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

		subTools, subToolsets, extraInstr, subHandles := toolsForAgentConfig(ctx, cfg, runtime, skillTS, softSkillTS, leaderMCPHandles, pool, codeIdx, regIdx, docIdx, sessIdx, attestStore, false, nil)
		mcpHandles = append(mcpHandles, subHandles...)
		instr = extraInstr + instr

		// Nested delegation: this agent's OWN team, mounted with the same agenttool
		// mechanism a squad root uses, one level down. This is what lets an expensive
		// specialist push bulk retrieval into a cheap gatherer's context instead of
		// accumulating it in its own — retrieval cost is quadratic in ONE agent's tool
		// calls, so who holds the pages decides the bill. The targets are already
		// built (topological order), and each mount point gets a FRESH wrapper.
		if len(cfg.SubAgents) > 0 {
			nestedCfgs := make([]RuntimeAgentConfig, 0, len(cfg.SubAgents))
			for _, dep := range cfg.SubAgents {
				depAgent, ok := subAgentMap[dep]
				if !ok {
					return nil, nil, nil, nil, fmt.Errorf("agent %q: subagent %q was not built", cfg.Name, dep)
				}
				depTool, wErr := wrapSubAgentTool(depAgent, closureByName[dep])
				if wErr != nil {
					return nil, nil, nil, nil, fmt.Errorf("agent %q: %w", cfg.Name, wErr)
				}
				subTools = append(subTools, depTool)
				nestedCfgs = append(nestedCfgs, closureByName[dep])
			}
			// Same capabilities block the squad root gets: the delegation contract and
			// each teammate's description are how this agent knows what its team is for.
			instr += buildSubAgentCapabilitiesBlock(nestedCfgs, runtime)
		}

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

		// The sub-agent's model calls are charged to the turn's token budget. This
		// is the half that matters most: a reviewer/researcher sub-agent can burn
		// millions of prompt tokens across its private flow loop while making only
		// a couple of tool calls the leader can see.
		afterModel := []llmagent.AfterModelCallback{callbacks.AfterModel}
		if budgetAfterModel != nil {
			afterModel = append(afterModel, budgetAfterModel)
		}

		// A sub-agent runs in agenttool's own plugin-less runner, so the
		// runner-level permissions AND hooks plugins never see its tool calls —
		// attach their tool-level callbacks here so a sub-agent's
		// Edit/Write/Bash/MCP calls are gated + hooked exactly like the leader's.
		// Order lives in one place: see beforeToolChain (agent/tool_chain.go).
		beforeTool := beforeToolChain(callbacks.BeforeTool, hooksBeforeTool, permGate, budgetBeforeTool)
		afterTool := []llmagent.AfterToolCallback{callbacks.AfterTool}
		if hooksAfterTool != nil {
			afterTool = append(afterTool, hooksAfterTool)
		}
		// Cap this sub-agent's tool output. The runner-level shaper never reached
		// here (plugins don't cross into agenttool's runner), so a fetched page
		// landed whole in the sub-agent's context and was re-billed on every
		// subsequent model call of its private flow loop — the quadratic blow-up
		// behind research_critic's 9.1M prompt tokens. Last in the chain, so a hook
		// that rewrote the result is shaped too.
		afterTool = append(afterTool, subAgentShaperCallback())

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
			AfterModelCallbacks:  afterModel,
		})
		if sErr != nil {
			return nil, nil, nil, nil, sErr
		}

		subAgents = append(subAgents, sa)
		subAgentMap[cfg.Name] = sa
	}

	// Leader tools: the DIRECT members only, in declared order. An agent reachable
	// solely through another agent's `subagents` (a pure gatherer serving one
	// specialist) is built and event-wired, but never handed to the coordinator —
	// that is what keeps the leader's tool list from growing every time a
	// specialist gains a helper.
	mounted := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		if !cfg.Enabled || mounted[cfg.Name] {
			continue
		}
		sa, ok := subAgentMap[cfg.Name]
		if !ok {
			continue
		}
		mounted[cfg.Name] = true
		lt, wErr := wrapSubAgentTool(sa, cfg)
		if wErr != nil {
			return nil, nil, nil, nil, wErr
		}
		leaderSubTools = append(leaderSubTools, lt)
	}
	return subAgentMap, subAgents, leaderSubTools, mcpHandles, nil
}

// wrapSubAgentTool turns a built agent into a delegable tool for ONE mount point.
//
// resumable_sessions swaps the throwaway per-call agenttool for one that keeps
// durable, re-attachable sessions (the caller resumes via a returned handle). It
// implements the same runnableTool interface, so the concurrency wrapper is
// unchanged — each parallel call still mints its own handle, so durability and
// fan-out compose.
//
// max_instances is then just the width of that wrapper's semaphore: the schema the
// model sees is always the sub-agent's own single-task shape, and ADK's own
// concurrent dispatch of the calls in one model response is what produces the
// fan-out (see concurrentAgentTool).
//
// A FRESH wrapper is made per mount point, deliberately: the semaphore and the
// resumable wrapper's handle map are per-tool state, so sharing one wrapper between
// the leader and a nested caller would make them contend for each other's slots —
// one caller's fan-out would throttle the other's. The underlying agent is stateless
// (agenttool builds a session per call), so several wrappers over one agent are safe.
func wrapSubAgentTool(sa adkagent.Agent, cfg RuntimeAgentConfig) (tool.Tool, error) {
	var wrapped runnableTool
	if cfg.ResumableSessions {
		rt, err := newResumableAgentTool(sa)
		if err != nil {
			return nil, fmt.Errorf("resumable sub-agent %q: %w", cfg.Name, err)
		}
		wrapped = rt
	} else {
		w, ok := agenttool.New(sa, &agenttool.Config{}).(runnableTool)
		if !ok {
			return nil, fmt.Errorf("agenttool for %q is not runnable", cfg.Name)
		}
		wrapped = w
	}
	return newConcurrentAgentTool(wrapped, cfg.MaxInstances), nil
}
