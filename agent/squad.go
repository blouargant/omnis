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
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/core/llm"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/lsp"
	mcpcfg "github.com/blouargant/omnis/internal/mcp"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/softskills"
	"github.com/blouargant/omnis/internal/teammates"
	"github.com/blouargant/omnis/internal/worktree"
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
	// A leaderless squad (Leader == "") runs its single member directly as the
	// runner root — no coordinator, no sub-agents — so the agent is limited to
	// exactly the tools it declares (plus the always-on essentials below).
	// resolveSquadEntries guarantees a leaderless squad has exactly one member.
	leaderless := squad.Leader == ""
	rootName := squad.Leader
	if leaderless {
		if len(squad.Members) != 1 {
			return nil, fmt.Errorf("squad %q: leaderless squad must have exactly one member", squad.Name)
		}
		rootName = squad.Members[0]
	}
	rootCfg, ok := runtime.AgentConfig(rootName)
	if !ok {
		return nil, fmt.Errorf("squad %q: root agent %q not found in agent catalogue", squad.Name, rootName)
	}
	if !rootCfg.Enabled {
		return nil, fmt.Errorf("squad %q: root agent %q is disabled", squad.Name, rootName)
	}

	modelForAgent := func(cfg RuntimeAgentConfig) (model.LLM, error) {
		// In server mode (DeferModelErrors), a missing API key / base URL must
		// not abort agent build — return a deferred LLM that fails at first use
		// instead, so the server boots and the provider-health banner reports
		// the unreachable provider. A valid selection still builds eagerly.
		if opts.DeferModelErrors {
			return llm.NewDeferredWithSelection(ctx, selectionFromAgentConfig(cfg)), nil
		}
		m, err := newModelForAgent(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
		}
		return m, nil
	}

	orchestratorLLM, err := modelForAgent(rootCfg)
	if err != nil {
		return nil, err
	}

	emb := infra.Embedder(ctx, runtime)
	// buildLeaderToolsets resolves the root's skill/soft-skill/MCP toolsets and
	// acquires its MCP handles (also handed to sub-agents). Its aggregated
	// toolset slice is intentionally discarded: the root's tools are assembled
	// from its declared `tools` groups via toolsForAgentConfig, exactly like a
	// sub-agent, so a specialised root only gets what it asks for.
	skillTS, softSkillTS, _, _, leaderHandles := buildLeaderToolsets(ctx, runtime, rootCfg, infra.MCPPool, emb)
	allMCPHandles := append([]*mcpcfg.Handle(nil), leaderHandles...)

	nameFunc := func(u, s, name string) string { return infra.NameFunc(u, s, name) }
	codeIdx := infra.CodeIndex(ctx, runtime)
	regIdx := infra.RegistryIndex(ctx, runtime)
	docIdx := infra.DocIndex(ctx, runtime)

	// ── Root capability tools — config-driven from rootCfg.Tools ──
	// A coordinating leader (asLeader=true) keeps embedder-backed soft-skill
	// recall; a leaderless root uses sub-agent (glob) soft-skill semantics.
	capTools, capToolsets, capInstruction, capHandles := toolsForAgentConfig(
		ctx, rootCfg, runtime, skillTS, softSkillTS, leaderHandles,
		infra.MCPPool, codeIdx, regIdx, docIdx, !leaderless, emb)
	allMCPHandles = append(allMCPHandles, capHandles...)

	leadTools := append([]tool.Tool{}, capTools...)

	// Infra-scoped coordination groups, gated on declaration. These need
	// session-scoped state holders, so they are mounted here rather than in
	// toolsForAgentConfig.
	keySet := make(map[string]bool, len(rootCfg.Tools))
	for _, k := range rootCfg.Tools {
		keySet[strings.TrimSpace(k)] = true
	}
	if keySet["planning"] {
		leadTools = append(leadTools, infra.TodoStore.Tools()...)
		leadTools = append(leadTools, infra.TaskGraph.Tools()...)
	}
	if keySet["worktree"] {
		leadTools = append(leadTools, worktree.Tools(infra.Repo)...)
	}
	if keySet["bg"] {
		leadTools = append(leadTools, infra.BgQueues.Tools()...)
	}
	if keySet["lsp"] {
		// Language-server code intelligence (navigation + diagnostics + rename).
		// Mounted only when lsp_config.json declares a server, so the toolset
		// stays clean otherwise (additive no-op contract).
		if mgr := infra.LSP(); mgr != nil && mgr.HasServers() {
			leadTools = append(leadTools, lsp.Tools(mgr)...)
		}
	}

	// ── Always-on for any squad root: teammate mailbox + ask_user ──
	// The mailbox keeps the root reachable by other sessions/squads (e.g. a
	// coordinator asking the Helper to install a skill); ask_user lets it prompt
	// the user. Inbound delivery is drained on the canonical session address
	// (Infrastructure.WatchMailbox) regardless of the root agent's name.
	leadMailbox := teammates.NewAgent(rootCfg.Name, infra.Backend)
	leadMailbox.NameFunc = nameFunc
	leadMailbox.Registry = infra.Registry
	// When the host drains the inbox in the background (server pushManager),
	// drop the teammate_check tool: polling would race the background drainer
	// for the single-consumer inbox.
	leadMailbox.SuppressInboxPolling = opts.BackgroundMailboxDelivery
	leadTools = append(leadTools, leadMailbox.Tools()...)

	// ── Omnis routing tools (gated on routing being enabled) ──
	// The router root gets route_to_squad (hand control to another squad); every
	// other squad root gets handoff_to_router (hand control back when a request
	// is out of scope). Both record a per-session directive the host dispatch
	// loop (Manager.RunWithRouting) consumes after the turn finishes.
	routingEnabled := runtime.RouterSquad != ""
	isRouter := routingEnabled && squad.Name == runtime.RouterSquad
	if isRouter {
		targets := routerSquadCatalogue(runtime)
		leadTools = append(leadTools, routeToSquadTool(infra.RouteDirectives, targets))
		// ask_squad lets the router privately check a candidate squad's scope
		// (a hidden, tool-less LLM judgment by that squad's lead) before
		// committing — used only when the router is unsure.
		leadTools = append(leadTools, askSquadTool(infra.RouteDirectives, runtime, targets))
	} else if routingEnabled {
		leadTools = append(leadTools, handoffToRouterTool(infra.RouteDirectives))
	}

	// ── Sub-agents + coordinator-only session tools (skipped when leaderless) ──
	subAgentMap := map[string]adkagent.Agent{}
	var subAgents []adkagent.Agent
	var memberCfgs []RuntimeAgentConfig
	if !leaderless {
		subAgentCallbacks := infra.Bus.AgentCallbacks(events.PluginOptions{IncludeModelRequest: opts.DebugLogging})

		// Resolve the member agent configs (preserving declared order).
		// buildSubAgents loops over this filtered list rather than the full
		// catalogue, so other squads' members don't get wired in.
		memberCfgs = make([]RuntimeAgentConfig, 0, len(squad.Members))
		for _, m := range squad.Members {
			cfg, ok := runtime.AgentConfig(m)
			if !ok || !cfg.Enabled {
				continue
			}
			if cfg.Name == rootCfg.Name {
				continue
			}
			memberCfgs = append(memberCfgs, cfg)
		}

		var subAgentLeaderTools []tool.Tool
		var subAgentMCPHandles []*mcpcfg.Handle
		var berr error
		subAgentMap, subAgents, subAgentLeaderTools, subAgentMCPHandles, berr = buildSubAgentsFromConfigs(
			ctx, memberCfgs, runtime,
			skillTS, softSkillTS, leaderHandles, infra.MCPPool,
			modelForAgent, subAgentCallbacks, codeIdx, regIdx, docIdx, infra.SteerStore,
		)
		if berr != nil {
			for _, h := range allMCPHandles {
				infra.MCPPool.Release(h)
			}
			return nil, fmt.Errorf("squad %q: %w", squad.Name, berr)
		}
		allMCPHandles = append(allMCPHandles, subAgentMCPHandles...)
		leadTools = append(leadTools, subAgentLeaderTools...)

		// Session-lifecycle tools belong to a coordinator that owns the session.
		leadTools = append(leadTools, curateSessionTool())
		// record_session_feedback persists the wrap-session answer to
		// $OMNIS_HOME/logs/agent_feedback_<suffix>.json so the post-session
		// reflector can treat it as the dominant verdict signal.
		leadTools = append(leadTools, softskills.NewFeedbackTool(
			paths.LogsDir(),
			func(u, s string) string { return infra.SessionSuffix(u, s) },
		))
	}

	leadTools = append(leadTools, fstools.NewAskUserTool(infra.AskUserRegistry))

	rootDescription := rootCfg.Description
	if rootDescription == "" {
		rootDescription = defaultAgentDescription(rootCfg.Name)
	}
	rootInstruction := rootCfg.Instruction
	if rootInstruction == "" {
		rootInstruction = defaultAgentInstruction(rootCfg.Name)
	}
	if isRouter {
		// The router never coordinates members. Use the router prompt (shipped
		// registry instruction.md if present, else the built-in) plus the squad
		// catalogue — bypassing the generic default-agent fallback and the
		// capability/sub-agent blocks the router doesn't use.
		base := strings.TrimSpace(ReadAgentInstruction(rootCfg.Name))
		if base == "" {
			base = defaultRouterInstruction()
		}
		rootInstruction = routerCatalogueBlock(runtime) + base
	} else {
		// capInstruction carries the loader protocols for the groups actually
		// mounted (skills, soft-skills + recall, registries, MCP, A2A); prepend it
		// so the tool docs precede the agent's own prompt.
		rootInstruction = capInstruction + rootInstruction
		if !leaderless {
			// Only describe this squad's members so two squads can specialise the
			// same agent.json by exposing different subsets.
			rootInstruction += buildSubAgentCapabilitiesBlock(memberCfgs, runtime)
		}
		if routingEnabled {
			// Non-router squad: tell the leader to hand control back to the
			// router when a request falls outside its scope.
			rootInstruction += routerHandoffProtocolBlock()
		}
		// Make the root aware it can be steered mid-turn and should forward a
		// relevant note to a delegated sub-agent (see steeringAwarenessBlock).
		if infra.SteerStore != nil {
			rootInstruction += steeringAwarenessBlock()
		}
	}

	lead, err := agentkit.New(agentkit.AgentConfig{
		Name:        rootCfg.Name,
		Description: rootDescription,
		Model:       orchestratorLLM,
		Tools:       leadTools,
		Toolsets:    capToolsets,
		Instruction: rootInstruction,
	})
	if err != nil {
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}

	// ── Plugins (one set per squad — bound to this squad's leader LLM) ──
	suffix := func(u, s string) string { return infra.SessionSuffix(u, s) }
	asker := NewAskUserPermissionAsker(infra.AskUserRegistry)
	hooksEngine := infra.Hooks(runtime)
	isRouterSquad := runtime.RouterSquad != "" && squad.Name == runtime.RouterSquad
	plugins, pluginCloser, err := buildPlugins(runtime, opts, infra.Bus, orchestratorLLM, suffix, infra.BuildTimestamp, asker, hooksEngine, infra.SteerStore, infra.LSP(), isRouterSquad)
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
		LeaderCfg:                  rootCfg,
		LeaderAllowFileAttachments: rootCfg.AllowFileAttachments,
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
