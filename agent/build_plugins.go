package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/core/permissions"
	"github.com/blouargant/yoke/internal/cache"
	"github.com/blouargant/yoke/internal/compress"
	"github.com/blouargant/yoke/internal/paths"
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
	closer = func() error {
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
	if perms, err := buildPermissionsPlugin(runtime); err == nil {
		plugins = append(plugins, perms)
	}
	if _, cp, err := cache.Plugin("cache"); err == nil {
		plugins = append(plugins, cp)
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

// buildPermissionsPlugin loads the base permissions config, then scans every
// agent's skills directory for per-skill permissions.json overlays and merges
// them together. Skill rules are appended after base rules so the base config
// always takes precedence within each tier.
func buildPermissionsPlugin(runtime RuntimeSettings) (*plugin.Plugin, error) {
	base, err := permissions.Load(runtime.PermissionsConfigPath)
	if err != nil {
		return nil, err
	}

	// Load permissions.json overlays from skill directories in the registry.
	// Each agent lists the skills it uses; we aggregate all distinct skills.
	registryDir := paths.SkillsRegistryDir()
	seen := map[string]bool{}
	var overlays []*permissions.Rules
	for _, agentCfg := range runtime.Agents {
		skillNames := agentCfg.Skills
		if len(skillNames) == 0 {
			// No explicit list — scan all installed skills.
			entries, _ := os.ReadDir(registryDir)
			for _, e := range entries {
				if e.IsDir() {
					skillNames = append(skillNames, e.Name())
				}
			}
		}
		for _, skillName := range skillNames {
			permPath := filepath.Join(registryDir, skillName, "permissions.json")
			if seen[permPath] {
				continue
			}
			seen[permPath] = true
			r, err := permissions.Load(permPath)
			if err == nil && r.HasRules() {
				overlays = append(overlays, r)
			}
		}
	}

	merged := permissions.Merge(base, overlays...)
	return permissions.NewPluginFromRules("perms", merged, permissions.StdinAsker{})
}
