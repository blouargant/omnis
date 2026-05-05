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
	"path/filepath"
	"strings"

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
	// SubAgents are all mounted sub-agents keyed by name.
	SubAgents map[string]adkagent.Agent
	// AgentLoader is the ADK agent loader for the launcher.
	AgentLoader adkagent.Loader
	// RunnerConfig is the ADK runner configuration for this agent.
	RunnerConfig runner.Config
	// Plugins are the plugins wired to this agent.
	Plugins []*plugin.Plugin
	// EventBus is the event bus for this agent.
	EventBus *events.Bus
	// LeaderInputTokenPricePerMillion is the effective leader input token price.
	LeaderInputTokenPricePerMillion float64
	// LeaderOutputTokenPricePerMillion is the effective leader output token price.
	LeaderOutputTokenPricePerMillion float64
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
	// CuratorEnabled explicitly enables/disables curator auto-run.
	CuratorEnabled *bool
}

func selectionFromAgentConfig(cfg RuntimeAgentConfig) llm.Selection {
	return llm.Selection{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		BaseURL:  cfg.BaseURL,
		APIKey:   cfg.APIKey,
	}
}

func defaultAgentDescription(name string) string {
	switch name {
	case "leader":
		return "Generic coordinator agent. Specialise it by mounting domain-specific tools, skills, and MCP servers."
	case "investigator":
		return "Gathers evidence with read-only tools (file reads, log inspection, MCP queries) and reports findings."
	case "summariser":
		return "Condenses long content into a structured brief."
	case "curator":
		return "Post-session soft-skill curator."
	default:
		return "Specialist helper agent."
	}
}

func defaultAgentInstruction(name string) string {
	switch name {
	case "leader":
		return `You are a generic Claude-Code-style coordinator. You are not bound to any single domain — what you can do is determined by the tools, skills and MCP servers currently mounted.

Operating method (always, regardless of the task):
  1. RESTATE the user's goal in one sentence and confirm scope before acting on anything irreversible.
  2. DISCOVER SKILLS FIRST: call 'list_skills' at the very start of every non-trivial task to see authored procedures available to YOU. Also consult the "Available Sub-Agents" section below — each sub-agent that owns the 'skills' tool group lists its own skill catalog there. Skills are authoritative — they override your default behaviour.
       • If a skill in YOUR catalog matches the request, call 'load_skill' and follow it.
       • If a skill in a SUB-AGENT'S catalog matches the request, delegate to that sub-agent and explicitly tell it which skill to load (e.g. "use the k8s-triage skill to answer this").
  3. PLAN with task_create whenever the work has more than one step. Keep tasks small and verifiable.
  4. INVESTIGATE before you act: gather evidence using your own read-only tools, MCP servers, or by delegating to the 'investigator' sub-agent. Never rely on assumptions when a tool can confirm.
  5. ACT in small reversible steps. Prefer tools over shell, prefer dry-runs over mutations.
  6. SUMMARISE long outputs through the 'summariser' sub-agent before reasoning over them.
  7. RESPECT permissions: if a tool call is denied, do NOT retry — report and ask the user.
  8. ESCALATE to the user when ambiguity remains after one round of evidence gathering.

You have no built-in domain expertise. Lean on the mounted skills and tools to discover what is appropriate for the current environment.

Soft-skills: after step 2 (skills discovery), also call 'list_softskills' once to scan curator-distilled procedures from past sessions, and 'load_softskill' the relevant one before planning. Treat soft-skills as hints, not authority — defer to authored skills, tool docs and the user when they disagree.

IMPORTANT — loader pairing (do not mix):
  • Names returned by 'list_skills'      MUST be loaded with 'load_skill'.
  • Names returned by 'list_softskills'  MUST be loaded with 'load_softskill'.
  Calling 'load_skill' with a soft-skill name (or vice-versa) will fail with "skill not found" because the two loaders read different directories (skills/ vs softskills/).`
	case "investigator":
		return `You are an investigator.

Operating method (always):
  1. Start each non-trivial request by calling 'list_skills'. If a matching skill exists, call 'load_skill' and follow it exactly.
  2. Call 'list_softskills' once per task and load a relevant soft-skill via 'load_softskill' when useful.
  3. Use the available read-only tools to collect concrete evidence before drawing any conclusion.
  4. Cite each finding with its source (file:line, command output, MCP resource id).
  5. Do not modify state.

Loader pairing: 'list_skills' → 'load_skill' (skills/ directory); 'list_softskills' → 'load_softskill' (softskills/ directory). The two loaders are not interchangeable — using the wrong one fails with "skill not found".`
	case "summariser":
		return "Reply with: (1) a one-sentence headline, (2) <= 7 bullets of the most important facts, (3) a short list of suggested next actions. No fluff."
	default:
		return "You are a specialist helper. Follow your instruction and use your tools to assist the leader agent."
	}
}

func defaultToolKeys(name string) []string {
	switch name {
	case "investigator":
		return []string{"fs", "mcp"}
	case "summariser":
		return []string{}
	default:
		return []string{}
	}
}

func toolsForAgentConfig(ctx context.Context, cfg RuntimeAgentConfig, runtime RuntimeSettings, skillTS, softSkillTS tool.Toolset, mcpToolsets []tool.Toolset) ([]tool.Tool, []tool.Toolset) {
	keys := cfg.Tools
	if keys == nil {
		keys = defaultToolKeys(cfg.Name)
	}

	// Per-agent overrides: if the agent specifies its own skills_dir /
	// softskills_dir / mcp_config_path, build a dedicated toolset from it
	// instead of reusing the leader-level one.
	resolvedSkillTS := skillTS
	if cfg.SkillsDir != "" && cfg.SkillsDir != runtime.SkillsDir {
		if ts, err := skills.Toolset(ctx, cfg.SkillsDir); err == nil {
			resolvedSkillTS = ts
		}
	}
	resolvedSoftSkillTS := softSkillTS
	if cfg.SoftSkillsDir != "" && cfg.SoftSkillsDir != runtime.SoftSkillsDir {
		if sts, err := softskills.Toolset(ctx, cfg.SoftSkillsDir); err == nil {
			resolvedSoftSkillTS = sts
		}
	}
	resolvedMCPToolsets := mcpToolsets
	if cfg.MCPConfigPath != "" && cfg.MCPConfigPath != runtime.MCPConfigPath {
		if mc, err := mcpcfg.Load(cfg.MCPConfigPath); err == nil {
			if mts, err := mc.Toolsets(); err == nil {
				resolvedMCPToolsets = mts
			}
		}
	}

	tools := []tool.Tool{}
	toolsets := []tool.Toolset{}
	for _, key := range keys {
		switch key {
		case "fs":
			tools = append(tools, fstools.New()...)
		case "mcp":
			toolsets = append(toolsets, resolvedMCPToolsets...)
		case "skills":
			if resolvedSkillTS != nil {
				toolsets = append(toolsets, resolvedSkillTS)
			}
		case "softskills":
			if resolvedSoftSkillTS != nil {
				toolsets = append(toolsets, resolvedSoftSkillTS)
			}
		}
	}
	return tools, toolsets
}

// skillCatalogEntry is one skill discovered on disk for documentation
// purposes (front-matter only — body is never read here).
type skillCatalogEntry struct {
	Name        string
	Description string
}

// scanSkillCatalog reads `<dir>/<name>/SKILL.md` front matter for each
// subdirectory and returns a list of {name, description}. It is best-effort:
// any unreadable / malformed file is skipped silently. It returns nil when
// the directory does not exist.
func scanSkillCatalog(dir string) []skillCatalogEntry {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []skillCatalogEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontMatter(b)
		if name == "" {
			name = e.Name()
		}
		out = append(out, skillCatalogEntry{Name: name, Description: desc})
	}
	return out
}

// parseSkillFrontMatter extracts the `name` and `description` keys from the
// YAML block delimited by `---` at the top of a SKILL.md file. It uses a
// minimal line-based parser to avoid pulling YAML for one purpose.
func parseSkillFrontMatter(b []byte) (name, description string) {
	s := string(b)
	if !strings.HasPrefix(s, "---") {
		return "", ""
	}
	rest := strings.TrimPrefix(s, "---")
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	header := rest[:end]
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimRight(line, "\r")
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			val = strings.Trim(val, `"'`)
			switch key {
			case "name":
				name = val
			case "description":
				description = val
			}
		}
	}
	return name, description
}

// buildSubAgentCapabilitiesBlock generates a structured block of sub-agent
// information for the leader's instruction. It lists all enabled sub-agents
// (excluding leader and curator) with their descriptions, tools, skill
// catalogs (when the agent has the `skills` tool group), and how to invoke them.
func buildSubAgentCapabilitiesBlock(runtimeAgents []RuntimeAgentConfig, runtime RuntimeSettings) string {
	var enabled []RuntimeAgentConfig
	for _, cfg := range runtimeAgents {
		if cfg.Name == "leader" || !cfg.Enabled || cfg.Name == "curator" {
			continue
		}
		enabled = append(enabled, cfg)
	}

	if len(enabled) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n# Available Sub-Agents\n\n")
	sb.WriteString("The following sub-agents are mounted as tools and can be invoked when delegation is appropriate. Each sub-agent only sees the tools listed under it. When a sub-agent owns a skill that matches the user's request, delegate to that sub-agent and explicitly tell it which skill to load (e.g. \"use the k8s-triage skill\").\n\n")

	for _, cfg := range enabled {
		desc := cfg.Description
		if desc == "" {
			desc = defaultAgentDescription(cfg.Name)
		}

		sb.WriteString(fmt.Sprintf("**%s**: %s\n", cfg.Name, desc))

		if len(cfg.Tools) > 0 {
			sb.WriteString(fmt.Sprintf("  - Tools: %s\n", strings.Join(cfg.Tools, ", ")))
		}

		// Surface the skill catalog when this sub-agent has access to the
		// `skills` tool group, so the leader can route by skill ownership.
		if hasTool(cfg.Tools, "skills") {
			skillsDir := cfg.SkillsDir
			if skillsDir == "" {
				skillsDir = runtime.SkillsDir
			}
			catalog := scanSkillCatalog(skillsDir)
			if len(catalog) > 0 {
				sb.WriteString("  - Skills available to this agent:\n")
				for _, sk := range catalog {
					if sk.Description != "" {
						sb.WriteString(fmt.Sprintf("      • %s — %s\n", sk.Name, sk.Description))
					} else {
						sb.WriteString(fmt.Sprintf("      • %s\n", sk.Name))
					}
				}
			}
		}

		if cfg.Mailbox {
			sb.WriteString("  - Messaging: This agent has a mailbox and can receive messages via teammate_ask/tell\n")
		} else {
			sb.WriteString("  - Messaging: This agent does not have a mailbox (one-way delegation only)\n")
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

func hasTool(tools []string, key string) bool {
	for _, t := range tools {
		if t == key {
			return true
		}
	}
	return false
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
	leaderCfg, ok := runtime.LeaderConfig()
	if !ok {
		return nil, fmt.Errorf("runtime config: missing mandatory leader agent")
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

	modelForAgent := func(cfg RuntimeAgentConfig) (model.LLM, error) {
		m, err := llm.NewWithSelection(ctx, selectionFromAgentConfig(cfg))
		if err != nil {
			return nil, fmt.Errorf("model agent %q: %w", cfg.Name, err)
		}
		return m, nil
	}

	orchestratorLLM, err := modelForAgent(leaderCfg)
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

	// Effective directories for the leader's own toolsets: per-agent leader
	// overrides take precedence over the global runtime defaults.
	leaderSkillsDir := runtime.SkillsDir
	if leaderCfg.SkillsDir != "" {
		leaderSkillsDir = leaderCfg.SkillsDir
	}
	leaderSoftSkillsDir := runtime.SoftSkillsDir
	if leaderCfg.SoftSkillsDir != "" {
		leaderSoftSkillsDir = leaderCfg.SoftSkillsDir
	}
	leaderMCPConfigPath := runtime.MCPConfigPath
	if leaderCfg.MCPConfigPath != "" {
		leaderMCPConfigPath = leaderCfg.MCPConfigPath
	}

	var toolsets []tool.Toolset
	var skillTS tool.Toolset
	if ts, err := skills.Toolset(ctx, leaderSkillsDir); err == nil {
		skillTS = ts
		toolsets = append(toolsets, ts)
	}
	var softSkillTS tool.Toolset
	if sts, err := softskills.Toolset(ctx, leaderSoftSkillsDir); err == nil {
		softSkillTS = sts
		toolsets = append(toolsets, sts)
	}
	var mcpToolsets []tool.Toolset
	if mc, err := mcpcfg.Load(leaderMCPConfigPath); err == nil {
		if mts, err := mc.Toolsets(); err == nil {
			mcpToolsets = append(mcpToolsets, mts...)
			toolsets = append(toolsets, mts...)
		}
	}

	be, err := teammates.ChooseBackend()
	if err != nil {
		return nil, fmt.Errorf("mailbox backend: %w", err)
	}
	nameFunc := func(u, s, name string) string {
		return sessionSuffix(u, s) + ":" + name
	}

	leadMailbox := teammates.NewAgent("leader", be)
	// Namespace mailbox names per session so two concurrent sessions
	// running an agent named "leader" never share an inbox.
	leadMailbox.NameFunc = nameFunc
	leadTools = append(leadTools, leadMailbox.Tools()...)

	subAgents := []adkagent.Agent{}
	subAgentMap := map[string]adkagent.Agent{}
	seenNames := map[string]bool{"leader": true}

	for _, cfg := range runtime.Agents {
		if cfg.Name == "leader" || !cfg.Enabled {
			continue
		}
		if cfg.Name == "curator" {
			continue
		}
		if seenNames[cfg.Name] {
			continue
		}
		seenNames[cfg.Name] = true

		agentLLM, err := modelForAgent(cfg)
		if err != nil {
			be.Close()
			return nil, err
		}

		desc := cfg.Description
		if desc == "" {
			desc = defaultAgentDescription(cfg.Name)
		}
		instr := cfg.Instruction
		if instr == "" {
			instr = defaultAgentInstruction(cfg.Name)
		}

		subTools, subToolsets := toolsForAgentConfig(ctx, cfg, runtime, skillTS, softSkillTS, mcpToolsets)
		if cfg.Mailbox {
			mb := teammates.NewAgent(cfg.Name, be)
			mb.NameFunc = nameFunc
			subTools = append(subTools, mb.Tools()...)
		}

		sa, err := agentkit.New(agentkit.AgentConfig{
			Name:        cfg.Name,
			Description: desc,
			Instruction: instr,
			Model:       agentLLM,
			Tools:       subTools,
			Toolsets:    subToolsets,
		})
		if err != nil {
			be.Close()
			return nil, err
		}

		subAgents = append(subAgents, sa)
		subAgentMap[cfg.Name] = sa
		leadTools = append(leadTools, agenttool.New(sa, &agenttool.Config{}))
	}

	leaderDescription := leaderCfg.Description
	if leaderDescription == "" {
		leaderDescription = defaultAgentDescription("leader")
	}
	leaderInstruction := leaderCfg.Instruction
	if leaderInstruction == "" {
		leaderInstruction = defaultAgentInstruction("leader")
	}

	// Append dynamic sub-agent capabilities to the leader instruction
	leaderInstruction += buildSubAgentCapabilitiesBlock(runtime.Agents, runtime)

	lead, err := agentkit.New(agentkit.AgentConfig{
		Name:        "leader",
		Description: leaderDescription,
		Model:       orchestratorLLM,
		Tools:       leadTools,
		Toolsets:    toolsets,
		SubAgents:   subAgents,
		Instruction: leaderInstruction,
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
		events.EventToolError,
		events.EventSessionStart, events.EventSessionEnd,
		events.EventRunStart, events.EventRunEnd,
		events.EventCurateNow,
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
	if curatorCfg, ok := runtime.AgentConfig("curator"); ok && curatorCfg.Enabled {
		curatorLLM, err := modelForAgent(curatorCfg)
		if err == nil {
			registerCuratorHook(bus, curatorLLM, runtime.SoftSkillsDir, runtime.SkillsDir, sessionSuffix)
		}
	}

	// Create AgentLoader from the agents
	loader, err := adkagent.NewMultiLoader(lead, subAgents...)
	if err != nil {
		be.Close()
		return nil, err
	}

	return &AgentResult{
		Agent:     lead,
		SubAgents: subAgentMap,
		Plugins:   plugins,
		EventBus:  bus,
		LeaderInputTokenPricePerMillion:  leaderCfg.InputTokenPricePerMillion,
		LeaderOutputTokenPricePerMillion: leaderCfg.OutputTokenPricePerMillion,
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
