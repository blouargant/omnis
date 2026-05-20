// squad.go — one named group of agents (leader + members) wired into a
// runner. Each generation builds N SquadInstances, one per RuntimeSquadConfig
// declared in agent.json. A chat session selects which squad to use when it
// is created; the server then resolves Instance.Squad(name).Runner to drive
// that session's turns.
//
// Squads compose existing agent definitions — they do not redefine agents.
// Skills, tools, and MCP servers are owned by the agents themselves, so two
// squads that include the same member agent share the same per-agent
// configuration (and the MCP pool dedups any subprocess that backs it).
package agent

import (
	"context"
	"fmt"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/core/llm"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/a2a"
	mcpcfg "github.com/blouargant/yoke/internal/mcp"
	"github.com/blouargant/yoke/internal/softskills"
	"github.com/blouargant/yoke/internal/teammates"
	"github.com/blouargant/yoke/internal/worktree"
)

// newModelForAgent instantiates the LLM client for one agent configuration
// using its provider / model / base-url / api-key selection. Shared between
// the leader build path and every squad's sub-agent wiring.
func newModelForAgent(ctx context.Context, cfg RuntimeAgentConfig) (model.LLM, error) {
	m, err := llm.NewWithSelection(ctx, selectionFromAgentConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("model agent %q: %w", cfg.Name, err)
	}
	return m, nil
}

// SquadInstance is the fully-wired tree for one squad inside a generation:
// the leader agent, its wrapped sub-agent tools, its runner, and the plugins
// bound to that leader's model. Per-generation closers (MCP releases,
// plugin teardown) are tracked by the parent Instance, not here.
type SquadInstance struct {
	Name        string
	Description string
	// Members is the ordered list of member agent names (lower-cased), not
	// including the leader. Surface for the web UI / introspection.
	Members []string

	Leader      adkagent.Agent
	SubAgents   map[string]adkagent.Agent
	AgentLoader adkagent.Loader
	Plugins     []*plugin.Plugin

	RunnerConfig runner.Config
	Runner       *runner.Runner

	LeaderCfg                  RuntimeAgentConfig
	LeaderAllowFileAttachments bool
}

// squadBuildResult bundles the artefacts returned by buildSquadInstance so
// the parent Instance can aggregate per-generation teardown work.
type squadBuildResult struct {
	Squad         *SquadInstance
	PluginCloser  func() error
	MCPHandles    []*mcpcfg.Handle
	SubAgentNames []string // members participating in this squad, for the curator gate
}

// buildSquadInstance constructs the leader+sub-agents tree for one squad,
// using agent configurations resolved from the runtime settings. The
// `runtime` snapshot is shared across all squads in the generation.
func buildSquadInstance(
	ctx context.Context,
	infra *Infrastructure,
	opts Options,
	runtime RuntimeSettings,
	squad RuntimeSquadConfig,
) (*squadBuildResult, error) {
	leaderCfg, ok := runtime.AgentConfig(squad.Leader)
	if !ok {
		return nil, fmt.Errorf("squad %q: leader %q not found in agent catalogue", squad.Name, squad.Leader)
	}
	if !leaderCfg.Enabled {
		return nil, fmt.Errorf("squad %q: leader %q is disabled", squad.Name, squad.Leader)
	}

	modelForAgent := func(cfg RuntimeAgentConfig) (model.LLM, error) {
		m, err := newModelForAgent(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
		}
		return m, nil
	}

	orchestratorLLM, err := modelForAgent(leaderCfg)
	if err != nil {
		return nil, err
	}

	// ── Leader tools (closing over infra's session-scoped state holders) ──
	leadTools := []tool.Tool{}
	leadTools = append(leadTools, fstools.New()...)
	leadTools = append(leadTools, fstools.NewCalcTools()...)
	leadTools = append(leadTools, infra.TodoStore.Tools()...)
	leadTools = append(leadTools, infra.TaskGraph.Tools()...)
	leadTools = append(leadTools, worktree.Tools(infra.Repo)...)
	leadTools = append(leadTools, infra.BgQueues.Tool())
	leadTools = append(leadTools, curateSessionTool())

	skillTS, softSkillTS, _, toolsets, leaderHandles := buildLeaderToolsets(ctx, runtime, leaderCfg, infra.MCPPool)
	allMCPHandles := append([]*mcpcfg.Handle(nil), leaderHandles...)

	nameFunc := func(u, s, name string) string { return infra.NameFunc(u, s, name) }

	leadMailbox := teammates.NewAgent(leaderCfg.Name, infra.Backend)
	leadMailbox.NameFunc = nameFunc
	leadMailbox.Registry = infra.Registry
	leadTools = append(leadTools, leadMailbox.Tools()...)

	// Mount remote A2A peers the leader can reach. A2AAgents names entries
	// from a2a_config.json; unknown names are silently skipped so a
	// misconfigured leader still boots. Instruction text describing each
	// peer is assembled into leaderInstruction below.
	var a2aPeers []a2a.Agent
	if len(leaderCfg.A2AAgents) > 0 && runtime.A2AConfigPath != "" {
		if a2aCfg, err := a2a.Load(runtime.A2AConfigPath); err == nil {
			a2aPeers = selectA2AAgents(a2aCfg, leaderCfg.A2AAgents)
			if a2aTools := a2a.NewTools(a2aPeers); len(a2aTools) > 0 {
				leadTools = append(leadTools, a2aTools...)
			}
		}
	}

	subAgentCallbacks := infra.Bus.AgentCallbacks(events.PluginOptions{IncludeModelRequest: opts.DebugLogging})

	// Resolve the member agent configs (preserving the order declared in the
	// squad). buildSubAgents below loops over this filtered list rather than
	// the full agent catalogue, so other squads' members don't get wired in.
	memberCfgs := make([]RuntimeAgentConfig, 0, len(squad.Members))
	for _, m := range squad.Members {
		cfg, ok := runtime.AgentConfig(m)
		if !ok || !cfg.Enabled {
			continue
		}
		if cfg.Name == leaderCfg.Name {
			continue
		}
		memberCfgs = append(memberCfgs, cfg)
	}

	subAgentMap, subAgents, subAgentLeaderTools, subAgentMCPHandles, err := buildSubAgentsFromConfigs(
		ctx, memberCfgs, runtime,
		skillTS, softSkillTS, leaderHandles, infra.MCPPool,
		modelForAgent, subAgentCallbacks,
	)
	if err != nil {
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}
	allMCPHandles = append(allMCPHandles, subAgentMCPHandles...)
	leadTools = append(leadTools, subAgentLeaderTools...)

	leadTools = append(leadTools, fstools.NewAskUserTool(infra.AskUserRegistry))

	leaderDescription := leaderCfg.Description
	if leaderDescription == "" {
		leaderDescription = defaultAgentDescription("leader")
	}
	leaderInstruction := leaderCfg.Instruction
	if leaderInstruction == "" {
		leaderInstruction = defaultAgentInstruction("leader")
	}
	if skillTS != nil && softSkillTS != nil {
		leaderInstruction = softskills.LoaderRule + leaderInstruction
	}
	if mounted := filterMCPHandles(leaderHandles, leaderCfg.MCPServers); len(mounted) > 0 {
		names := make([]string, 0, len(mounted))
		for _, h := range mounted {
			names = append(names, h.Name)
		}
		if p := mcpcfg.BuildLoaderProtocol(names); p != "" {
			leaderInstruction = p + "\n" + leaderInstruction
		}
	}
	// Only describe the squad's members in the leader prompt — not every
	// agent in the catalogue — so two squads can specialise the same
	// agent.json by exposing different subsets.
	leaderInstruction += buildSubAgentCapabilitiesBlock(memberCfgs, runtime)
	if len(a2aPeers) > 0 {
		leaderInstruction += buildA2AInstruction(a2aPeers)
	}

	lead, err := agentkit.New(agentkit.AgentConfig{
		Name:        leaderCfg.Name,
		Description: leaderDescription,
		Model:       orchestratorLLM,
		Tools:       leadTools,
		Toolsets:    toolsets,
		Instruction: leaderInstruction,
	})
	if err != nil {
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}

	// ── Plugins (one set per squad — bound to this squad's leader LLM) ──
	suffix := func(u, s string) string { return infra.SessionSuffix(u, s) }
	plugins, pluginCloser, err := buildPlugins(runtime, opts, infra.Bus, orchestratorLLM, suffix, infra.BuildTimestamp)
	if err != nil {
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}

	loader, err := adkagent.NewMultiLoader(lead, subAgents...)
	if err != nil {
		if pluginCloser != nil {
			_ = pluginCloser()
		}
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}

	rc := runner.Config{
		AppName:           runtime.AppName,
		Agent:             lead,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
		PluginConfig:      runner.PluginConfig{Plugins: plugins},
	}
	r, err := runner.New(rc)
	if err != nil {
		if pluginCloser != nil {
			_ = pluginCloser()
		}
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}

	sq := &SquadInstance{
		Name:                       squad.Name,
		Description:                squad.Description,
		Members:                    append([]string(nil), squad.Members...),
		Leader:                     lead,
		SubAgents:                  subAgentMap,
		AgentLoader:                loader,
		Plugins:                    plugins,
		RunnerConfig:               rc,
		Runner:                     r,
		LeaderCfg:                  leaderCfg,
		LeaderAllowFileAttachments: leaderCfg.AllowFileAttachments,
	}
	subAgentNames := make([]string, 0, len(memberCfgs))
	for _, cfg := range memberCfgs {
		subAgentNames = append(subAgentNames, cfg.Name)
	}
	return &squadBuildResult{
		Squad:         sq,
		PluginCloser:  pluginCloser,
		MCPHandles:    allMCPHandles,
		SubAgentNames: subAgentNames,
	}, nil
}
