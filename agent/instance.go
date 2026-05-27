// instance.go — one generation of the agent built from a snapshot of the
// runtime config. Each call to BuildInstance produces a fresh tree of
// SquadInstances (leader + sub-agents + runner + plugins, one set per
// squad declared in agent.json) on top of the shared Infrastructure.
// Hot-reload replaces the current Instance with a new one; in-flight
// sessions stay pinned to the old Instance until they finish (see
// agent/manager.go). A chat session selects which squad it uses; the
// server resolves Instance.Squad(name).Runner per session.
package agent

import (
	"context"
	"fmt"
	"sort"
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"

	fstools "github.com/blouargant/yoke/core/tools"
)

// Instance is one fully-wired agent generation. It owns the set of
// SquadInstances derived from a single snapshot of RuntimeSettings.
//
// The fields surfaced at the top level (Leader, SubAgents, Runner,
// RunnerConfig, AgentLoader, Plugins, LeaderCfg, LeaderAllowFileAttachments)
// proxy the **default squad** so legacy callers (CLI, TUI, examples) that
// don't yet know about squads keep working unchanged.
type Instance struct {
	Generation int
	Settings   RuntimeSettings

	// Squads is keyed by squad name (lower-cased). Always contains an
	// entry named DefaultSquadName.
	Squads      map[string]*SquadInstance
	DefaultName string

	// Default-squad mirrors for legacy callers — populated after squads
	// are built. New code should prefer Squads[name] or Squad(name).
	Leader      adkagent.Agent
	SubAgents   map[string]adkagent.Agent
	AgentLoader adkagent.Loader
	Plugins     []*plugin.Plugin

	RunnerConfig runner.Config
	Runner       *runner.Runner

	LeaderCfg                  RuntimeAgentConfig
	LeaderAllowFileAttachments bool
	CuratorIdleTimeout         time.Duration

	closers []func() error
}

// Squad returns the SquadInstance with the given name (case-insensitive).
// Falls back to the default squad when name is empty.
func (inst *Instance) Squad(name string) *SquadInstance {
	if inst == nil {
		return nil
	}
	if name == "" {
		return inst.Squads[inst.DefaultName]
	}
	if sq, ok := inst.Squads[lowerTrim(name)]; ok {
		return sq
	}
	return nil
}

// Default returns the default squad.
func (inst *Instance) Default() *SquadInstance {
	if inst == nil {
		return nil
	}
	return inst.Squads[inst.DefaultName]
}

// SquadNames returns the squad names in a stable (sorted) order with the
// default squad first.
func (inst *Instance) SquadNames() []string {
	if inst == nil {
		return nil
	}
	out := make([]string, 0, len(inst.Squads))
	for name := range inst.Squads {
		if name == inst.DefaultName {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return append([]string{inst.DefaultName}, out...)
}

// BuildInstance constructs a new agent generation on top of the shared
// infrastructure. Pass a generation number that uniquely identifies this
// build (the Manager assigns these monotonically; standalone callers can
// pass 1). Returns a fully-wired Instance with one SquadInstance per
// squad declared in the resolved runtime configuration.
func BuildInstance(ctx context.Context, infra *Infrastructure, opts Options, generation int) (*Instance, error) {
	if infra == nil {
		return nil, fmt.Errorf("BuildInstance: infrastructure is nil")
	}

	runtime, err := ResolveRuntimeSettings(opts)
	if err != nil {
		return nil, err
	}

	// Bash filter / timeout are process-globals; reapply on each build so a
	// config reload picks up changes.
	if err := fstools.ConfigureBashOutputFilter(fstools.BashOutputFilterConfig{
		Enabled:    runtime.BashOutputFilterEnabled,
		FiltersDir: runtime.BashOutputFiltersDir,
	}); err != nil {
		return nil, fmt.Errorf("bootstrap bash output filter: %w", err)
	}
	fstools.SetBashDefaultTimeout(time.Duration(runtime.BashTimeoutSeconds) * time.Second)

	if _, ok := runtime.LeaderConfig(); !ok {
		return nil, fmt.Errorf("runtime config: missing mandatory leader agent")
	}
	if len(runtime.Squads) == 0 {
		return nil, fmt.Errorf("runtime config: no squads resolved (expected at least the default squad)")
	}

	inst := &Instance{
		Generation:         generation,
		Settings:           runtime,
		Squads:             make(map[string]*SquadInstance, len(runtime.Squads)),
		DefaultName:        DefaultSquadName,
		CuratorIdleTimeout: runtime.CuratorIdleTimeout,
	}

	// Aggregate sub-agent names across squads so the curator activity gate
	// observes calls into any squad's members.
	subAgentNamesSeen := map[string]bool{}
	var subAgentNamesAll []string

	for _, sqCfg := range runtime.Squads {
		built, err := buildSquadInstance(ctx, infra, opts, runtime, sqCfg)
		if err != nil {
			inst.Close()
			return nil, err
		}
		inst.Squads[built.Squad.Name] = built.Squad
		if built.PluginCloser != nil {
			pc := built.PluginCloser
			inst.closers = append(inst.closers, pc)
		}
		if len(built.MCPHandles) > 0 {
			pool := infra.MCPPool
			handles := built.MCPHandles
			inst.closers = append(inst.closers, func() error {
				for _, h := range handles {
					pool.Release(h)
				}
				return nil
			})
		}
		for _, n := range built.SubAgentNames {
			if !subAgentNamesSeen[n] {
				subAgentNamesSeen[n] = true
				subAgentNamesAll = append(subAgentNamesAll, n)
			}
		}
	}

	// Mirror the default squad onto Instance so legacy callers (CLI, TUI,
	// examples, assembleAgentResult) stay unchanged.
	def := inst.Squads[inst.DefaultName]
	if def == nil {
		inst.Close()
		return nil, fmt.Errorf("runtime config: default squad %q not built", inst.DefaultName)
	}
	inst.Leader = def.Leader
	inst.SubAgents = def.SubAgents
	inst.AgentLoader = def.AgentLoader
	inst.Plugins = def.Plugins
	inst.RunnerConfig = def.RunnerConfig
	inst.Runner = def.Runner
	inst.LeaderCfg = def.LeaderCfg
	inst.LeaderAllowFileAttachments = def.LeaderAllowFileAttachments

	// Load recorder is registered once per generation. It counts
	// `load_softskill` calls and flushes per-session totals to
	// softskills/_stats.json on EventSessionEnd. Leader-loaded skills are
	// keyed by bare name; sub-agent loads are keyed as "<agent>/<name>".
	{
		leaderSeen := map[string]bool{}
		var leaderNames []string
		for _, sq := range runtime.Squads {
			if sq.Leader != "" && !leaderSeen[sq.Leader] {
				leaderSeen[sq.Leader] = true
				leaderNames = append(leaderNames, sq.Leader)
			}
		}
		suffix := func(u, s string) string { return infra.SessionSuffix(u, s) }
		subs := registerLoadRecorderHook(infra.Bus, runtime.SoftSkillsDir, leaderNames, suffix)
		for _, sub := range subs {
			s := sub
			inst.closers = append(inst.closers, func() error { s.Off(); return nil })
		}
	}

	// Sub-agent boundary synthesises EventSubAgentStart / EventSubAgentEnd
	// from the leader's before_tool / after_tool / tool_error payloads
	// whose `tool` matches a sub-agent name. Reflection hooks subscribe to
	// these high-level events instead of filtering every after_tool.
	if len(subAgentNamesAll) > 0 {
		subs := registerSubAgentBoundary(infra.Bus, subAgentNamesAll)
		for _, sub := range subs {
			s := sub
			inst.closers = append(inst.closers, func() error { s.Off(); return nil })
		}
	}

	// Sub-agent load counter + per-invocation tagger: each load_softskill
	// called from a sub-agent bumps LoadedCount, and at EventRunEnd the
	// hook walks the per-run buffer to detect retries + classify the
	// leader's reaction (Phase 6) and applies one tag per loaded skill.
	if len(subAgentNamesAll) > 0 {
		// Collect leader names so the leader-reaction lexical scan
		// only consumes the leader's assistant text (sub-agents have
		// their own AfterModel events on the same bus).
		leaderSeen := map[string]bool{}
		var subLeaderNames []string
		for _, sq := range runtime.Squads {
			if sq.Leader != "" && !leaderSeen[sq.Leader] {
				leaderSeen[sq.Leader] = true
				subLeaderNames = append(subLeaderNames, sq.Leader)
			}
		}
		subs := registerSubAgentLoadHook(infra.Bus, runtime.SoftSkillsDir, subAgentNamesAll, subLeaderNames)
		for _, sub := range subs {
			s := sub
			inst.closers = append(inst.closers, func() error { s.Off(); return nil })
		}
	}

	// Curator hook is registered once per generation (not per squad). The
	// curator listens on the shared event bus; its model is built from the
	// curator agent config. Sub-agent names span every squad's members so
	// the activity gate observes calls in any squad.
	if curatorCfg, ok := runtime.AgentConfig("curator"); ok && curatorCfg.Enabled {
		curatorLLM, cerr := newModelForAgent(ctx, curatorCfg)
		if cerr == nil {
			gate := CuratorGateConfig{
				MinTurns:         runtime.CuratorMinTurns,
				MinSubAgentCalls: runtime.CuratorMinSubAgentCalls,
			}
			// Reflector LLM is optional — when the reflector agent
			// is disabled or unresolvable, curator_hook skips the LLM
			// reflection step and runs with the heuristic Outcome only.
			var reflectorLLM model.LLM
			if reflectorCfg, ok := runtime.AgentConfig("reflector"); ok && reflectorCfg.Enabled {
				if m, rerr := newModelForAgent(ctx, reflectorCfg); rerr == nil {
					reflectorLLM = m
				}
			}
			suffix := func(u, s string) string { return infra.SessionSuffix(u, s) }
			subs := registerCuratorHook(infra.Bus, curatorLLM, reflectorLLM, runtime.SoftSkillsDir, subAgentNamesAll, gate, suffix)
			for _, sub := range subs {
				s := sub
				inst.closers = append(inst.closers, func() error { s.Off(); return nil })
			}
		}
	}

	return inst, nil
}

// Close releases the per-generation resources. Safe to call once.
func (inst *Instance) Close() error {
	if inst == nil {
		return nil
	}
	var firstErr error
	for i := len(inst.closers) - 1; i >= 0; i-- {
		if err := inst.closers[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	inst.closers = nil
	return firstErr
}

// lowerTrim is a small helper used by Squad lookups. Mirrors the
// normalisation done in runtime_config.go without exporting it.
func lowerTrim(s string) string {
	// Implemented as a tiny local helper rather than importing strings to
	// keep the dependency footprint of instance.go light. Lower-case ASCII
	// and trim ASCII whitespace.
	start, end := 0, len(s)
	for start < end {
		c := s[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			start++
			continue
		}
		break
	}
	for end > start {
		c := s[end-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			end--
			continue
		}
		break
	}
	b := make([]byte, 0, end-start)
	for i := start; i < end; i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	return string(b)
}
