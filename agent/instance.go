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

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/adk/v2/runner"

	fstools "github.com/blouargant/omnis/core/tools"
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
	// RouterName is the Omnis router squad name for this generation, or "" when
	// routing is disabled. New chats default to this squad; the dispatch loop
	// (Manager.RunWithRouting) routes handoffs back to it.
	RouterName string
	// StartName is the squad new chats start on when it is not the router
	// (agents.json start_squad / OMNIS_START_SQUAD), or "".
	StartName string

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

	// Inject the Omnis router agent + leaderless router squad when routing is
	// enabled and the config does not already declare them. Done here (not in
	// ResolveRuntimeSettings) so config-only tests see an unmodified squad list.
	ensureRouterSquad(&runtime)

	// The bash timeout is a process-global; reapply on each build so a config
	// reload picks up changes.
	fstools.SetBashDefaultTimeout(time.Duration(runtime.BashTimeoutSeconds) * time.Second)

	if _, ok := runtime.LeaderConfig(); !ok {
		return nil, fmt.Errorf("runtime config: missing mandatory leader agent")
	}
	if len(runtime.Squads) == 0 {
		return nil, fmt.Errorf("runtime config: no squads resolved (expected at least the default squad)")
	}

	inst := &Instance{
		Generation:  generation,
		Settings:    runtime,
		Squads:      make(map[string]*SquadInstance, len(runtime.Squads)),
		DefaultName: DefaultSquadName,
		RouterName:  runtime.RouterSquad,
		StartName:   runtime.StartSquad,
	}

	// Aggregate sub-agent names across squads so the sub-agent event bridge
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

	// Sub-agent boundary synthesises EventSubAgentStart / EventSubAgentEnd
	// from the leader's before_tool / after_tool / tool_error payloads
	// whose `tool` matches a sub-agent name. Reflection hooks subscribe to
	// these high-level events (lifecycle SubagentStop hooks) instead of
	// filtering every after_tool.
	if len(subAgentNamesAll) > 0 {
		subs := registerSubAgentBoundary(infra.Bus, subAgentNamesAll)
		for _, sub := range subs {
			s := sub
			inst.closers = append(inst.closers, func() error { s.Off(); return nil })
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
