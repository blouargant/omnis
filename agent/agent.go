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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool"
	"gopkg.in/yaml.v3"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/core/llm"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/a2a"
	"github.com/blouargant/yoke/internal/askuser"
	"github.com/blouargant/yoke/internal/codeindex"
	mcpcfg "github.com/blouargant/yoke/internal/mcp"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
	"github.com/blouargant/yoke/internal/skills"
	"github.com/blouargant/yoke/internal/softskills"
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
	// SoftSkillsDir is the directory to load curator-generated soft-skills
	// from (default: "softskills"). Created if missing.
	SoftSkillsDir string
	// DisableAutoCurate disables the EventSessionEnd hook that fires the
	// curator agent in the background. The manual `curate` CLI subcommand
	// remains available regardless.
	DisableAutoCurate bool
	// Repo is the repository root for worktree tools (default: current working directory).
	Repo string
	// MCPSConfigPath is the path to the MCP config file (default: resolved from .agents/mcp_config.json or $HOME/.yoke/mcp_config.json).
	MCPSConfigPath string
	// PermissionsConfigPath is the path to the permissions config (default: resolved from .agents/permissions.json or $HOME/.yoke/permissions.json).
	PermissionsConfigPath string
	// AppName is the application name for the runner (default: "yoke").
	AppName string
	// ConfigPath is the runtime JSON configuration path (default: resolved from .agents/agents.json or $HOME/.yoke/agents.json).
	ConfigPath string
	// ConfigPathStrict returns an error when ConfigPath does not exist.
	ConfigPathStrict bool
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
	if s := ReadAgentInstruction(name); s != "" {
		return s
	}
	return ReadAgentInstruction("default")
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

func toolsForAgentConfig(ctx context.Context, cfg RuntimeAgentConfig, runtime RuntimeSettings, skillTS, softSkillTS tool.Toolset, leaderMCPHandles []*mcpcfg.Handle, pool *mcpcfg.Pool, codeIdx *codeindex.Index) ([]tool.Tool, []tool.Toolset, string, []*mcpcfg.Handle) {
	keys := cfg.Tools
	if keys == nil {
		keys = defaultToolKeys(cfg.Name)
	}

	// Build per-agent skills toolset from the agent's explicit skills list.
	// An empty/nil list means all installed skills in the registry are visible.
	// Always rebuilt per-agent so each agent sees exactly its declared skills.
	resolvedSkillTS := skillTS // fallback if Toolset fails
	if ts, err := skills.Toolset(ctx, cfg.Skills); err == nil {
		resolvedSkillTS = ts
	}
	resolvedSoftSkillTS := softSkillTS
	agentSoftSkillsDir := cfg.SoftSkillsDir
	if agentSoftSkillsDir == "" && cfg.Name != "" {
		agentSoftSkillsDir = filepath.Join(runtime.SoftSkillsDir, cfg.Name)
	}
	if agentSoftSkillsDir != "" && agentSoftSkillsDir != runtime.SoftSkillsDir {
		// Sub-agents use the glob-only soft-skill loader (recall is mounted on
		// the leader in Phase 1); pass a nil embedder.
		if sts, err := softskills.Toolset(ctx, agentSoftSkillsDir, nil); err == nil {
			resolvedSoftSkillTS = sts
		}
	}
	// Resolve the candidate MCP handle pool for this agent: by default the
	// leader's handles, unless the agent points at its own mcp_config.json.
	// `mcpHandles` (returned) tracks only handles we *acquired* for this
	// agent, so Instance.Close releases them exactly once.
	resolvedMCPHandles := leaderMCPHandles
	var mcpHandles []*mcpcfg.Handle
	if cfg.MCPConfigPath != "" && cfg.MCPConfigPath != runtime.MCPConfigPath && pool != nil {
		if mc, err := mcpcfg.Load(cfg.MCPConfigPath); err == nil {
			if _, hs, err := pool.AcquireAll(mc); err == nil {
				resolvedMCPHandles = hs
				mcpHandles = hs
			}
		}
	}

	// namedTools allows individual tool names (e.g. "Bash", "Read") to be
	// listed directly in agent.json alongside group keys. SerpAPI WebSearch
	// overwrites the DDG version when a key is configured.
	namedTools := buildNamedToolMap(runtime.SerpAPIKey)

	agentTools := []tool.Tool{}
	toolsets := []tool.Toolset{}
	hasSkills, hasSoftSkills, hasRegistries := false, false, false
	var mountedMCPNames []string
	for _, key := range keys {
		switch key {
		case "fs":
			agentTools = append(agentTools, fstools.New()...)
		case "mcp":
			// Explicit opt-in: only the servers named in cfg.MCPServers
			// (matched case-insensitively against handle Name) are mounted.
			for _, h := range filterMCPHandles(resolvedMCPHandles, cfg.MCPServers) {
				toolsets = append(toolsets, h.Toolset)
				mountedMCPNames = append(mountedMCPNames, h.Name)
			}
		case "Skill":
			if resolvedSkillTS != nil {
				toolsets = append(toolsets, resolvedSkillTS)
				hasSkills = true
			}
		case "softskills":
			if resolvedSoftSkillTS != nil {
				toolsets = append(toolsets, resolvedSoftSkillTS)
				hasSoftSkills = true
			}
		case "calc":
			agentTools = append(agentTools, fstools.NewCalcTools()...)
		case "ddg":
			agentTools = append(agentTools, fstools.NewDDGTools()...)
		case "serpapi":
			agentTools = append(agentTools, fstools.NewSerpAPITools(runtime.SerpAPIKey)...)
		case "web":
			agentTools = append(agentTools, fstools.NewWebTools()...)
		case "registries":
			agentTools = append(agentTools, registries.NewTools(buildRegistriesDeps(runtime))...)
			hasRegistries = true
		case "code_search":
			// Mounted only when a semantic embedder is configured; otherwise
			// the agent falls back to grep/read (additive contract).
			if codeIdx != nil {
				agentTools = append(agentTools, codeIdx.Tools()...)
				// search_code returns file paths + line ranges; Read, Grep,
				// and Glob are needed to retrieve the actual content. Auto-inject
				// them if not already covered by "fs" or an explicit named key.
				hasFS := false
				for _, k := range keys {
					if k == "fs" {
						hasFS = true
						break
					}
				}
				if !hasFS {
					for _, dep := range []string{"Read", "Grep", "Glob"} {
						already := false
						for _, k := range keys {
							if k == dep {
								already = true
								break
							}
						}
						if !already {
							if t, ok := namedTools[dep]; ok {
								agentTools = append(agentTools, t)
							}
						}
					}
				}
			}
		default:
			if t, ok := namedTools[key]; ok {
				agentTools = append(agentTools, t)
			}
		}
	}
	var instructionParts []string
	if hasSkills {
		instructionParts = append(instructionParts, skills.LoaderProtocol)
	}
	if hasSoftSkills {
		instructionParts = append(instructionParts, softskills.LoaderProtocol)
	}
	if hasSkills && hasSoftSkills {
		instructionParts = append(instructionParts, softskills.LoaderRule)
	}
	if hasRegistries {
		instructionParts = append(instructionParts, registries.LoaderProtocol)
	}
	if p := mcpcfg.BuildLoaderProtocol(mountedMCPNames); p != "" {
		instructionParts = append(instructionParts, p)
	}

	// Mount remote A2A peers selected by cfg.A2AAgents. The config file is
	// optional; a missing file or unknown name is a silent no-op (already
	// surfaced by the editor before the agent is built).
	if len(cfg.A2AAgents) > 0 && runtime.A2AConfigPath != "" {
		if a2aCfg, err := a2a.Load(runtime.A2AConfigPath); err == nil {
			selected := selectA2AAgents(a2aCfg, cfg.A2AAgents)
			if a2aTools := a2a.NewTools(selected); len(a2aTools) > 0 {
				agentTools = append(agentTools, a2aTools...)
				instructionParts = append(instructionParts, buildA2AInstruction(selected))
			}
		}
	}

	extraInstruction := ""
	if len(instructionParts) > 0 {
		extraInstruction = strings.Join(instructionParts, "\n") + "\n"
	}
	return agentTools, toolsets, extraInstruction, mcpHandles
}

// buildRegistriesDeps wires the registries tool group to the live runtime
// settings. Paths are resolved lazily so a user adding a registry or
// installing a skill via the Web UI is reflected on the next tool call.
func buildRegistriesDeps(runtime RuntimeSettings) registries.Deps {
	// Snapshot the agent skills lists at build time. A hot-reload rebuilds
	// the toolset, so in-flight tool calls keep the snapshot they started with.
	agentSkills := make(map[string][]string, len(runtime.Agents))
	for _, a := range runtime.Agents {
		agentSkills[a.Name] = a.Skills
	}
	return registries.Deps{
		RegistryDir: func() string {
			if v := strings.TrimSpace(os.Getenv("YOKE_SKILLS_REGISTRY_DIR")); v != "" {
				return v
			}
			return paths.SkillsRegistryDir()
		},
		ConfigPath: func() string { return registries.ReadConfigPath() },
		ListAgentSkills: func() map[string][]string {
			return agentSkills
		},
		AddSkillToAgent: func(agentName, skillName string) error {
			// Preserve the agent's source layer so a local-layer agent doesn't
			// get forked into $HOME/.yoke/ just because a skill was attached.
			writeDir := paths.AgentsRegistryWriteDirForLayer(layerForAgent(agentName))
			_, err := registries.AddSkillToAgent(paths.AgentsRegistryDir(), writeDir, agentName, skillName)
			return err
		},

		AgentsRegistryDir: func() string {
			if v := strings.TrimSpace(os.Getenv("YOKE_AGENTS_REGISTRY_DIR")); v != "" {
				return v
			}
			return paths.AgentsRegistryDir()
		},

		InstalledMCPNames: func() map[string]bool {
			cfg, err := mcpcfg.Load(paths.FindConfig("mcp_config.json"))
			if err != nil {
				return map[string]bool{}
			}
			out := make(map[string]bool, len(cfg.Servers))
			for _, s := range cfg.ServerList() {
				out[s.Name] = true
			}
			return out
		},

		InstalledSquadNames: func() map[string]bool {
			data, err := os.ReadFile(paths.FindConfig("agents.json"))
			if err != nil {
				return map[string]bool{}
			}
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				return map[string]bool{}
			}
			rawSquads, _ := cfg["squads"].([]any)
			out := make(map[string]bool, len(rawSquads))
			for _, item := range rawSquads {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if name, _ := m["name"].(string); name != "" {
					out[strings.TrimSpace(name)] = true
				}
			}
			return out
		},

		InstalledA2ANames: func() map[string]bool {
			cfg, err := a2a.Load(paths.FindConfig("a2a_config.json"))
			if err != nil {
				return map[string]bool{}
			}
			out := make(map[string]bool, len(cfg.Agents))
			for _, ag := range cfg.AgentList() {
				out[ag.Name] = true
			}
			return out
		},

		InstallMCP: func(ref registries.RepoRef, token, dirPath string) (string, bool, error) {
			body, err := registries.FetchMCPManifest(ref, token, dirPath)
			if err != nil {
				return "", false, err
			}
			// Resolve the server name from manifest "name" field or directory leaf.
			serverName := dirPath
			if i := strings.LastIndex(strings.TrimSuffix(dirPath, ".json"), "/"); i >= 0 {
				serverName = dirPath[i+1:]
			}
			serverName = strings.TrimSuffix(serverName, ".json")
			var nameCheck struct {
				Name string `json:"name,omitempty"`
			}
			if json.Unmarshal(body, &nameCheck) == nil && strings.TrimSpace(nameCheck.Name) != "" {
				serverName = strings.TrimSpace(nameCheck.Name)
			}
			var srv mcpcfg.Server
			if err := json.Unmarshal(body, &srv); err != nil {
				return "", false, fmt.Errorf("parse mcp manifest: %w", err)
			}
			srv.Name = serverName
			cfgPath := paths.FindConfig("mcp_config.json")
			cfg, err := mcpcfg.Load(cfgPath)
			if err != nil {
				return "", false, fmt.Errorf("load mcp_config.json: %w", err)
			}
			_, already := cfg.Servers[serverName]
			if cfg.Servers == nil {
				cfg.Servers = map[string]mcpcfg.Server{}
			}
			cfg.Servers[serverName] = srv
			out, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return "", false, fmt.Errorf("marshal mcp_config.json: %w", err)
			}
			writePath := filepath.Join(paths.WriteDirForLayer(layerForConfigFile("mcp_config.json")), "mcp_config.json")
			if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
				return "", false, err
			}
			return serverName, !already, os.WriteFile(writePath, append(out, '\n'), 0o644)
		},

		InstallAgent: func(ref registries.RepoRef, token, dirPath string, enable bool) (string, bool, error) {
			// $YOKE_AGENTS_REGISTRY_DIR is an explicit operator override and
			// always wins. Otherwise the install follows the layer where the
			// project's agents.json already lives (or user when there is no
			// project-local one).
			layer := layerForConfigFile("agents.json")
			agentsDir := paths.AgentsRegistryWriteDirForLayer(layer)
			if v := strings.TrimSpace(os.Getenv("YOKE_AGENTS_REGISTRY_DIR")); v != "" {
				agentsDir = v
			}
			if err := os.MkdirAll(agentsDir, 0o755); err != nil {
				return "", false, err
			}
			agentName, err := registries.InstallAgent(ref, token, dirPath, agentsDir)
			if err != nil {
				return "", false, err
			}
			enabled := false
			if enable {
				enabled, _ = appendAgentNameToConfig(paths.FindConfig("agents.json"),
					filepath.Join(paths.WriteDirForLayer(layer), "agents.json"), agentName)
			}
			return agentName, enabled, nil
		},

		InstallSquad: func(ref registries.RepoRef, token, dirPath string) (string, bool, error) {
			body, err := registries.FetchSquadJSON(ref, token, dirPath)
			if err != nil {
				return "", false, err
			}
			type squadJSON struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Leader      string   `json:"leader"`
				Members     []string `json:"members"`
			}
			var sq squadJSON
			if err := json.Unmarshal(body, &sq); err != nil {
				return "", false, fmt.Errorf("parse squad.json: %w", err)
			}
			if sq.Name == "" {
				sq.Name = dirPath
				if i := strings.LastIndex(dirPath, "/"); i >= 0 {
					sq.Name = dirPath[i+1:]
				}
			}
			if sq.Name == "" {
				return "", false, fmt.Errorf("could not determine squad name")
			}
			cfgPath := paths.FindConfig("agents.json")
			data, _ := os.ReadFile(cfgPath)
			var cfg map[string]any
			if len(data) > 0 {
				if err := json.Unmarshal(data, &cfg); err != nil {
					return "", false, fmt.Errorf("decode agents.json: %w", err)
				}
			}
			if cfg == nil {
				cfg = map[string]any{}
			}
			entry := map[string]any{
				"name": sq.Name, "description": sq.Description,
				"leader": sq.Leader, "members": sq.Members,
			}
			rawSquads, _ := cfg["squads"].([]any)
			added := true
			for i, item := range rawSquads {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if name, _ := m["name"].(string); strings.EqualFold(strings.TrimSpace(name), sq.Name) {
					rawSquads[i] = entry
					added = false
					break
				}
			}
			if added {
				rawSquads = append(rawSquads, entry)
			}
			cfg["squads"] = rawSquads
			out, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return "", false, fmt.Errorf("encode agents.json: %w", err)
			}
			writePath := filepath.Join(paths.WriteDirForLayer(layerForConfigFile("agents.json")), "agents.json")
			if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
				return "", false, err
			}
			return sq.Name, added, os.WriteFile(writePath, append(out, '\n'), 0o644)
		},

		InstallA2A: func(ref registries.RepoRef, token, dirPath string) (string, bool, error) {
			body, err := registries.FetchA2AAgentJSON(ref, token, dirPath)
			if err != nil {
				return "", false, err
			}
			agentName := dirPath
			if i := strings.LastIndex(dirPath, "/"); i >= 0 {
				agentName = dirPath[i+1:]
			}
			var nameCheck struct {
				Name string `json:"name,omitempty"`
			}
			if json.Unmarshal(body, &nameCheck) == nil && strings.TrimSpace(nameCheck.Name) != "" {
				agentName = strings.TrimSpace(nameCheck.Name)
			}
			var ag a2a.Agent
			if err := json.Unmarshal(body, &ag); err != nil {
				return "", false, fmt.Errorf("parse a2a.json: %w", err)
			}
			ag.Name = agentName
			cfgPath := paths.FindConfig("a2a_config.json")
			cfg, err := a2a.Load(cfgPath)
			if err != nil {
				return "", false, fmt.Errorf("load a2a_config.json: %w", err)
			}
			_, already := cfg.Agents[agentName]
			if cfg.Agents == nil {
				cfg.Agents = map[string]a2a.Agent{}
			}
			cfg.Agents[agentName] = ag
			out, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return "", false, fmt.Errorf("marshal a2a_config.json: %w", err)
			}
			writePath := filepath.Join(paths.WriteDirForLayer(layerForConfigFile("a2a_config.json")), "a2a_config.json")
			if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
				return "", false, err
			}
			return agentName, !already, os.WriteFile(writePath, append(out, '\n'), 0o644)
		},
	}
}

// layerForConfigFile returns the layer where the named config file currently
// resides. Files that live under /etc/yoke (or that don't yet exist) fork
// into "user" — yoke never writes back into the system layer.
func layerForConfigFile(filename string) string {
	p := paths.FindConfig(filename)
	if paths.Layer(p) == "local" {
		return "local"
	}
	return "user"
}

// layerForAgent returns the layer where the named agent's agent.json lives,
// or "user" when the agent has no existing definition.
func layerForAgent(name string) string {
	if l := paths.AgentSourceLayer(name); l == "local" {
		return "local"
	}
	return "user"
}

// appendAgentNameToConfig appends name to the `agents` list in agents.json
// if not already present. Returns (added, error).
func appendAgentNameToConfig(readPath, writePath, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("agent name is empty")
	}
	data, _ := os.ReadFile(readPath)
	var cfg map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return false, fmt.Errorf("decode agents.json: %w", err)
		}
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	rawAgents, _ := cfg["agents"].([]any)
	for _, item := range rawAgents {
		if s, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(s), name) {
			return false, nil
		}
	}
	cfg["agents"] = append(rawAgents, name)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode agents.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(writePath, append(out, '\n'), 0o644)
}

// skillCatalogEntry is one skill discovered on disk for documentation
// purposes (front-matter only — body is never read here).
type skillCatalogEntry struct {
	Name        string
	Description string
}

// scanSkillCatalog reads SKILL.md front matter for the given skill names,
// merging entries across every layer of the skills search chain
// (paths.SkillsAllSearchDirs). When skillNames is nil/empty, all skills
// discovered across layers are scanned. First-wins per name, matching the
// runtime loader's precedence. Results are best-effort: unreadable or
// malformed entries are skipped.
func scanSkillCatalog(skillNames []string) []skillCatalogEntry {
	searchDirs := paths.SkillsAllSearchDirs()
	var names []string
	if len(skillNames) == 0 {
		seen := map[string]bool{}
		for _, dir := range searchDirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() || seen[e.Name()] {
					continue
				}
				seen[e.Name()] = true
				names = append(names, e.Name())
			}
		}
	} else {
		names = skillNames
	}
	var out []skillCatalogEntry
	for _, n := range names {
		for _, dir := range searchDirs {
			b, err := os.ReadFile(filepath.Join(dir, n, "SKILL.md"))
			if err != nil {
				continue
			}
			name, desc := parseSkillFrontMatter(b)
			if name == "" {
				name = n
			}
			out = append(out, skillCatalogEntry{Name: name, Description: desc})
			break
		}
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
	sb.WriteString("The following sub-agents are mounted as tools. Invoke them by name — they return their findings to you automatically. Call at most one sub-agent at a time; wait for its findings before deciding whether another sub-agent call is needed. When one or more skills in a sub-agent's catalog match the user's request, delegate to that sub-agent and explicitly name EVERY applicable skill in the delegation (e.g. \"load the k8s-triage and k8s-log-investigation skills, then …\"). Skills covering the same domain are complementary, not alternatives — listing only the closest match is a delegation bug. The sub-agent retains discretion to load additional skills from its own catalog if it judges them relevant. The same applies to mounted MCP servers: when a sub-agent has an MCP server whose name or domain matches the user's request (e.g. a `github` server for repo/issue/PR questions), delegate to that sub-agent and explicitly tell it to use that server — do not try the task with bash first. Never use transfer_to_agent — it permanently hands off control.\n\n")
	sb.WriteString("## Briefing a sub-agent\n\nA sub-agent starts in a fresh session with no transcript and no memory of this conversation — it sees ONLY the `request` string you pass. Asking a bare question (\"investigate the auth bug\") wastes the call: the sub-agent has to rediscover what you already know. Always brief it with the concrete context it needs to act:\n\n- file paths it should read (e.g. `core/auth/session.go:120-180`)\n- prior findings from earlier in this session\n- which skill to load, or which MCP server to use\n- constraints and out-of-scope items\n- the exact shape of the answer you want back\n\n**Inline vs. file brief — the ~20-line rule.** If the brief fits in roughly 20 lines, write it directly into `request`. If it would be longer (long lists of files, multi-paragraph findings, code excerpts, briefs you will reference more than once), write it to a scratch file first and reference that path in `request` instead — e.g. write `$YOKE_HOME/logs/brief_<topic>.md` (or a path under the current worktree) with the Write tool, then call the sub-agent with a short `request` like \"Read $YOKE_HOME/logs/brief_auth.md, then answer the questions at the bottom.\" This keeps the bulk of the brief out of your own transcript (you pay for tool-call args on every subsequent turn) and lets you update or extend the brief across multiple sub-agent calls.\n\n")

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

		// Surface mounted MCP servers so the leader can route by domain
		// (e.g. a `github` server → route GitHub-related asks to this agent
		// with an explicit instruction to use that server's tools).
		if hasTool(cfg.Tools, "mcp") && len(cfg.MCPServers) > 0 {
			sb.WriteString(fmt.Sprintf("  - MCP servers: %s\n", strings.Join(cfg.MCPServers, ", ")))
		}

		// Surface the skill catalog when this sub-agent has access to the
		// `skills` tool group, so the leader can route by skill ownership.
		if hasTool(cfg.Tools, "Skill") {
			catalog := scanSkillCatalog(cfg.Skills)
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
	case "registries_crawler":
		return "Delegate when you need to discover, inspect, install, or link a skill from a remote registry. Pass the topic and, when linking is needed, the target agent name."
	default:
		return ""
	}
}

// buildNamedToolMap returns a flat map of tool-name → tool covering all
// individually-mountable tools. Callers can list "Bash", "Read", etc. directly
// in agent.json instead of the "fs" group key. SerpAPI WebSearch overwrites
// the DDG entry when an API key is available.
func buildNamedToolMap(serpAPIKey string) map[string]tool.Tool {
	m := make(map[string]tool.Tool)
	for _, t := range fstools.New() {
		m[t.Name()] = t
	}
	for _, t := range fstools.NewCalcTools() {
		m[t.Name()] = t
	}
	for _, t := range fstools.NewWebTools() {
		m[t.Name()] = t
	}
	for _, t := range fstools.NewDDGTools() {
		m[t.Name()] = t
	}
	if serpAPIKey != "" {
		for _, t := range fstools.NewSerpAPITools(serpAPIKey) {
			m[t.Name()] = t
		}
	}
	return m
}

// selectA2AAgents returns the subset of agents defined in cfg whose names
// appear in want, preserving the order of want and silently skipping names
// not present in the config.
func selectA2AAgents(cfg *a2a.Config, want []string) []a2a.Agent {
	if cfg == nil || len(want) == 0 {
		return nil
	}
	out := make([]a2a.Agent, 0, len(want))
	for _, name := range want {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if agent, ok := cfg.Agents[key]; ok {
			agent.Name = key
			out = append(out, agent)
		}
	}
	return out
}

// buildA2AInstruction documents the mounted A2A tools so the agent's model
// knows which `a2a_*` tool to call for what, and — critically — does NOT
// confuse them with teammate_* (which is for in-process session mailboxes,
// a different protocol entirely).
func buildA2AInstruction(agents []a2a.Agent) string {
	var sb strings.Builder
	sb.WriteString("\n# Remote A2A Agents\n\n")
	sb.WriteString("The following remote agents are reachable over the Agent-to-Agent (A2A) JSON-RPC protocol. Each is mounted as a separate tool whose name starts with `a2a_`. Call the relevant `a2a_*` tool with a single `prompt` argument describing the task; it returns the remote agent's full text response.\n\n")
	sb.WriteString("IMPORTANT — routing rules:\n")
	sb.WriteString("- To talk to a remote A2A agent listed below, you MUST call its `a2a_*` tool. Do NOT use `teammate_tell`, `teammate_ask`, or any other mailbox tool for these — those are for in-process session mailboxes between yoke sub-agents in this same process, which is a completely different protocol.\n")
	sb.WriteString("- When the user names one of the agents below (e.g. \"ask the X agent\", \"have new-agent do Y\", \"say hello to the a2a agent foo\"), route to its `a2a_*` tool.\n")
	sb.WriteString("- When the user names a specific REMOTE session (e.g. \"the session called teaching-kite\", \"new-agent's session foo\"), pass that name through as the `session_name` argument to the tool. Without `session_name` the call is stateless and the remote has no memory of prior turns; with it the call targets an existing named conversation on the remote and preserves history. Do NOT guess a session name — use only what the user gave you.\n")
	sb.WriteString("- When the user asks to CREATE a new remote session (e.g. \"start a session called foo on new-agent and ask it ...\", \"open a thread named bar with the X agent\"), pass `session_name` AND `create:true`. Session names must be lowercase-kebab-case (letters, digits, dashes — no spaces, no underscores, no uppercase). If the user supplies an invalid name, ask for one that fits the rules rather than transliterating silently.\n")
	sb.WriteString("- The tool returns the remote agent's reply as text. Surface that reply to the user; do not paraphrase unless asked.\n\n")
	sb.WriteString("Available peers:\n")
	for _, a := range agents {
		desc := strings.TrimSpace(a.Description)
		if desc == "" {
			desc = "no description"
		}
		fmt.Fprintf(&sb, "- tool `%s%s` → remote agent %q at %s — %s\n",
			a2a.ToolPrefix, a2a.SanitizeToolName(a.Name), a.Name, a.URL, desc)
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

// NewAgent creates a fully configured yoke agent that can be used
// by other Go projects. It returns the agent, runner config, and supporting
// components.
//
// Internally NewAgent is a thin wrapper around BuildInfrastructure +
// BuildInstance; callers that need to hot-reload the agent (notably the HTTP
// server) should use the lower-level helpers directly along with Manager.
func NewAgent(ctx context.Context, opts Options) (*AgentResult, error) {
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		return nil, err
	}
	// Propagate the resolved repo back into opts so BuildInstance sees the
	// same value as Infrastructure (worktree tools, etc.).
	if opts.Repo == "" {
		opts.Repo = infra.Repo
	}
	if opts.AppName == "" {
		opts.AppName = infra.AppName
	}
	inst, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		infra.Close()
		return nil, err
	}
	return assembleAgentResult(infra, inst), nil
}

// assembleAgentResult bundles the infrastructure and a single instance into
// the legacy AgentResult shape so existing callers (CLI, TUI, examples) keep
// working without code changes.
func assembleAgentResult(infra *Infrastructure, inst *Instance) *AgentResult {
	leaderCfg := inst.LeaderCfg
	return &AgentResult{
		Agent:                                   inst.Leader,
		SubAgents:                               inst.SubAgents,
		Plugins:                                 inst.Plugins,
		EventBus:                                infra.Bus,
		AskUserRegistry:                         infra.AskUserRegistry,
		LeaderInputTokenPricePerMillion:         leaderCfg.InputTokenPricePerMillion,
		LeaderOutputTokenPricePerMillion:        leaderCfg.OutputTokenPricePerMillion,
		LeaderCachedInputTokenPricePerMillion:   leaderCfg.CachedInputTokenPricePerMillion,
		LeaderCacheCreationTokenPricePerMillion: leaderCfg.CacheCreationTokenPricePerMillion,
		LeaderAllowFileAttachments:              inst.LeaderAllowFileAttachments,
		CuratorIdleTimeout:                      inst.CuratorIdleTimeout,
		RunnerConfig:                            inst.RunnerConfig,
		AgentLoader:                             inst.AgentLoader,
		RegisterSession:                         infra.RegisterSession,
		RenameSession:                           infra.RenameSession,
		UnregisterSession:                       infra.UnregisterSession,
		ListSessionRegistry:                     infra.ListSessionRegistry,
		WatchMailbox:                            infra.WatchMailbox,
	}
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
