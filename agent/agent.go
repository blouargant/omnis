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
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
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
	// LeaderCachedInputTokenPricePerMillion is the effective leader cached
	// input token price (Anthropic cache_read / OpenAI prompt cache).
	LeaderCachedInputTokenPricePerMillion float64
	// LeaderCacheCreationTokenPricePerMillion is the effective leader cache
	// creation input token price (Anthropic cache_creation).
	LeaderCacheCreationTokenPricePerMillion float64

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
	switch name {
	case "leader":
		return `You are a generic Claude-Code-style coordinator. You are not bound to any single domain — what you can do is determined by the tools, skills and MCP servers currently mounted.

Operating method (always, regardless of the task):
  0. CHECK MAILBOX: call 'teammate_check' at two points every turn:
       • At the very START, before anything else. If a message is waiting, acknowledge the sender (use 'teammate_tell' to reply if a reply is warranted) and then continue to the user's task.
       • Just BEFORE writing your final response, after all task work is done. This catches messages that arrived while you were working. Handle them the same way, then include a note in your response that a cross-session message was received and acted on.
  1. RESTATE the user's goal in one sentence and confirm scope before acting on anything irreversible.
  2. DISCOVER SKILLS FIRST: call 'list_skills' at the very start of every non-trivial task to see authored procedures available to YOU. Also consult the "Available Sub-Agents" section below — each sub-agent that owns the 'skills' tool group lists its own skill catalog there. Skills are authoritative — they override your default behaviour.
       • If a skill in YOUR catalog matches the request, call 'load_skill' and follow it.
       • If a skill in a SUB-AGENT'S catalog matches the request, delegate to that sub-agent and explicitly tell it which skill to load (e.g. "use the k8s-triage skill to answer this").
  3. PLAN with task_create whenever the work has more than one step. Keep tasks small and verifiable.
  4. INVESTIGATE before you act: gather evidence using your own read-only tools, MCP servers, or by delegating focused evidence questions to the 'investigator' sub-agent. Ask the investigator for compact cited findings: facts, sources, confidence, and open questions. Never rely on assumptions when a tool can confirm.
  5. DELEGATE BY DEFAULT — your primary job is to coordinate, not to execute. Whenever a sub-agent listed under "Available Sub-Agents" is a plausible fit for a step, delegate it instead of running the work yourself (no bash, no file edits, no direct MCP calls). Give the sub-agent a precise objective, the exact skill or soft-skill to load, and any constraints.
       • If a sub-agent comes back with a failure, an empty result, or a wrong answer, DO NOT immediately take over. Re-task it: diagnose what likely went wrong, invent a sharper instruction (different angle, narrower scope, alternative tool, missing context, explicit skill to load, concrete commands to try) and send it back. Iterate at least 2-3 times with genuinely different guidance before considering the sub-agent unable to perform the task.
       • Only act directly (run bash, edit files, call MCP tools yourself) when (a) no mounted sub-agent is suitable for the step, or (b) suitable sub-agents have repeatedly failed despite refined instructions. State briefly which condition applies before doing so.
  6. ACT in small reversible steps when you must act yourself. Prefer tools over shell, prefer dry-runs over mutations.
  7. CONTROL BULK before reasoning: use the 'summariser' sub-agent for oversized raw tool output, verbose investigator reports, or user-requested briefs. As a rule of thumb, summarise material over roughly 150-250 lines or 2k-4k tokens, but do not summarise concise investigator evidence briefs unless they are too large or poorly structured.
  8. RESPECT permissions: if a tool call is denied, do NOT retry — report and ask the user.
  9. ESCALATE to the user when ambiguity remains after one round of evidence gathering.

You have no built-in domain expertise. Lean on the mounted skills and tools to discover what is appropriate for the current environment.

Soft-skills: after step 2 (skills discovery), also call 'list_softskills' once to scan curator-distilled procedures from past sessions, and 'load_softskill' the relevant one before planning. Treat soft-skills as hints, not authority — defer to authored skills, tool docs and the user when they disagree.

Cross-session communication: use 'teammate_list' to discover other active sessions, 'teammate_ask' to query a peer and wait for its reply, and 'teammate_tell' to send a one-way notification. When asked to notify another session once a task is complete, send the message with 'teammate_tell' immediately after the task finishes — do not wait for the user to prompt you again.

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
  4. Return a compact evidence brief, not a raw dump. Include findings, exact sources (file:line, command output, MCP resource id), confidence, and open questions.
  5. Quote only decisive excerpts. Include bulk output only when it is essential to the user's question.
  6. Do not modify state.
  7. If information is missing (e.g. pod name, namespace, time window), list it under "open questions" in your brief — do NOT use teammate_ask or any mailbox tool to request it. The leader will relay unanswered questions to the user.

Loader pairing: 'list_skills' → 'load_skill' (skills/ directory); 'list_softskills' → 'load_softskill' (softskills/ directory). The two loaders are not interchangeable — using the wrong one fails with "skill not found".`
	case "summariser":
		return "Reply with: (1) a one-sentence headline, (2) <= 7 bullets of the most important facts, (3) a short list of suggested next actions. Preserve source anchors when present: file paths, line numbers, command names, exact error messages, resource ids, and uncertainty markers. Distinguish facts from guesses. No fluff."
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
	subAgentCallbacks := bus.AgentCallbacks(events.PluginOptions{IncludeModelRequest: opts.DebugLogging})

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
			Name:                 cfg.Name,
			Description:          desc,
			Instruction:          instr,
			Model:                agentLLM,
			Tools:                subTools,
			Toolsets:             subToolsets,
			BeforeToolCallbacks:  []llmagent.BeforeToolCallback{subAgentCallbacks.BeforeTool},
			AfterToolCallbacks:   []llmagent.AfterToolCallback{subAgentCallbacks.AfterTool},
			OnToolErrorCallbacks: []llmagent.OnToolErrorCallback{subAgentCallbacks.OnToolError},
			BeforeModelCallbacks: []llmagent.BeforeModelCallback{subAgentCallbacks.BeforeModel},
			AfterModelCallbacks:  []llmagent.AfterModelCallback{subAgentCallbacks.AfterModel},
		})
		if err != nil {
			be.Close()
			return nil, err
		}

		subAgents = append(subAgents, sa)
		subAgentMap[cfg.Name] = sa
		wrappedSubAgent, ok := agenttool.New(sa, &agenttool.Config{}).(runnableTool)
		if !ok {
			be.Close()
			return nil, fmt.Errorf("agenttool for %q is not runnable", cfg.Name)
		}
		leadTools = append(leadTools, newNonConcurrentTool(wrappedSubAgent))
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
	// sub-agents at construction time. Here we wire it as a runner-level
	// plugin so the leader's tool/model activity, plus run start/end, also
	// flow through it.
	var plugins []*plugin.Plugin
	if err := os.MkdirAll("logs", 0o755); err != nil {
		be.Close()
		return nil, err
	}
	logger, closeLog, err := events.FileLoggerWithOptions(
		filepath.Join("logs", "agent_events_"+buildTimestamp+".log"),
		events.FileLoggerOptions{FullPayload: opts.DebugLogging},
	)
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
	eventsPlugin, err := bus.PluginWithOptions("events", events.PluginOptions{IncludeModelRequest: opts.DebugLogging})
	if err == nil {
		eb := eventsPlugin
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
			return filepath.Join("logs", fmt.Sprintf("agent_memory_%s.md", sessionSuffix(userID, sessionID)))
		},
		// Per-session State Log path — consumed by the curator agent
		// after EventSessionEnd to mine successful procedures.
		StateLogPathFunc: func(userID, sessionID string) string {
			return filepath.Join("logs", fmt.Sprintf("agent_statelog_%s.json", sessionSuffix(userID, sessionID)))
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
		Agent:                                   lead,
		SubAgents:                               subAgentMap,
		Plugins:                                 plugins,
		EventBus:                                bus,
		LeaderInputTokenPricePerMillion:         leaderCfg.InputTokenPricePerMillion,
		LeaderOutputTokenPricePerMillion:        leaderCfg.OutputTokenPricePerMillion,
		LeaderCachedInputTokenPricePerMillion:   leaderCfg.CachedInputTokenPricePerMillion,
		LeaderCacheCreationTokenPricePerMillion: leaderCfg.CacheCreationTokenPricePerMillion,
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
