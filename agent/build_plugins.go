package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/core/permissions"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/cache"
	"github.com/blouargant/omnis/internal/compress"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/lsp"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/steer"
)

// buildPlugins wires the runner-level plugins (events bridge, permissions,
// cache stats, compression) for one squad. The bus must already be created so
// per-agent callbacks can be attached at sub-agent construction time.
//
// suffix is the function used to derive a per-session filename suffix
// from (userID, sessionID).
//
// NOTE: the event *file logger* is deliberately NOT built here. buildPlugins
// runs once per squad and again for every squad of every hot-reloaded
// generation, while the audit log is one file per build subscribed to one
// process-wide bus — opening it here duplicated every line once per live squad
// and let the independent per-instance writers interleave mid-line. It lives on
// Infrastructure instead (EventLog, memoised sync.Once, closed by
// Infrastructure.Close); the call below is the idempotent "make sure it exists".
//
// The returned closer detaches this squad's own per-generation subscriptions.
// Call it when the agent generation owning these plugins is being torn down
// (Manager.Reload).
func buildPlugins(
	infra *Infrastructure,
	runtime RuntimeSettings,
	opts Options,
	orchestratorLLM model.LLM,
	suffix func(userID, sessionID string) string,
	permPlugin *plugin.Plugin,
	hooksEngine *hooks.Reloader,
	steerStore *steer.Store,
	lspMgr *lsp.Manager,
	isRouterSquad bool,
	budgetBeforeTool llmagent.BeforeToolCallback,
	budgetAfterModel llmagent.AfterModelCallback,
) (plugins []*plugin.Plugin, closer func() error, err error) {
	bus := infra.Bus
	// Process-wide, opened + subscribed exactly once however many squads and
	// generations call this.
	if err := infra.EventLog(opts.DebugLogging); err != nil {
		return nil, nil, err
	}

	var extraCleanups []func()
	closer = func() error {
		for _, c := range extraCleanups {
			c()
		}
		return nil
	}

	if eventsPlugin, err := bus.PluginWithOptions("events", events.PluginOptions{IncludeModelRequest: opts.DebugLogging}); err == nil {
		plugins = append(plugins, eventsPlugin)
	}
	// Claude Code-style lifecycle hooks. The per-squad runner plugin carries the
	// blocking/injecting hooks (PreToolUse/PostToolUse/UserPromptSubmit/Stop) and
	// reads the shared hot-reloading engine; the fire-and-forget lifecycle
	// listeners are wired once on the bus by Infrastructure.Hooks. The router
	// squad mounts none (hooks fire on the answering squad — see buildHooksPlugin).
	//
	// Mounted BEFORE the permission gate, for the reason in beforeToolChain
	// (agent/tool_chain.go): a hook's refusal must not arrive after the user has
	// already approved the call. Keep these two in this order. (It does not make
	// permissionDecision:"allow" work — see that comment.)
	if hp, herr := buildHooksPlugin(hooksEngine, infra.AskUserRegistry, infra.HookState, infra.Attest, isRouterSquad); herr == nil && hp != nil {
		plugins = append(plugins, hp)
	}
	// The permission gate is built once per squad by the caller (so the same
	// enforcement — one approval cache/asker — also attaches to sub-agents) and
	// passed in as a runner plugin here, mounted after the hooks so a hook that
	// already refused the call never reaches the user as a prompt.
	if permPlugin != nil {
		plugins = append(plugins, permPlugin)
	}
	// Per-turn spend ceiling. Counts the root's tool calls + tokens against the
	// session's budget (the same callbacks are attached to every sub-agent, so the
	// whole turn spends from one bucket) and asks the user whether to continue
	// once it runs out. Mounted after the permission/hook gates so a rejected call
	// is not charged. nil callbacks (router squad, or no ceiling configured) ⇒
	// no plugin, byte-identical to before.
	if bp, berr := budgetPlugin("budget", budgetBeforeTool, budgetAfterModel); berr == nil && bp != nil {
		plugins = append(plugins, bp)
	}
	// Mid-turn steering: inject notes the user types while a turn is computing
	// into the running turn at its next model call. Mounted on answering squad
	// roots only — the router never answers, so it never steers (same gating as
	// hooks). No pending notes ⇒ no-op.
	if !isRouterSquad && steerStore != nil {
		if sp, serr := steerPlugin("steer", steerStore); serr == nil && sp != nil {
			plugins = append(plugins, sp)
		}
	}
	// Token-saving coding transforms (edit-fused diagnostics, unchanged-read
	// dedup, universal output shaper). Mounted on answering roots only (the
	// router never runs fs/edit tools); a no-op until a tool result is large,
	// a re-read is identical, or an edit touches a file with a running LSP.
	if !isRouterSquad {
		if cep, cepClean, cerr := codingEfficiencyPlugin("coding_efficiency", lspMgr, bus); cerr == nil && cep != nil {
			plugins = append(plugins, cep)
			if cepClean != nil {
				extraCleanups = append(extraCleanups, cepClean)
			}
		}
	}
	if _, cp, err := cache.Plugin("cache"); err == nil {
		plugins = append(plugins, cp)
	}
	// AGENT.md project memory: inject the resolved hierarchy into the
	// leader/root system instruction per turn (no-op when no AGENT.md exists).
	if amd, err := agentMDPlugin("agentmd"); err == nil {
		plugins = append(plugins, amd)
	}
	// Collection context: inject the session's collection instructions + memory
	// into the answering root's system instruction per turn (no-op when the
	// session has no collection or the collection has no prose). Gated to
	// answering roots — unlike AGENT.md the router stays neutral so a workstream's
	// guidance never colours a routing decision. Prepended after AGENT.md so the
	// broad workstream framing sits outermost, project specifics next.
	if !isRouterSquad {
		if col, err := collectionCtxPlugin("collection_ctx"); err == nil {
			plugins = append(plugins, col)
		}
	}
	if cmp, _, _, err := compress.PluginWithTools("compress", compress.Config{
		// Per-session audit file so concurrent users / sessions
		// never share a counter or overwrite each other's summaries.
		AuditPathFunc: func(userID, sessionID string) string {
			return filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_memory_%s.md", suffix(userID, sessionID)))
		},
		// Per-session State Log path — consumed by the curator agent
		// after EventSessionEnd to mine successful procedures.
		StateLogPathFunc: func(userID, sessionID string) string {
			return filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_statelog_%s.json", suffix(userID, sessionID)))
		},
		LLM:      orchestratorLLM,
		EventBus: bus,
	}); err == nil {
		plugins = append(plugins, cmp)
		// NOTE: compact_now tool returned here is intentionally not mounted
		// on the lead in this entry-point; mount it explicitly when wiring
		// a custom agent (see examples/s06_compress for the pattern).
	}
	return plugins, closer, nil
}

// buildPermissionGate wires the shared permission gate with three rule
// sources:
//
//  1. the base file at runtime.PermissionsConfigPath (project config),
//  2. the user-owned overlay at $HOME/.omnis/config/permissions.json
//     where "Allow in this project" / "Allow always" persistence lands,
//  3. an in-memory skill overlay aggregated from each agent's skills.
//
// A Reloader polls (1) and (2) for changes and atomically swaps the
// merged rule set, so external edits and persisted approvals propagate
// to every running session without restarting the server.
//
// It returns the permissions.Gate (whose Plugin governs the squad root and
// whose Callback is attached to every sub-agent) plus a cleanup that stops the
// reload poller and detaches the session-end approval-cache sweeper; the caller
// (buildSquadInstance) runs the cleanup on generation teardown. Building the
// gate here — once per squad — is what lets the SAME approval cache/asker cover
// both the leader and its sub-agents.
func buildPermissionGate(runtime RuntimeSettings, asker permissions.Asker, bus *events.Bus) (*permissions.Gate, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	// Skill overlays (in-memory; do not need to be polled).
	// Merge across every layer of the skills search chain so overlays from
	// /etc/omnis or .agents/ are not dropped when $HOME/.omnis/registry/skills
	// happens to exist (even empty). First-wins per skill name matches the
	// runtime loader's precedence.
	searchDirs := paths.SkillsAllSearchDirs()
	seenSkill := map[string]bool{}
	seenPerm := map[string]bool{}
	var skillOverlays []*permissions.Config
	for _, agentCfg := range runtime.Agents {
		skillNames := agentCfg.Skills
		if len(skillNames) == 0 {
			for _, dir := range searchDirs {
				entries, _ := os.ReadDir(dir)
				for _, e := range entries {
					if !e.IsDir() || seenSkill[e.Name()] {
						continue
					}
					seenSkill[e.Name()] = true
					skillNames = append(skillNames, e.Name())
				}
			}
		}
		for _, skillName := range skillNames {
			for _, dir := range searchDirs {
				permPath := filepath.Join(dir, skillName, "permissions.json")
				if seenPerm[permPath] {
					break
				}
				if _, err := os.Stat(permPath); err != nil {
					continue
				}
				seenPerm[permPath] = true
				r, lerr := permissions.Load(permPath)
				if lerr == nil && r.HasRules() {
					skillOverlays = append(skillOverlays, r)
				}
				break
			}
		}
	}

	// Merge permissions across EVERY layer of the search chain, low→high, so a
	// per-user overlay evolves with the package-shipped rule set (the safe
	// read-only allow-list) instead of shadowing it. Candidates include the
	// not-yet-created user-layer file so the reloader watches for the first
	// "Allow always" persist. base = lowest layer (system), overlays ascend to
	// local; the in-memory skill overlays stay highest (staticOverlays).
	layerPaths := paths.ConfigLayerCandidates("permissions.json")
	basePath := runtime.PermissionsConfigPath
	var overlayPaths []string
	if len(layerPaths) > 0 {
		basePath = layerPaths[0]
		overlayPaths = layerPaths[1:]
	}

	reloader := permissions.NewReloader(basePath, overlayPaths, skillOverlays)
	reloader.Start(ctx)

	// Where "Allow always" / "Allow in this project" persistence lands (the
	// layer-aware user/local write target). It is already among the watched
	// overlay candidates above, so a persist is picked up by the reloader.
	userConfigPath := filepath.Join(paths.WriteDirForLayer(layerForConfigFile("permissions.json")), "permissions.json")

	if asker == nil {
		asker = permissions.StdinAsker{}
	}
	gate, err := permissions.NewGate(permissions.PluginConfig{
		Name:           "perms",
		Source:         reloader,
		Asker:          asker,
		UserConfigPath: userConfigPath,
		OnPersist:      func() { _ = reloader.Refresh() },
		// Scope cwd-bound rules ("Allow in this project") to the session's
		// working directory — the folder the user navigated to via the Folders
		// panel / "!cd" — so a grant applies to that directory and its children
		// but not its parents. Resolved identically to where the tools actually
		// run; falls back to the process cwd when no per-session cwd resolves.
		CWDFunc: func(tc adk.ToolContext) string {
			if d := fstools.CwdForContext(tc); d != "" {
				return d
			}
			d, _ := os.Getwd()
			return d
		},
		// Recover the user-facing session id for a sub-agent tool call (which
		// runs under an ephemeral agenttool session) so its approval cache keys —
		// and grants like "Allow all Edit this session" — line up with the
		// leader's. Falls back to tc.SessionID() for the root's own calls.
		SessionFunc: func(tc adk.ToolContext) string {
			if id := steerSessionID(tc); id != "" {
				return id
			}
			return tc.SessionID()
		},
		Debug: os.Getenv("OMNIS_DEBUG") != "",
	})
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if gate.Cleaner != nil && bus != nil {
		sub := bus.Subscribe(events.EventSessionEnd, func(_ string, payload map[string]any) {
			sid, _ := payload["session_id"].(string)
			if sid != "" {
				gate.Cleaner.Forget(sid)
			}
		})
		// Detach on cancel so a hot-reload tears down the subscription.
		go func() { <-ctx.Done(); sub.Off() }()
	}
	return gate, cancel, nil
}
