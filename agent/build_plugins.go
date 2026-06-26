package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/core/permissions"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/cache"
	"github.com/blouargant/omnis/internal/compress"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/steer"
)

// buildPlugins wires the runner-level plugins (events bridge, permissions,
// cache stats, compression) and registers the file event logger on the
// shared bus. The bus must already be created so per-agent callbacks can
// be attached at sub-agent construction time.
//
// suffix is the function used to derive a per-session filename suffix
// from (userID, sessionID). buildTimestamp is the global build-level
// timestamp used for the (process-wide) event log filename.
//
// The returned closer detaches the file-logger subscriptions and closes the
// event log file. Call it when the agent generation owning these plugins is
// being torn down (Manager.Reload).
func buildPlugins(
	runtime RuntimeSettings,
	opts Options,
	bus *events.Bus,
	orchestratorLLM model.LLM,
	suffix func(userID, sessionID string) string,
	buildTimestamp string,
	asker permissions.Asker,
	hooksEngine *hooks.Reloader,
	steerStore *steer.Store,
	isRouterSquad bool,
) (plugins []*plugin.Plugin, closer func() error, err error) {
	logsDir := paths.LogsDir()
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, nil, err
	}
	logger, closeLog, err := events.FileLoggerWithOptions(
		filepath.Join(logsDir, "agent_events_"+buildTimestamp+".log"),
		events.FileLoggerOptions{FullPayload: opts.DebugLogging},
	)
	if err != nil {
		return nil, nil, err
	}

	var loggerSubs []*events.Subscription
	for _, ev := range []string{
		events.EventBeforeTool, events.EventAfterTool,
		events.EventBeforeModel, events.EventAfterModel,
		events.EventToolError,
		events.EventSessionStart, events.EventSessionEnd,
		events.EventRunStart, events.EventRunEnd,
		events.EventCurateNow,
	} {
		loggerSubs = append(loggerSubs, bus.Subscribe(ev, logger))
	}
	permsCtx, cancelPerms := context.WithCancel(context.Background())
	closer = func() error {
		cancelPerms()
		for _, sub := range loggerSubs {
			sub.Off()
		}
		if closeLog != nil {
			return closeLog()
		}
		return nil
	}

	if eventsPlugin, err := bus.PluginWithOptions("events", events.PluginOptions{IncludeModelRequest: opts.DebugLogging}); err == nil {
		plugins = append(plugins, eventsPlugin)
	}
	if perms, err := buildPermissionsPlugin(permsCtx, runtime, asker, bus); err == nil {
		plugins = append(plugins, perms)
	}
	// Claude Code-style lifecycle hooks. The per-squad runner plugin carries the
	// blocking/injecting hooks (PreToolUse/PostToolUse/UserPromptSubmit/Stop) and
	// reads the shared hot-reloading engine; the fire-and-forget lifecycle
	// listeners are wired once on the bus by Infrastructure.Hooks. The router
	// squad mounts none (hooks fire on the answering squad — see buildHooksPlugin).
	if hp, herr := buildHooksPlugin(hooksEngine, isRouterSquad); herr == nil && hp != nil {
		plugins = append(plugins, hp)
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
	if _, cp, err := cache.Plugin("cache"); err == nil {
		plugins = append(plugins, cp)
	}
	// AGENT.md project memory: inject the resolved hierarchy into the
	// leader/root system instruction per turn (no-op when no AGENT.md exists).
	if amd, err := agentMDPlugin("agentmd"); err == nil {
		plugins = append(plugins, amd)
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

// buildPermissionsPlugin wires the permissions plugin with three rule
// sources:
//
//  1. the base file at runtime.PermissionsConfigPath (project config),
//  2. the user-owned overlay at $HOME/.omnis/config/permissions.json
//     where "Allow in this project" / "Allow always" persistence lands,
//  3. an in-memory skill overlay aggregated from each agent's skills.
//
// A Reloader polls (1) and (2) for changes and atomically swaps the
// merged rule set, so external edits and persisted approvals propagate
// to every running session without restarting the server. ctx cancels
// the polling loop on generation teardown.
func buildPermissionsPlugin(ctx context.Context, runtime RuntimeSettings, asker permissions.Asker, bus *events.Bus) (*plugin.Plugin, error) {
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

	userConfigPath := filepath.Join(paths.WriteDirForLayer(layerForConfigFile("permissions.json")), "permissions.json")
	// Only treat the user overlay as a polled file when it's a separate
	// path from the base — otherwise we'd be reloading the same file twice.
	var overlayPaths []string
	if userConfigPath != runtime.PermissionsConfigPath {
		overlayPaths = append(overlayPaths, userConfigPath)
	}

	reloader := permissions.NewReloader(runtime.PermissionsConfigPath, overlayPaths, skillOverlays)
	reloader.Start(ctx)

	if asker == nil {
		asker = permissions.StdinAsker{}
	}
	plug, cleaner, err := permissions.NewPluginFromConfig(permissions.PluginConfig{
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
		CWDFunc: func(tc tool.Context) string {
			if d := fstools.CwdForContext(tc); d != "" {
				return d
			}
			d, _ := os.Getwd()
			return d
		},
		Debug: os.Getenv("OMNIS_DEBUG") != "",
	})
	if err == nil && cleaner != nil && bus != nil {
		sub := bus.Subscribe(events.EventSessionEnd, func(_ string, payload map[string]any) {
			sid, _ := payload["session_id"].(string)
			if sid != "" {
				cleaner.Forget(sid)
			}
		})
		// Detach on ctx cancel so a hot-reload tears down the subscription.
		go func() { <-ctx.Done(); sub.Off() }()
	}
	return plug, err
}
