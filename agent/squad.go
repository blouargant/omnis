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
	"sort"
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
	"github.com/blouargant/omnis/internal/sessindex"
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
	// Resolved lazily on the first search_sessions call, not here: the session
	// index is used in bursts and unloads itself between them, so opening it at
	// every squad build (and every hot-reload) would be pure cost.
	sessIdx := sessionIndexFn(func() *sessindex.Index { return infra.SessionIndex(ctx, runtime) })

	// ── Root capability tools — config-driven from rootCfg.Tools ──
	// A coordinating leader (asLeader=true) keeps embedder-backed soft-skill
	// recall; a leaderless root uses sub-agent (glob) soft-skill semantics.
	capTools, capToolsets, capInstruction, capHandles := toolsForAgentConfig(
		ctx, rootCfg, runtime, skillTS, softSkillTS, leaderHandles,
		infra.MCPPool, codeIdx, regIdx, docIdx, sessIdx, !leaderless, emb)
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
	// spawn_session — hand a parallel task to a fresh session. Leader-only
	// (skipped when leaderless, so neither the router nor a single-specialist
	// root gets it) and surface-gated (opts.SessionSpawning is set only by the
	// server, which drains SpawnDirectives after the turn and materialises the
	// sessions). Removing "spawn" from the leader's tools is the user opt-out.
	if keySet["spawn"] && opts.SessionSpawning && !leaderless {
		leadTools = append(leadTools, spawnSessionTool(infra.SpawnDirectives, routerSquadCatalogue(runtime)))
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

	// ── Permission gate (built once, shared by leader + sub-agents) ──
	// Its runner Plugin governs the squad root's own tools; the same Callback is
	// attached to every sub-agent (which run in agenttool's plugin-less runner and
	// so never see the Plugin), so a sub-agent's Edit/Write/Bash/MCP calls are
	// asked/denied identically and share the leader's approval cache. Built before
	// the sub-agents because buildSubAgentsFromConfigs needs the callback.
	asker := NewAskUserPermissionAsker(infra.AskUserRegistry)
	permGate, permGateClose, err := buildPermissionGate(runtime, asker, infra.Bus)
	if err != nil {
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: permission gate: %w", squad.Name, err)
	}

	// ── Tool-level lifecycle hooks (PreToolUse/PostToolUse), shared like the gate ──
	// The runner plugin (buildPlugins → buildHooksPlugin) carries these for the
	// squad root; the same tool-level callbacks are attached to every sub-agent so
	// a sub-agent's internal tool calls fire PreToolUse/PostToolUse too. Built here
	// (before the sub-agents) from the process-wide hooks engine; nil for the
	// router squad. UserPromptSubmit/Stop stay leader-only (see hookToolCallbacks).
	hooksEngine := infra.Hooks(runtime)
	hooksBeforeTool, hooksAfterTool := hookToolCallbacks(hooksEngine, isRouter)

	// ── Per-turn spend ceiling, shared like the gate ──
	// Same two-place wiring, for the same reason: the plugin (buildPlugins) counts
	// the root's tool calls + tokens, and the SAME callbacks are attached to every
	// sub-agent so a max_instances fan-out spends from one bucket instead of N
	// invisible ones. Without the sub-agent half a leader could delegate an
	// unbounded search loop and never be charged for it — which is exactly what a
	// runaway deep-research turn did. Nil for the router squad / unlimited config.
	budgetBeforeTool, budgetAfterModel := budgetCallbacks(infra.Budget, infra.AskUserRegistry, runtime.TurnBudget, isRouter)

	// ── Sub-agents + coordinator-only session tools ──
	//
	// What the ROOT may delegate to differs by squad shape:
	//   - a coordinating leader delegates to the squad's `members`;
	//   - a LEADERLESS root has no members to coordinate, but it may still have a
	//     team of its OWN — the `subagents` declared on its agent.json. That is
	//     what lets the Helper (a leaderless single specialist) hand a
	//     "find the chat where we…" request to session_search without the Helper
	//     squad growing a coordinator. `subagents` exists precisely to decouple
	//     "may delegate" from "is a coordinator" (see the gatherer doctrine), so a
	//     leaderless root is not a reason to skip building a team.
	// The coordinator-only session-lifecycle tools below stay leader-only.
	subAgentMap := map[string]adkagent.Agent{}
	var subAgents []adkagent.Agent
	var memberCfgs []RuntimeAgentConfig
	var delegable []string
	if leaderless {
		delegable = rootCfg.SubAgents
	} else {
		delegable = squad.Members
	}
	if len(delegable) > 0 {
		subAgentCallbacks := infra.Bus.AgentCallbacks(events.PluginOptions{IncludeModelRequest: opts.DebugLogging})

		// Resolve the delegable agent configs (preserving declared order).
		// buildSubAgentsFromConfigs loops over this filtered list rather than the
		// full catalogue, so other squads' members don't get wired in.
		memberCfgs = make([]RuntimeAgentConfig, 0, len(delegable))
		for _, m := range delegable {
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
			modelForAgent, subAgentCallbacks, codeIdx, regIdx, docIdx, sessIdx, infra.SteerStore,
			permGate.Callback, hooksBeforeTool, hooksAfterTool,
			budgetBeforeTool, budgetAfterModel,
		)
		if berr != nil {
			permGateClose()
			for _, h := range allMCPHandles {
				infra.MCPPool.Release(h)
			}
			return nil, fmt.Errorf("squad %q: %w", squad.Name, berr)
		}
		allMCPHandles = append(allMCPHandles, subAgentMCPHandles...)
		leadTools = append(leadTools, subAgentLeaderTools...)
	}

	// Session-lifecycle tools belong to a COORDINATOR that owns the session —
	// keyed on leaderless, not on "has a team": a leaderless root may now hold its
	// own `subagents` (above) without thereby becoming a session coordinator.
	if !leaderless {
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
		if len(memberCfgs) > 0 {
			// Describe exactly the agents THIS root can delegate to — a
			// coordinating leader's squad members, or a leaderless root's own
			// `subagents` — so two squads can specialise the same agent.json by
			// exposing different subsets, and so a leaderless root actually knows
			// about the team it was just given.
			rootInstruction += buildSubAgentCapabilitiesBlock(memberCfgs, runtime)
		}
		if routingEnabled {
			// Non-router squad: tell the leader to hand control back to the
			// router when a request falls outside its scope.
			rootInstruction += routerHandoffProtocolBlock()
		}
		// Reply in the user's language, delegate in English, and never let a
		// paraphrase strip the scope their wording carried (see
		// languagePolicyBlock).
		rootInstruction += languagePolicyBlock(!leaderless)
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
		permGateClose()
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}

	// ── Plugins (one set per squad — bound to this squad's leader LLM) ──
	// The permission gate + hooks engine built above are mounted here (the gate as
	// the root's runner plugin; buildHooksPlugin reads the same hooksEngine).
	suffix := func(u, s string) string { return infra.SessionSuffix(u, s) }
	isRouterSquad := runtime.RouterSquad != "" && squad.Name == runtime.RouterSquad
	plugins, pluginCloser, err := buildPlugins(infra, runtime, opts, orchestratorLLM, suffix, permGate.Plugin, hooksEngine, infra.SteerStore, infra.LSP(), isRouterSquad, budgetBeforeTool, budgetAfterModel)
	if err != nil {
		permGateClose()
		for _, h := range allMCPHandles {
			infra.MCPPool.Release(h)
		}
		return nil, fmt.Errorf("squad %q: %w", squad.Name, err)
	}
	// Tear down the gate's reload poller + session-end sweeper alongside the
	// squad's other plugins on generation teardown.
	squadCloser := func() error {
		permGateClose()
		if pluginCloser != nil {
			return pluginCloser()
		}
		return nil
	}

	loader, err := adkagent.NewMultiLoader(lead, subAgents...)
	if err != nil {
		_ = squadCloser()
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
		_ = squadCloser()
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
	// Every agent that can be INVOKED as a sub-agent, not just the squad's direct
	// members: a nested gatherer (reachable only through another agent's
	// `subagents`) is invoked exactly like a member, so it must be in this set or
	// registerSubAgentEvents would never re-emit EventSubAgentStart/End for it and
	// the reflection pipeline would be blind to the calls. Members first (declared
	// order), then the nested-only agents, sorted for determinism.
	subAgentNames := make([]string, 0, len(subAgentMap))
	listed := make(map[string]bool, len(subAgentMap))
	for _, cfg := range memberCfgs {
		if _, built := subAgentMap[cfg.Name]; built && !listed[cfg.Name] {
			listed[cfg.Name] = true
			subAgentNames = append(subAgentNames, cfg.Name)
		}
	}
	nested := make([]string, 0, len(subAgentMap))
	for name := range subAgentMap {
		if !listed[name] {
			nested = append(nested, name)
		}
	}
	sort.Strings(nested)
	subAgentNames = append(subAgentNames, nested...)

	return &squadBuildResult{
		Squad:         sq,
		PluginCloser:  squadCloser,
		MCPHandles:    allMCPHandles,
		SubAgentNames: subAgentNames,
	}, nil
}
