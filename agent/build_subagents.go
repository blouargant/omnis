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
	"github.com/blouargant/yoke/internal/teammates"
)

// buildSubAgents constructs every enabled sub-agent (skipping leader and
// curator) declared in the runtime config and returns:
//   - subAgentMap   : name → agent
//   - subAgents     : ordered slice for AgentLoader
//   - leaderSubTools: agenttool wrappers (non-concurrent) to append to the
//     leader's tool list, in declaration order.
//
// modelForAgent must instantiate an LLM for the given config. callbacks
// are attached to every sub-agent so its tool/model activity reaches the
// shared event bus (sub-agents run in their own internal runner that does
// not inherit runner-level plugins).
func buildSubAgents(
	ctx context.Context,
	runtime RuntimeSettings,
	be teammates.Backend,
	nameFunc func(u, s, name string) string,
	skillTS, softSkillTS tool.Toolset,
	mcpToolsets []tool.Toolset,
	modelForAgent func(RuntimeAgentConfig) (model.LLM, error),
	callbacks events.AgentCallbacks,
) (
	subAgentMap map[string]adkagent.Agent,
	subAgents []adkagent.Agent,
	leaderSubTools []tool.Tool,
	err error,
) {
	subAgentMap = map[string]adkagent.Agent{}
	seenNames := map[string]bool{"leader": true}

	for _, cfg := range runtime.Agents {
		if cfg.Name == "leader" || !cfg.Enabled || cfg.Name == "curator" {
			continue
		}
		if seenNames[cfg.Name] {
			continue
		}
		seenNames[cfg.Name] = true

		agentLLM, mErr := modelForAgent(cfg)
		if mErr != nil {
			return nil, nil, nil, mErr
		}

		desc := cfg.Description
		if desc == "" {
			desc = defaultAgentDescription(cfg.Name)
		}
		instr := cfg.Instruction
		if instr == "" {
			instr = defaultAgentInstruction(cfg.Name)
		}

		subTools, subToolsets, extraInstr := toolsForAgentConfig(ctx, cfg, runtime, skillTS, softSkillTS, mcpToolsets)
		instr += extraInstr
		if cfg.Mailbox {
			mb := teammates.NewAgent(cfg.Name, be)
			mb.NameFunc = nameFunc
			subTools = append(subTools, mb.Tools()...)
		}

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
			return nil, nil, nil, sErr
		}

		subAgents = append(subAgents, sa)
		subAgentMap[cfg.Name] = sa

		wrapped, ok := agenttool.New(sa, &agenttool.Config{}).(runnableTool)
		if !ok {
			return nil, nil, nil, fmt.Errorf("agenttool for %q is not runnable", cfg.Name)
		}
		leaderSubTools = append(leaderSubTools, newNonConcurrentTool(wrapped))
	}
	return subAgentMap, subAgents, leaderSubTools, nil
}
