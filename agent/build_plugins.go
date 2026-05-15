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
)

// buildPlugins wires the runner-level plugins (events bridge, permissions,
// cache stats, compression) and registers the file event logger on the
// shared bus. The bus must already be created so per-agent callbacks can
// be attached at sub-agent construction time.
//
// suffix is the function used to derive a per-session filename suffix
// from (userID, sessionID). buildTimestamp is the global build-level
// timestamp used for the (process-wide) event log filename.
func buildPlugins(
	runtime RuntimeSettings,
	opts Options,
	bus *events.Bus,
	orchestratorLLM model.LLM,
	suffix func(userID, sessionID string) string,
	buildTimestamp string,
) ([]*plugin.Plugin, error) {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		return nil, err
	}
	logger, closeLog, err := events.FileLoggerWithOptions(
		filepath.Join("logs", "agent_events_"+buildTimestamp+".log"),
		events.FileLoggerOptions{FullPayload: opts.DebugLogging},
	)
	if err != nil {
		return nil, err
	}
	// Note: closeLog should be called when shutting down; currently leaked
	// for the lifetime of the process — preserved from the original wiring.
	_ = closeLog

	for _, ev := range []string{
		events.EventBeforeTool, events.EventAfterTool,
		events.EventBeforeModel, events.EventAfterModel,
		events.EventToolError,
		events.EventSessionStart, events.EventSessionEnd,
		events.EventRunStart, events.EventRunEnd,
		events.EventCurateNow,
	} {
		bus.On(ev, logger)
	}

	var plugins []*plugin.Plugin
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
			return filepath.Join("logs", fmt.Sprintf("agent_memory_%s.md", suffix(userID, sessionID)))
		},
		// Per-session State Log path — consumed by the curator agent
		// after EventSessionEnd to mine successful procedures.
		StateLogPathFunc: func(userID, sessionID string) string {
			return filepath.Join("logs", fmt.Sprintf("agent_statelog_%s.json", suffix(userID, sessionID)))
		},
		LLM:      orchestratorLLM,
		EventBus: bus,
	}); err == nil {
		plugins = append(plugins, cmp)
		// NOTE: compact_now tool returned here is intentionally not mounted
		// on the lead in this entry-point; mount it explicitly when wiring
		// a custom agent (see examples/s06_compress for the pattern).
	}
	return plugins, nil
}

// buildPermissionsPlugin loads the base permissions config, then scans every
// agent's skills directory for per-skill permissions.yaml overlays and merges
// them together. Skill rules are appended after base rules so the base config
// always takes precedence within each tier.
func buildPermissionsPlugin(runtime RuntimeSettings) (*plugin.Plugin, error) {
	base, err := permissions.Load(runtime.PermissionsConfigPath)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var overlays []*permissions.Rules
	for _, agentCfg := range runtime.Agents {
		dir := agentCfg.SkillsDir
		if dir == "" {
			dir = runtime.SkillsDir
		}
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			permPath := filepath.Join(dir, e.Name(), "permissions.yaml")
			// Resolve symlinks so the same registry skill linked from multiple
			// agents is only loaded once.
			key := permPath
			if real, err := filepath.EvalSymlinks(permPath); err == nil {
				key = real
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			r, err := permissions.Load(permPath)
			if err == nil && r.HasRules() {
				overlays = append(overlays, r)
			}
		}
	}

	merged := permissions.Merge(base, overlays...)
	return permissions.NewPluginFromRules("perms", merged, permissions.StdinAsker{})
}
