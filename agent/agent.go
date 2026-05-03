// Package agent provides a ready-to-use agent-toolkit agent that can be
// imported and used by other Go projects.
//
// Usage:
//
//	result, err := agent.NewAgent(ctx, agent.Options{})
//	runner, err := runner.New(result.RunnerConfig)
package agent

import (
	"context"
	"fmt"
	"os"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/events"
	"github.com/blouargant/agent-toolkit/core/llm"
	"github.com/blouargant/agent-toolkit/core/permissions"
	fstools "github.com/blouargant/agent-toolkit/core/tools"
	"github.com/blouargant/agent-toolkit/internal/bg"
	"github.com/blouargant/agent-toolkit/internal/cache"
	"github.com/blouargant/agent-toolkit/internal/compress"
	mcpcfg "github.com/blouargant/agent-toolkit/internal/mcp"
	"github.com/blouargant/agent-toolkit/internal/skills"
	"github.com/blouargant/agent-toolkit/internal/softskills"
	"github.com/blouargant/agent-toolkit/internal/tasks"
	"github.com/blouargant/agent-toolkit/internal/teammates"
	"github.com/blouargant/agent-toolkit/internal/todo"
	"github.com/blouargant/agent-toolkit/internal/worktree"
)

// AgentResult holds the fully configured agent and its supporting components.
type AgentResult struct {
	// Agent is the lead coordinator agent ready to use.
	Agent adkagent.Agent
	// Investigator is the investigator sub-agent.
	Investigator adkagent.Agent
	// Summariser is the summariser sub-agent.
	Summariser adkagent.Agent
	// AgentLoader is the ADK agent loader for the launcher.
	AgentLoader adkagent.Loader
	// RunnerConfig is the ADK runner configuration for this agent.
	RunnerConfig runner.Config
	// Plugins are the plugins wired to this agent.
	Plugins []*plugin.Plugin
	// EventBus is the event bus for this agent.
	EventBus *events.Bus
}

// Options allows customizing the agent creation.
type Options struct {
	// SkillsDir is the directory to load skills from (default: "skills").
	SkillsDir string
	// SoftSkillsDir is the directory to load curator-generated soft-skills
	// from (default: "softskills"). Created if missing.
	SoftSkillsDir string
	// DisableAutoCurate disables the EventSessionEnd hook that fires the
	// curator agent in the background. The manual `curate` CLI subcommand
	// remains available regardless.
	DisableAutoCurate bool
	// Repo is the repository root for worktree tools (default: current working directory).
	Repo string
	// MCPSConfigPath is the path to the MCP config file (default: "config/mcp_config.yaml").
	MCPSConfigPath string
	// PermissionsConfigPath is the path to the permissions config (default: "config/permissions.yaml").
	PermissionsConfigPath string
	// AppName is the application name for the runner (default: "agent-toolkit").
	AppName string
	// ConfigPath is the runtime YAML configuration path (default: "config/agent.yaml").
	ConfigPath string
	// ConfigPathStrict returns an error when ConfigPath does not exist.
	ConfigPathStrict bool
	// ModelProvider overrides the global model provider for all roles not explicitly configured.
	ModelProvider string
	// ModelName overrides the global model for all roles not explicitly configured.
	ModelName string
	// ModelBaseURL overrides the global model base URL for all roles not explicitly configured.
	ModelBaseURL string
	// ModelAPIKey overrides the global model API key for all roles not explicitly configured.
	ModelAPIKey string
	// RoleModels overrides role-specific provider/model settings.
	RoleModels map[string]RoleModelConfig
	// CuratorEnabled explicitly enables/disables curator auto-run.
	CuratorEnabled *bool
}

// NewAgent creates a fully configured agent-toolkit agent that can be used
// by other Go projects. It returns the agent, runner config, and supporting
// components.
//
// The caller is responsible for closing any resources (the function returns
// a close function if needed).
func NewAgent(ctx context.Context, opts Options) (*AgentResult, error) {
	runtime, err := ResolveRuntimeSettings(opts)
	if err != nil {
		return nil, err
	}
	if runtime.SoftSkillsDir == "" {
		runtime.SoftSkillsDir = softskills.DefaultDir
	}
	if opts.Repo == "" {
		repo, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("bootstrap: getwd: %w", err)
		}
		opts.Repo = repo
	}

	modelForRole := func(role string) (model.LLM, error) {
		selection := runtime.RoleSelection(role)
		m, err := llm.NewWithSelection(ctx, llm.Selection{
			Provider: selection.Provider,
			Model:    selection.Model,
			BaseURL:  selection.BaseURL,
			APIKey:   selection.APIKey,
		})
		if err != nil {
			return nil, fmt.Errorf("model role %q: %w", role, err)
		}
		return m, nil
	}

	orchestratorLLM, err := modelForRole("orchestrator")
	if err != nil {
		return nil, err
	}
	investigatorLLM, err := modelForRole("investigator")
	if err != nil {
		return nil, err
	}
	summariserLLM, err := modelForRole("summariser")
	if err != nil {
		return nil, err
	}

	// ── Toolsets ─────────────────────────────────────────────────────────
	// All session-scoped components share the same (userID, sessionID)
	// → suffix mapping so a given session's task graph, plan, mailbox
	// and background queue all line up on disk and on the wire.
	sessionSuffix := SessionSuffix

	// Per-session task graph (.agent_tasks_<u>_<s>.json).
	g := tasks.NewSessionScoped("", func(u, s string) string {
		return fmt.Sprintf(".agent_tasks_%s.json", sessionSuffix(u, s))
	})
	// Per-session background notification queue.
	q := bg.NewSessionQueues(32)
	// Per-session todo plan (.agent_todo_<u>_<s>.json).
	store := todo.NewSessionScoped("", func(u, s string) string {
		return fmt.Sprintf(".agent_todo_%s.json", sessionSuffix(u, s))
	})

	leadTools := []tool.Tool{}
	leadTools = append(leadTools, fstools.New()...)
	leadTools = append(leadTools, store.Tools()...)
	leadTools = append(leadTools, g.Tools()...)
	leadTools = append(leadTools, worktree.Tools(opts.Repo)...)
	leadTools = append(leadTools, q.Tool())
	leadTools = append(leadTools, curateSessionTool())

	var toolsets []tool.Toolset
	if ts, err := skills.Toolset(ctx, runtime.SkillsDir); err == nil {
		toolsets = append(toolsets, ts)
	}
	if sts, err := softskills.Toolset(ctx, runtime.SoftSkillsDir); err == nil {
		toolsets = append(toolsets, sts)
	}
	if mc, err := mcpcfg.Load(runtime.MCPConfigPath); err == nil {
		if mts, err := mc.Toolsets(); err == nil {
			toolsets = append(toolsets, mts...)
		}
	}

	be, err := teammates.ChooseBackend()
	if err != nil {
		return nil, fmt.Errorf("mailbox backend: %w", err)
	}
	leadMailbox := teammates.NewAgent("lead", be)
	// Namespace mailbox names per session so two concurrent sessions
	// running an agent named "lead" never share an inbox.
	leadMailbox.NameFunc = func(u, s, name string) string {
		return sessionSuffix(u, s) + ":" + name
	}
	leadTools = append(leadTools, leadMailbox.Tools()...)

	// Generic specialist sub-agents — domain-agnostic by design. Specialise
	// them by adding tools/skills/MCP servers via config, not by hard-coding
	// a domain in their prompt. Examples of specialisation: drop a
	// `skills/k8s-triage/SKILL.md`, point an MCP server at `kubectl`, add a
	// permissions rule for `kubectl get`. The same binary then becomes a
	// Kubernetes diagnostician with no code change.
	investigator, err := agentkit.New(agentkit.AgentConfig{
		Name:        "investigator",
		Description: "Gathers evidence with read-only tools (file reads, log inspection, MCP queries) and reports findings.",
		Model:       investigatorLLM,
		Tools:       fstools.New(),
		Toolsets:    toolsets,
		Instruction: "You are an investigator. Use the available tools to collect concrete evidence before drawing any conclusion. Cite each finding with its source (file:line, command output, MCP resource id). Do not modify state.",
	})
	if err != nil {
		be.Close()
		return nil, err
	}
	summariser, err := agentkit.New(agentkit.AgentConfig{
		Name:        "summariser",
		Description: "Condenses long content into a structured brief.",
		Model:       summariserLLM,
		Instruction: "Reply with: (1) a one-sentence headline, (2) ≤ 7 bullets of the most important facts, (3) a short list of suggested next actions. No fluff.",
	})
	if err != nil {
		be.Close()
		return nil, err
	}
	leadTools = append(leadTools,
		agenttool.New(investigator, &agenttool.Config{}),
		agenttool.New(summariser, &agenttool.Config{}),
	)

	lead, err := agentkit.New(agentkit.AgentConfig{
		Name:        "lead",
		Description: "Generic coordinator agent. Specialise it by mounting domain-specific tools, skills, and MCP servers.",
		Model:       orchestratorLLM,
		Tools:       leadTools,
		Toolsets:    toolsets,
		SubAgents:   []adkagent.Agent{investigator, summariser},
		Instruction: `You are a generic Claude-Code-style coordinator. You are not bound to any single domain — what you can do is determined by the tools, skills and MCP servers currently mounted.

Operating method (always, regardless of the task):
  1. RESTATE the user's goal in one sentence and confirm scope before acting on anything irreversible.
  2. PLAN with task_create whenever the work has more than one step. Keep tasks small and verifiable.
  3. INVESTIGATE before you act: call the 'investigator' sub-agent (or read tools yourself) to gather evidence. Never rely on assumptions when a tool can confirm.
  4. ACT in small reversible steps. Prefer tools over shell, prefer dry-runs over mutations.
  5. SUMMARISE long outputs through the 'summariser' sub-agent before reasoning over them.
  6. RESPECT permissions: if a tool call is denied, do NOT retry — report and ask the user.
  7. ESCALATE to the user when ambiguity remains after one round of evidence gathering.

You have no built-in domain expertise. Lean on the mounted skills and tools to discover what is appropriate for the current environment.

Soft-skills: at the start of any non-trivial task, call 'list_softskills' once to scan curator-distilled procedures from past sessions, and 'load_softskill' the relevant one before planning. Treat soft-skills as hints, not authority — defer to authored skills, tool docs and the user when they disagree.`,
	})
	if err != nil {
		be.Close()
		return nil, err
	}

	// ── Plugins ──────────────────────────────────────────────────────────
	var plugins []*plugin.Plugin
	bus := events.NewBus()
	logger, closeLog, err := events.FileLogger(".agent_events.log")
	if err != nil {
		be.Close()
		return nil, err
	}
	// Note: closeLog should be called when shutting down
	_ = closeLog
	for _, ev := range []string{
		events.EventBeforeTool, events.EventAfterTool,
		events.EventBeforeModel, events.EventAfterModel,
		events.EventToolError, events.EventSessionStart, events.EventSessionEnd,
	} {
		bus.On(ev, logger)
	}
	if eb, err := bus.Plugin("events"); err == nil {
		plugins = append(plugins, eb)
	}
	if perms, err := permissions.NewPlugin("perms", runtime.PermissionsConfigPath, permissions.StdinAsker{}); err == nil {
		plugins = append(plugins, perms)
	}
	if _, cp, err := cache.Plugin("cache"); err == nil {
		plugins = append(plugins, cp)
	}
	if cmp, _, _, err := compress.PluginWithTools("compress", compress.Config{
		// Per-session audit file so concurrent users / sessions
		// never share a counter or overwrite each other's summaries.
		AuditPathFunc: func(userID, sessionID string) string {
			return fmt.Sprintf(".agent_memory_%s.md", sessionSuffix(userID, sessionID))
		},
		// Per-session State Log path — consumed by the curator agent
		// after EventSessionEnd to mine successful procedures.
		StateLogPathFunc: func(userID, sessionID string) string {
			return fmt.Sprintf(".agent_statelog_%s.json", sessionSuffix(userID, sessionID))
		},
		LLM: orchestratorLLM,
	}); err == nil {
		plugins = append(plugins, cmp)
		// NOTE: compact_now tool returned here is intentionally not mounted
		// on the lead in this entry-point; mount it explicitly when wiring
		// a custom agent (see examples/s06_compress for the pattern).
	}

	// Curator hook: after each session ends, fire-and-forget the curator
	// agent with the per-session audit + statelog paths. Best-effort —
	// process exit aborts. To run synchronously, use `agent-toolkit curate`.
	if runtime.CuratorEnabled {
		curatorLLM, err := modelForRole("curator")
		if err != nil {
			be.Close()
			return nil, err
		}
		registerCuratorHook(bus, curatorLLM, runtime.SoftSkillsDir, runtime.SkillsDir, sessionSuffix)
	}

	// Create AgentLoader from the agents
	loader, err := adkagent.NewMultiLoader(lead, investigator, summariser)
	if err != nil {
		be.Close()
		return nil, err
	}

	return &AgentResult{
		Agent:        lead,
		Investigator: investigator,
		Summariser:   summariser,
		Plugins:      plugins,
		EventBus:     bus,
		RunnerConfig: runner.Config{
			AppName:           runtime.AppName,
			Agent:             lead,
			SessionService:    session.InMemoryService(),
			AutoCreateSession: true,
			PluginConfig:      runner.PluginConfig{Plugins: plugins},
		},
		AgentLoader: loader,
	}, nil
}

// sanitizeID strips characters that are unsafe in a filename so user/session
// IDs can be embedded in per-session memory file paths without risk of path
// traversal or filesystem errors. Anything outside [A-Za-z0-9_.-] is replaced
// with '_'.
func sanitizeID(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// SessionSuffix returns the deterministic per-session filename suffix used
// across all components (tasks, todo, mailbox, audit, statelog). Exposed
// so external callers (notably the `curate` CLI) can reconstruct the
// per-session paths without duplicating the sanitizer.
func SessionSuffix(userID, sessionID string) string {
	u := sanitizeID(userID)
	s := sanitizeID(sessionID)
	if u == "" {
		u = "anon"
	}
	if s == "" {
		s = "default"
	}
	return u + "_" + s
}
