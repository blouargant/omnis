// Package agent provides a ready-to-use yoke agent that can be
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
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"gopkg.in/yaml.v3"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/core/llm"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/askuser"
	"github.com/blouargant/yoke/internal/bg"
	mcpcfg "github.com/blouargant/yoke/internal/mcp"
	"github.com/blouargant/yoke/internal/skills"
	"github.com/blouargant/yoke/internal/softskills"
	"github.com/blouargant/yoke/internal/tasks"
	"github.com/blouargant/yoke/internal/teammates"
	"github.com/blouargant/yoke/internal/todo"
	"github.com/blouargant/yoke/internal/worktree"
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
	// LeaderCachedInputTokenPricePerMillion is the effective leader cached
	// input token price (Anthropic cache_read / OpenAI prompt cache).
	LeaderCachedInputTokenPricePerMillion float64
	// LeaderCacheCreationTokenPricePerMillion is the effective leader cache
	// creation input token price (Anthropic cache_creation).
	LeaderCacheCreationTokenPricePerMillion float64
	// LeaderAllowFileAttachments controls whether the server embeds user-attached
	// files inline in LLM messages (true) or injects file paths as text (false).
	LeaderAllowFileAttachments bool
	// AskUserRegistry is the shared ask_user question/answer registry.
	// Surfaces (web server, TUI, console) use it to receive questions and
	// deliver answers. Call AskUserRegistry.SetNotify / SetCancel to attach
	// a surface after agent construction.
	AskUserRegistry *askuser.Registry
	// CuratorIdleTimeout is the idle-session duration after which the server
	// should trigger an automatic curation run. Zero means disabled.
	CuratorIdleTimeout time.Duration

	// RegisterSession registers a session's leader mailbox in the cross-session
	// registry under displayName. Call this when a new session is created so
	// other sessions can address it by name.
	// userID and sessionID must match those used by the ADK runner for this session.
	RegisterSession func(userID, sessionID, displayName string) error
	// RenameSession updates the cross-session registry when a session is renamed,
	// moving the mailbox address from oldName to newName.
	RenameSession func(oldName, newName string) error
	// UnregisterSession removes a session from the cross-session registry.
	// Call this when a session is deleted so it no longer appears in teammate_list.
	UnregisterSession func(displayName string) error
	// ListSessionRegistry returns a snapshot of the cross-session registry
	// as a display-name → mailbox-address map. Used by the server's GC to
	// detect entries whose underlying session no longer exists.
	ListSessionRegistry func() map[string]string
	// WatchMailbox starts a background goroutine that polls the leader mailbox
	// for (userID, sessionID). onMessage is called with (friendlyFromName, body)
	// whenever a message arrives; the message is consumed by the goroutine and
	// must be handled by the caller (it will NOT appear via teammate_check).
	// The goroutine exits when ctx is cancelled.
	WatchMailbox func(ctx context.Context, userID, sessionID string, onMessage func(from, body string))
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
	// AppName is the application name for the runner (default: "yoke").
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
	// DebugLogging enables full event payload logging, including model requests.
	DebugLogging bool
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
	if s := readEmbeddedInstruction(name); s != "" {
		return s
	}
	return readEmbeddedInstruction("default")
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
	agentSoftSkillsDir := cfg.SoftSkillsDir
	if agentSoftSkillsDir == "" && cfg.Name != "" {
		agentSoftSkillsDir = filepath.Join(runtime.SoftSkillsDir, cfg.Name)
	}
	if agentSoftSkillsDir != "" && agentSoftSkillsDir != runtime.SoftSkillsDir {
		if sts, err := softskills.Toolset(ctx, agentSoftSkillsDir); err == nil {
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
		case "calc":
			tools = append(tools, fstools.NewCalcTools()...)
		case "ddg":
			tools = append(tools, fstools.NewDDGTools()...)
		case "serpapi":
			tools = append(tools, fstools.NewSerpAPITools(runtime.SerpAPIKey)...)
		case "web":
			tools = append(tools, fstools.NewWebTools()...)
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
// YAML block delimited by `---` at the top of a SKILL.md file. Malformed
// front matter degrades gracefully to empty values rather than panicking.
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

	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(header), &fm); err != nil {
		return "", ""
	}
	return fm.Name, fm.Description
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
	sb.WriteString("The following sub-agents are mounted as tools. Invoke them by name — they return their findings to you automatically. Call at most one sub-agent at a time; wait for its findings before deciding whether another sub-agent call is needed. When a sub-agent owns a skill that matches the user's request, delegate to that sub-agent and explicitly tell it which skill to load (e.g. \"use the k8s-triage skill\"). Never use transfer_to_agent — it permanently hands off control.\n\n")

	for _, cfg := range enabled {
		desc := cfg.Description
		if desc == "" {
			desc = defaultAgentDescription(cfg.Name)
		}

		sb.WriteString(fmt.Sprintf("**%s**: %s\n", cfg.Name, desc))
		if guidance := defaultSubAgentUsageGuidance(cfg.Name); guidance != "" {
			sb.WriteString(fmt.Sprintf("  - Use: %s\n", guidance))
		}

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

func defaultSubAgentUsageGuidance(name string) string {
	switch name {
	case "investigator":
		return "Delegate focused evidence questions here; expect compact cited findings with sources, confidence, and open questions. Do not routinely send these reports to summariser unless they are oversized or poorly structured."
	case "summariser":
		return "Send oversized raw output, verbose reports, or user-requested briefs here; expect a lossy structured brief that preserves source anchors when present."
	default:
		return ""
	}
}

func hasTool(tools []string, key string) bool {
	for _, t := range tools {
		if t == key {
			return true
		}
	}
	return false
}

// NewAgent creates a fully configured yoke agent that can be used
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
	if err := fstools.ConfigureBashOutputFilter(fstools.BashOutputFilterConfig{
		Enabled:    runtime.BashOutputFilterEnabled,
		FiltersDir: runtime.BashOutputFiltersDir,
	}); err != nil {
		return nil, fmt.Errorf("bootstrap bash output filter: %w", err)
	}
	fstools.SetBashDefaultTimeout(time.Duration(runtime.BashTimeoutSeconds) * time.Second)
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
	//
	// When a sessionID is provided (web sessions use petnames such as
	// "happy-panda") it is used directly so each session gets a stable,
	// human-readable suffix that survives server restarts.  For sessions
	// without an ID (CLI / console mode) we fall back to the build
	// timestamp to preserve the existing isolation behaviour.
	buildTimestamp := time.Now().Format("20060102_150405")
	sessionSuffix := func(userID, sessionID string) string {
		u := sanitizeID(userID)
		if u == "" {
			u = "anon"
		}
		if s := sanitizeID(sessionID); s != "" {
			return u + "_" + s
		}
		return u + "_" + buildTimestamp
	}

	// Per-session task graph (logs/agent_tasks_<u>_<s>.json).
	g := tasks.NewSessionScoped("", func(u, s string) string {
		return filepath.Join("logs", fmt.Sprintf("agent_tasks_%s.json", sessionSuffix(u, s)))
	})
	// Per-session background notification queue.
	q := bg.NewSessionQueues(32)
	// Per-session todo plan (logs/agent_todo_<u>_<s>.json).
	store := todo.NewSessionScoped("", func(u, s string) string {
		return filepath.Join("logs", fmt.Sprintf("agent_todo_%s.json", sessionSuffix(u, s)))
	})

	leadTools := []tool.Tool{}
	leadTools = append(leadTools, fstools.New()...)
	leadTools = append(leadTools, fstools.NewCalcTools()...)
	leadTools = append(leadTools, store.Tools()...)
	leadTools = append(leadTools, g.Tools()...)
	leadTools = append(leadTools, worktree.Tools(opts.Repo)...)
	leadTools = append(leadTools, q.Tool())
	leadTools = append(leadTools, curateSessionTool())

	// ask_user registry — created before the event bus so the notify/cancel
	// callbacks can reference the bus once it is created below.
	askUserReg := askuser.NewRegistry()

	skillTS, softSkillTS, mcpToolsets, toolsets := buildLeaderToolsets(ctx, runtime, leaderCfg)

	be, err := teammates.ChooseBackend()
	if err != nil {
		return nil, fmt.Errorf("mailbox backend: %w", err)
	}
	nameFunc := func(u, s, name string) string {
		return sessionSuffix(u, s) + ":" + name
	}

	// Cross-session registry: maps session display names (petnames or
	// user-assigned titles) to their leader mailbox addresses so that a
	// leader in one session can address the leader in another by name.
	reg := teammates.NewSessionRegistry(".mailboxes")

	leadMailbox := teammates.NewAgent("leader", be)
	// Namespace mailbox names per session so two concurrent sessions
	// running an agent named "leader" never share an inbox.
	leadMailbox.NameFunc = nameFunc
	// Attach the cross-session registry only to the leader so that
	// intra-session sub-agents (investigator, summariser, …) are not
	// accidentally exposed as cross-session targets.
	leadMailbox.Registry = reg
	leadTools = append(leadTools, leadMailbox.Tools()...)

	// Event bus — created here (rather than later, with the rest of the
	// plugins) so its per-agent callbacks can be attached directly to each
	// sub-agent. Sub-agents are wrapped via agenttool, which spawns its
	// own internal runner that does NOT inherit the toolkit's runner-level
	// plugins; without these per-agent callbacks the sub-agents' tool and
	// model activity would never reach the bus (and thus the TUI / logs).
	bus := events.NewBus()

	// Wire ask_user registry notifications through the event bus so server
	// and TUI surfaces receive questions and cancellations as bus events.
	askUserReg.SetNotify(func(q askuser.Question) {
		bus.Emit(events.EventAskUser, askuser.QuestionToPayload(q))
	})
	askUserReg.SetCancel(func(q askuser.Question) {
		bus.Emit(events.EventAskUserCancel, map[string]any{
			"question_id": q.ID,
			"session_id":  q.SessionID,
		})
	})
	leadTools = append(leadTools, fstools.NewAskUserTool(askUserReg))

	subAgentCallbacks := bus.AgentCallbacks(events.PluginOptions{IncludeModelRequest: opts.DebugLogging})

	subAgentMap, subAgents, subAgentLeaderTools, err := buildSubAgents(
		ctx, runtime, be, nameFunc,
		skillTS, softSkillTS, mcpToolsets,
		modelForAgent, subAgentCallbacks,
	)
	if err != nil {
		be.Close()
		return nil, err
	}
	leadTools = append(leadTools, subAgentLeaderTools...)

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
		// SubAgents intentionally omitted: passing sub-agents here causes ADK to
		// inject a transfer_to_agent function that permanently transfers control
		// (no automatic return). Sub-agents are reached via their agenttool
		// wrappers already in leadTools, which always return control to the leader.
		Instruction: leaderInstruction,
	})
	if err != nil {
		be.Close()
		return nil, err
	}

	// ── Plugins ──────────────────────────────────────────────────────────
	// `bus` was created earlier so per-agent callbacks could be attached to
	// sub-agents at construction time. buildPlugins wires it as a runner-
	// level plugin so the leader's tool/model activity, plus run start/end,
	// also flow through it.
	plugins, err := buildPlugins(runtime, opts, bus, orchestratorLLM, sessionSuffix, buildTimestamp)
	if err != nil {
		be.Close()
		return nil, err
	}

	// Curator hook: after each session ends, fire-and-forget the curator
	// agent with the per-session audit + statelog paths. Best-effort —
	// process exit aborts. To run synchronously, use `yoke curate`.
	if curatorCfg, ok := runtime.AgentConfig("curator"); ok && curatorCfg.Enabled {
		curatorLLM, err := modelForAgent(curatorCfg)
		if err == nil {
			var subAgentNames []string
			for _, cfg := range runtime.Agents {
				if cfg.Name == "leader" || cfg.Name == "curator" || !cfg.Enabled {
					continue
				}
				subAgentNames = append(subAgentNames, cfg.Name)
			}
			gate := CuratorGateConfig{
				MinTurns:         runtime.CuratorMinTurns,
				MinSubAgentCalls: runtime.CuratorMinSubAgentCalls,
			}
			registerCuratorHook(bus, curatorLLM, runtime.SoftSkillsDir, runtime.SkillsDir, subAgentNames, gate, sessionSuffix)
		}
	}

	// Create AgentLoader from the agents
	loader, err := adkagent.NewMultiLoader(lead, subAgents...)
	if err != nil {
		be.Close()
		return nil, err
	}

	return &AgentResult{
		Agent:                                   lead,
		SubAgents:                               subAgentMap,
		Plugins:                                 plugins,
		EventBus:                                bus,
		AskUserRegistry:                         askUserReg,
		LeaderInputTokenPricePerMillion:         leaderCfg.InputTokenPricePerMillion,
		LeaderOutputTokenPricePerMillion:        leaderCfg.OutputTokenPricePerMillion,
		LeaderCachedInputTokenPricePerMillion:   leaderCfg.CachedInputTokenPricePerMillion,
		LeaderCacheCreationTokenPricePerMillion: leaderCfg.CacheCreationTokenPricePerMillion,
		LeaderAllowFileAttachments:              leaderCfg.AllowFileAttachments,
		CuratorIdleTimeout:                      runtime.CuratorIdleTimeout,
		RunnerConfig: runner.Config{
			AppName:           runtime.AppName,
			Agent:             lead,
			SessionService:    session.InMemoryService(),
			AutoCreateSession: true,
			PluginConfig:      runner.PluginConfig{Plugins: plugins},
		},
		AgentLoader: loader,
		RegisterSession: func(userID, sessionID, displayName string) error {
			addr := nameFunc(userID, sessionID, "leader")
			return reg.Register(displayName, addr)
		},
		RenameSession: func(oldName, newName string) error {
			return reg.Rename(oldName, newName)
		},
		UnregisterSession: func(displayName string) error {
			return reg.Unregister(displayName)
		},
		ListSessionRegistry: func() map[string]string {
			return reg.List()
		},
		WatchMailbox: func(ctx context.Context, userID, sessionID string, onMessage func(from, body string)) {
			addr := nameFunc(userID, sessionID, "leader")
			go func() {
				for {
					m, err := be.Receive(ctx, addr, 2*time.Second)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						continue
					}
					if m == nil {
						continue
					}
					// Resolve the sender's mailbox address to a friendly session
					// name via registry reverse-lookup; fall back to the raw address.
					from := m.From
					for name, maddr := range reg.List() {
						if maddr == m.From {
							from = name
							break
						}
					}
					onMessage(from, m.Body)
				}
			}()
		},
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
