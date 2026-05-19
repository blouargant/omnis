package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blouargant/yoke/internal/paths"
)

// SquadEntry describes one named group of agents in the JSON runtime config.
// A squad picks a leader and a set of member sub-agents from the top-level
// `agents:` array; squads don't redefine agents. Selecting a squad per
// chat session controls which leader and which sub-agents the session uses.
type SquadEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Leader      string   `json:"leader"`
	Members     []string `json:"members"`
}

// AgentEntry describes one agent in the JSON runtime config.
type AgentEntry struct {
	Name                  string   `json:"name"`
	ModelRef              string   `json:"model_ref"`
	Provider              string   `json:"provider"`
	Model                 string   `json:"model"`
	BaseURL               string   `json:"base_url"`
	APIKey                string   `json:"api_key"`
	Description           string   `json:"description"`
	Instruction           string   `json:"instruction"`
	Enabled               *bool    `json:"enabled"`
	Leader                *bool    `json:"leader"`
	BuiltIn               *bool    `json:"builtin"`
	AllowFileAttachments  *bool    `json:"allow_file_attachments"`
	Tools                 []string `json:"tools"`
	Skills                []string `json:"skills"`
	SoftSkillsDir         string   `json:"softskills_dir"`
	MCPConfigPath         string   `json:"mcp_config_path"`
	MCPServers            []string `json:"mcp_servers"`
	PermissionsConfigPath string   `json:"permissions_config_path"`
}

// ModelEntry describes one reusable model profile in JSON runtime config.
type ModelEntry struct {
	Provider                   string  `json:"provider"`
	Model                      string  `json:"model"`
	BaseURL                    string  `json:"base_url"`
	APIKey                     string  `json:"api_key"`
	ContextLength              int     `json:"context_length"`
	InputTokenPricePerMillion  float64 `json:"input_token_price_per_million"`
	OutputTokenPricePerMillion float64 `json:"output_token_price_per_million"`
	// CachedInputTokenPricePerMillion is the price for prompt tokens served
	// from the provider's prompt cache (Anthropic cache_read,
	// OpenAI prompt_tokens_details.cached_tokens). Defaults to
	// InputTokenPricePerMillion when unset (i.e. no cache discount).
	CachedInputTokenPricePerMillion float64 `json:"cached_input_token_price_per_million"`
	// CacheCreationTokenPricePerMillion is the price for prompt tokens that
	// populate the provider's prompt cache for the first time (Anthropic
	// cache_creation_input_tokens). Defaults to InputTokenPricePerMillion
	// when unset.
	CacheCreationTokenPricePerMillion float64 `json:"cache_creation_token_price_per_million"`
}

type runtimeConfigFile struct {
	SoftSkillsDir         string                `json:"softskills_dir"`
	AppName               string                `json:"app_name"`
	TokenOptimization     bool                  `json:"token_optimization"`
	BashOutputFiltersDir  string                `json:"bash_output_filters_dir"`
	BashTimeoutSeconds    int                   `json:"bash_timeout_seconds"`
	MCPConfigPath         string                `json:"mcp_config_path"`
	PermissionsConfigPath string                `json:"permissions_config_path"`
	SerpAPIKey            string                `json:"serpapi_key"`
	Models                map[string]ModelEntry `json:"models"`
	Agents                []string              `json:"agents"`
	Squads                []SquadEntry          `json:"squads"`
}

// RuntimeModelConfig is one normalized model profile.
type RuntimeModelConfig struct {
	Name                              string
	Provider                          string
	Model                             string
	BaseURL                           string
	APIKey                            string
	ContextLength                     int
	InputTokenPricePerMillion         float64
	OutputTokenPricePerMillion        float64
	CachedInputTokenPricePerMillion   float64
	CacheCreationTokenPricePerMillion float64
}

// RuntimeAgentConfig is one fully-resolved agent configuration entry.
type RuntimeAgentConfig struct {
	Name                              string
	ModelRef                          string
	Provider                          string
	Model                             string
	BaseURL                           string
	APIKey                            string
	ContextLength                     int
	InputTokenPricePerMillion         float64
	OutputTokenPricePerMillion        float64
	CachedInputTokenPricePerMillion   float64
	CacheCreationTokenPricePerMillion float64
	Description                       string
	Instruction                       string
	Enabled                           bool
	Leader                            bool
	BuiltIn                           bool
	AllowFileAttachments              bool
	Tools                             []string
	// Skills is the explicit list of skill names this agent can access from
	// the shared registry. Nil/empty means all installed skills are visible.
	Skills                []string
	SoftSkillsDir         string
	MCPConfigPath         string
	// MCPServers is the per-agent whitelist of MCP server names (matching
	// `name` fields in the resolved mcp_config.json). An empty / unset list
	// means the agent gets NO MCP servers — opt-in is explicit.
	MCPServers            []string
	PermissionsConfigPath string
}

// RuntimeSquadConfig is one normalized squad: a named group composed of an
// existing leader agent plus a set of member sub-agents. Members are
// references by name into RuntimeSettings.Agents; the squad itself does not
// own agent definitions, skills, tools or MCP — those live on the agents.
type RuntimeSquadConfig struct {
	Name        string
	Description string
	Leader      string
	Members     []string
}

// DefaultSquadName is the name of the squad used when a session does not
// specify one. Always present in RuntimeSettings.Squads after resolution
// (synthesised when the config file does not declare one).
const DefaultSquadName = "default"

// RuntimeSettings is the merged runtime configuration after precedence
// resolution: defaults -> JSON -> ENV -> Options.
type RuntimeSettings struct {
	ConfigPath              string
	SoftSkillsDir           string
	AppName                 string
	BashOutputFilterEnabled bool
	BashOutputFiltersDir    string
	BashTimeoutSeconds      int
	MCPConfigPath           string
	PermissionsConfigPath   string
	SerpAPIKey              string
	Models                  map[string]RuntimeModelConfig
	Agents                  []RuntimeAgentConfig
	// Squads is the normalised list of named agent groups. Always contains
	// at least one entry named DefaultSquadName.
	Squads []RuntimeSquadConfig
	// Curator gate thresholds (YOKE_CURATOR_MIN_TURNS / YOKE_CURATOR_MIN_SUB_AGENT_CALLS).
	// Zero values fall back to the defaults in CuratorGateConfig.
	CuratorMinTurns         int
	CuratorMinSubAgentCalls int
	// CuratorIdleTimeout is the idle-session duration after which the Web UI
	// server fires an automatic curation run (YOKE_CURATOR_IDLE_TIMEOUT).
	// Zero means disabled.
	CuratorIdleTimeout time.Duration
}

// AgentConfig returns the effective config for one agent name.
func (s RuntimeSettings) AgentConfig(name string) (RuntimeAgentConfig, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return RuntimeAgentConfig{}, false
	}
	for _, cfg := range s.Agents {
		if strings.ToLower(strings.TrimSpace(cfg.Name)) == needle {
			return cfg, true
		}
	}
	return RuntimeAgentConfig{}, false
}

// LeaderConfig returns the mandatory leader agent configuration.
func (s RuntimeSettings) LeaderConfig() (RuntimeAgentConfig, bool) {
	return s.AgentConfig("leader")
}

// Squad returns the squad with the given name (case-insensitive).
func (s RuntimeSettings) Squad(name string) (RuntimeSquadConfig, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return RuntimeSquadConfig{}, false
	}
	for _, sq := range s.Squads {
		if sq.Name == needle {
			return sq, true
		}
	}
	return RuntimeSquadConfig{}, false
}

// DefaultSquad returns the squad named DefaultSquadName. Callers can rely on
// it being present after ResolveRuntimeSettings.
func (s RuntimeSettings) DefaultSquad() (RuntimeSquadConfig, bool) {
	return s.Squad(DefaultSquadName)
}

// normalizeNames lower-cases, trims and de-dups a list of names while
// preserving order. Returns nil for an empty input so the field round-trips
// cleanly through JSON.
func normalizeNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeTools(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.TrimSpace(raw)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func defaultAgents() []RuntimeAgentConfig {
	return []RuntimeAgentConfig{
		{
			Name:    "leader",
			Enabled: true,
			Leader:  true,
		},
		{
			Name:    "investigator",
			Enabled: true,
			Tools:   []string{"fs", "mcp"},
		},
		{
			Name:    "summariser",
			Enabled: true,
			Tools:   []string{},
		},
		{
			Name:    "curator",
			Enabled: true,
		},
	}
}

func normalizeModelCatalog(models map[string]ModelEntry) map[string]RuntimeModelConfig {
	if len(models) == 0 {
		return map[string]RuntimeModelConfig{}
	}
	out := make(map[string]RuntimeModelConfig, len(models))
	for rawName, m := range models {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		out[name] = RuntimeModelConfig{
			Name:                              name,
			Provider:                          strings.TrimSpace(m.Provider),
			Model:                             strings.TrimSpace(m.Model),
			BaseURL:                           resolveBaseURLReference(strings.TrimSpace(m.BaseURL)),
			APIKey:                            resolveAPIKeyReference(strings.TrimSpace(m.APIKey)),
			ContextLength:                     m.ContextLength,
			InputTokenPricePerMillion:         m.InputTokenPricePerMillion,
			OutputTokenPricePerMillion:        m.OutputTokenPricePerMillion,
			CachedInputTokenPricePerMillion:   m.CachedInputTokenPricePerMillion,
			CacheCreationTokenPricePerMillion: m.CacheCreationTokenPricePerMillion,
		}
	}
	return out
}

func resolveAgentEntries(entries []AgentEntry, modelCatalog map[string]RuntimeModelConfig) ([]RuntimeAgentConfig, error) {
	out := make([]RuntimeAgentConfig, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if name == "" {
			continue
		}
		modelRef := strings.ToLower(strings.TrimSpace(e.ModelRef))
		refModel := RuntimeModelConfig{}
		if modelRef != "" {
			m, ok := modelCatalog[modelRef]
			if !ok {
				return nil, fmt.Errorf("runtime config: agent %q references unknown model_ref %q", name, modelRef)
			}
			refModel = m
		}
		enabled := true
		if e.Enabled != nil {
			enabled = *e.Enabled
		}
		// Leader flag controls squad-leader eligibility and teammate-tool
		// wiring. The agent literally named "leader" is the canonical default
		// and is always leader-eligible (and always enabled). Any other agent
		// can be marked leader explicitly.
		leader := false
		if e.Leader != nil {
			leader = *e.Leader
		}
		if name == "leader" {
			enabled = true
			leader = true
		}
		allowFileAttachments := false
		if e.AllowFileAttachments != nil {
			allowFileAttachments = *e.AllowFileAttachments
		}
		builtIn := false
		if e.BuiltIn != nil {
			builtIn = *e.BuiltIn
		}
		out = append(out, RuntimeAgentConfig{
			Name:                              name,
			ModelRef:                          modelRef,
			Provider:                          firstNonEmpty(strings.TrimSpace(e.Provider), refModel.Provider),
			Model:                             firstNonEmpty(strings.TrimSpace(e.Model), refModel.Model),
			BaseURL:                           resolveBaseURLReference(firstNonEmpty(strings.TrimSpace(e.BaseURL), refModel.BaseURL)),
			APIKey:                            resolveAPIKeyReference(firstNonEmpty(strings.TrimSpace(e.APIKey), refModel.APIKey)),
			ContextLength:                     refModel.ContextLength,
			InputTokenPricePerMillion:         refModel.InputTokenPricePerMillion,
			OutputTokenPricePerMillion:        refModel.OutputTokenPricePerMillion,
			CachedInputTokenPricePerMillion:   refModel.CachedInputTokenPricePerMillion,
			CacheCreationTokenPricePerMillion: refModel.CacheCreationTokenPricePerMillion,
			Description:                       strings.TrimSpace(e.Description),
			Instruction:                       strings.TrimSpace(e.Instruction),
			Enabled:                           enabled,
			Leader:                            leader,
			BuiltIn:                           builtIn,
			AllowFileAttachments:              allowFileAttachments,
			Tools:                             normalizeTools(e.Tools),
			Skills:                            normalizeNames(e.Skills),
			SoftSkillsDir:                     strings.TrimSpace(e.SoftSkillsDir),
			MCPConfigPath:                     strings.TrimSpace(e.MCPConfigPath),
			MCPServers:                        normalizeNames(e.MCPServers),
			PermissionsConfigPath:             strings.TrimSpace(e.PermissionsConfigPath),
		})
	}
	return out, nil
}

func inheritAgentModelFromLeader(in RuntimeAgentConfig, leader RuntimeAgentConfig) RuntimeAgentConfig {
	out := in
	if strings.TrimSpace(out.Provider) == "" {
		out.Provider = leader.Provider
	}
	if strings.TrimSpace(out.Model) == "" {
		out.Model = leader.Model
	}
	if strings.TrimSpace(out.BaseURL) == "" {
		out.BaseURL = leader.BaseURL
	}
	if strings.TrimSpace(out.APIKey) == "" {
		out.APIKey = leader.APIKey
	}
	if out.ContextLength == 0 {
		out.ContextLength = leader.ContextLength
	}
	if out.InputTokenPricePerMillion == 0 {
		out.InputTokenPricePerMillion = leader.InputTokenPricePerMillion
	}
	if out.OutputTokenPricePerMillion == 0 {
		out.OutputTokenPricePerMillion = leader.OutputTokenPricePerMillion
	}
	if out.CachedInputTokenPricePerMillion == 0 {
		out.CachedInputTokenPricePerMillion = leader.CachedInputTokenPricePerMillion
	}
	if out.CacheCreationTokenPricePerMillion == 0 {
		out.CacheCreationTokenPricePerMillion = leader.CacheCreationTokenPricePerMillion
	}
	return out
}

func withInheritedModels(agents []RuntimeAgentConfig) ([]RuntimeAgentConfig, error) {
	var leader RuntimeAgentConfig
	foundLeader := false
	for _, a := range agents {
		if a.Name == "leader" {
			leader = a
			foundLeader = true
			break
		}
	}
	if !foundLeader {
		return nil, fmt.Errorf("runtime config: missing mandatory agents entry with name=leader")
	}
	out := make([]RuntimeAgentConfig, 0, len(agents))
	for _, a := range agents {
		if a.Name == "leader" {
			out = append(out, a)
			continue
		}
		out = append(out, inheritAgentModelFromLeader(a, leader))
	}
	return out, nil
}

func mergeAgentByName(agents []RuntimeAgentConfig, name string, f func(RuntimeAgentConfig) RuntimeAgentConfig) []RuntimeAgentConfig {
	needle := strings.ToLower(strings.TrimSpace(name))
	for i := range agents {
		if agents[i].Name == needle {
			agents[i] = f(agents[i])
			return agents
		}
	}
	return agents
}

func applyCuratorEnabledOverride(agents []RuntimeAgentConfig, enabled bool) []RuntimeAgentConfig {
	for i := range agents {
		if agents[i].Name == "curator" {
			agents[i].Enabled = enabled
			return agents
		}
	}
	agents = append(agents, RuntimeAgentConfig{Name: "curator", Enabled: enabled})
	return agents
}

func applyLeaderSelectionOverride(agents []RuntimeAgentConfig, provider, model, baseURL, apiKey string) []RuntimeAgentConfig {
	return mergeAgentByName(agents, "leader", func(a RuntimeAgentConfig) RuntimeAgentConfig {
		if strings.TrimSpace(provider) != "" {
			a.Provider = strings.TrimSpace(provider)
		}
		if strings.TrimSpace(model) != "" {
			a.Model = strings.TrimSpace(model)
		}
		if strings.TrimSpace(baseURL) != "" {
			a.BaseURL = strings.TrimSpace(baseURL)
		}
		if strings.TrimSpace(apiKey) != "" {
			a.APIKey = strings.TrimSpace(apiKey)
		}
		return a
	})
}

func applyLeaderModelEnv(agents []RuntimeAgentConfig) []RuntimeAgentConfig {
	provider := strings.TrimSpace(os.Getenv("YOKE_PROVIDER"))
	model := strings.TrimSpace(os.Getenv("YOKE_MODEL"))
	baseURL := strings.TrimSpace(os.Getenv("YOKE_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("YOKE_API_KEY"))
	return applyLeaderSelectionOverride(agents, provider, model, baseURL, apiKey)
}

func mapAgentEntries(entries []RuntimeAgentConfig, fn func(RuntimeAgentConfig) RuntimeAgentConfig) []RuntimeAgentConfig {
	out := make([]RuntimeAgentConfig, 0, len(entries))
	for _, e := range entries {
		out = append(out, fn(e))
	}
	return out
}

func normalizedAgentConfig(in RuntimeAgentConfig) RuntimeAgentConfig {
	return RuntimeAgentConfig{
		Name:                              strings.ToLower(strings.TrimSpace(in.Name)),
		ModelRef:                          strings.ToLower(strings.TrimSpace(in.ModelRef)),
		Provider:                          strings.TrimSpace(in.Provider),
		Model:                             strings.TrimSpace(in.Model),
		BaseURL:                           resolveBaseURLReference(strings.TrimSpace(in.BaseURL)),
		APIKey:                            resolveAPIKeyReference(strings.TrimSpace(in.APIKey)),
		ContextLength:                     in.ContextLength,
		InputTokenPricePerMillion:         in.InputTokenPricePerMillion,
		OutputTokenPricePerMillion:        in.OutputTokenPricePerMillion,
		CachedInputTokenPricePerMillion:   in.CachedInputTokenPricePerMillion,
		CacheCreationTokenPricePerMillion: in.CacheCreationTokenPricePerMillion,
		Description:                       strings.TrimSpace(in.Description),
		Instruction:                       strings.TrimSpace(in.Instruction),
		Enabled:                           in.Enabled,
		Leader:                            in.Leader,
		AllowFileAttachments:              in.AllowFileAttachments,
		Tools:                             normalizeTools(in.Tools),
		Skills:                            normalizeNames(in.Skills),
		SoftSkillsDir:                     strings.TrimSpace(in.SoftSkillsDir),
		MCPConfigPath:                     strings.TrimSpace(in.MCPConfigPath),
		MCPServers:                        normalizeNames(in.MCPServers),
		PermissionsConfigPath:             strings.TrimSpace(in.PermissionsConfigPath),
	}
}

// resolveSquadEntries normalises raw JSON squad entries against the agent
// catalogue. It enforces:
//   - non-empty squad name; names lower-cased and unique
//   - leader and members reference existing, enabled agents
//   - the squad's leader is an agent marked `leader: true`
//   - curator is not a member (it is process-wide)
//   - members are de-duplicated; the leader is never listed as a member
//
// Returns the resolved squads and an error describing the first violation.
func resolveSquadEntries(entries []SquadEntry, agents []RuntimeAgentConfig) ([]RuntimeSquadConfig, error) {
	enabled := map[string]RuntimeAgentConfig{}
	for _, a := range agents {
		if a.Enabled {
			enabled[a.Name] = a
		}
	}
	seenName := map[string]bool{}
	out := make([]RuntimeSquadConfig, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if name == "" {
			return nil, fmt.Errorf("runtime config: squad has empty name")
		}
		if seenName[name] {
			return nil, fmt.Errorf("runtime config: duplicate squad name %q", name)
		}
		seenName[name] = true
		leader := strings.ToLower(strings.TrimSpace(e.Leader))
		if leader == "" {
			return nil, fmt.Errorf("runtime config: squad %q has empty leader", name)
		}
		leaderCfg, ok := enabled[leader]
		if !ok {
			return nil, fmt.Errorf("runtime config: squad %q leader %q is not an enabled agent", name, leader)
		}
		if !leaderCfg.Leader {
			return nil, fmt.Errorf("runtime config: squad %q leader %q is not marked as leader: true", name, leader)
		}
		members := make([]string, 0, len(e.Members))
		seenMember := map[string]bool{leader: true}
		for _, raw := range e.Members {
			m := strings.ToLower(strings.TrimSpace(raw))
			if m == "" || seenMember[m] {
				continue
			}
			seenMember[m] = true
			if _, ok := enabled[m]; !ok {
				return nil, fmt.Errorf("runtime config: squad %q member %q is not an enabled agent", name, m)
			}
			if m == "curator" {
				return nil, fmt.Errorf("runtime config: squad %q cannot include the curator agent (curator is process-wide)", name)
			}
			members = append(members, m)
		}
		out = append(out, RuntimeSquadConfig{
			Name:        name,
			Description: strings.TrimSpace(e.Description),
			Leader:      leader,
			Members:     members,
		})
	}
	return out, nil
}

// synthesizeDefaultSquad builds a `default` squad from the enabled agents
// when no `squads:` block is present. The leader is the agent named
// "leader" (mandatory); members are every other enabled agent except
// "curator" (which is process-wide).
func synthesizeDefaultSquad(agents []RuntimeAgentConfig) RuntimeSquadConfig {
	sq := RuntimeSquadConfig{Name: DefaultSquadName, Leader: "leader"}
	for _, a := range agents {
		if !a.Enabled || a.Name == "leader" || a.Name == "curator" {
			continue
		}
		sq.Members = append(sq.Members, a.Name)
	}
	return sq
}

// ensureDefaultSquad guarantees the squad list contains an entry named
// DefaultSquadName. When the caller provided squads but none is named
// "default", a synthesised default is prepended so the resolved list
// always has a fallback for sessions that don't specify a squad. This
// keeps the editor UX friendly: a user who creates a single non-default
// squad doesn't have to manually re-declare the default one alongside it.
func ensureDefaultSquad(squads []RuntimeSquadConfig, agents []RuntimeAgentConfig) ([]RuntimeSquadConfig, error) {
	for _, sq := range squads {
		if sq.Name == DefaultSquadName {
			return squads, nil
		}
	}
	synth := synthesizeDefaultSquad(agents)
	if len(squads) == 0 {
		return []RuntimeSquadConfig{synth}, nil
	}
	return append([]RuntimeSquadConfig{synth}, squads...), nil
}

// loadAgentFromRegistry loads an agent definition from the registry.
// Path is {registryDir}/{name}/agent.json. If the agent's name field is
// empty, it is inferred from the directory name.
func loadAgentFromRegistry(name, registryDir string) (AgentEntry, error) {
	p := filepath.Join(registryDir, name, "agent.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return AgentEntry{}, fmt.Errorf("agent registry %q: %w", p, err)
	}
	var e AgentEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return AgentEntry{}, fmt.Errorf("agent registry %q: decode json: %w", p, err)
	}
	if e.Name == "" {
		e.Name = name
	}
	return e, nil
}

// ResolveRuntimeSettings loads and merges runtime settings using precedence:
// defaults -> JSON -> ENV -> Options.
func ResolveRuntimeSettings(opts Options) (RuntimeSettings, error) {
	out := RuntimeSettings{
		ConfigPath:              paths.FindConfig("agents.json"),
		SoftSkillsDir:           paths.SoftSkillsDir(),
		AppName:                 "yoke",
		BashOutputFilterEnabled: false,
		BashOutputFiltersDir:    paths.FindConfigDir("filters"),
		BashTimeoutSeconds:      120,
		MCPConfigPath:           paths.FindConfig("mcp_config.json"),
		PermissionsConfigPath:   paths.FindConfig("permissions.json"),
		Models:                  map[string]RuntimeModelConfig{},
		Agents:                  defaultAgents(),
	}

	if strings.TrimSpace(opts.ConfigPath) != "" {
		out.ConfigPath = strings.TrimSpace(opts.ConfigPath)
	}

	cfg, err := loadRuntimeConfig(out.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !opts.ConfigPathStrict {
			cfg = runtimeConfigFile{}
		} else {
			return RuntimeSettings{}, err
		}
	}

	// File
	if strings.TrimSpace(cfg.SoftSkillsDir) != "" {
		out.SoftSkillsDir = strings.TrimSpace(cfg.SoftSkillsDir)
	}
	if strings.TrimSpace(cfg.AppName) != "" {
		out.AppName = strings.TrimSpace(cfg.AppName)
	}
	out.BashOutputFilterEnabled = cfg.TokenOptimization
	if strings.TrimSpace(cfg.BashOutputFiltersDir) != "" {
		out.BashOutputFiltersDir = strings.TrimSpace(cfg.BashOutputFiltersDir)
	}
	if cfg.BashTimeoutSeconds > 0 {
		out.BashTimeoutSeconds = cfg.BashTimeoutSeconds
	}
	if strings.TrimSpace(cfg.MCPConfigPath) != "" {
		out.MCPConfigPath = strings.TrimSpace(cfg.MCPConfigPath)
	}
	if strings.TrimSpace(cfg.PermissionsConfigPath) != "" {
		out.PermissionsConfigPath = strings.TrimSpace(cfg.PermissionsConfigPath)
	}
	if strings.TrimSpace(cfg.SerpAPIKey) != "" {
		out.SerpAPIKey = resolveAPIKeyReference(strings.TrimSpace(cfg.SerpAPIKey))
	}
	if len(cfg.Models) > 0 {
		out.Models = normalizeModelCatalog(cfg.Models)
	}
	if len(cfg.Agents) > 0 {
		agentsRegistryDir := paths.AgentsRegistryDir()
		entries := make([]AgentEntry, 0, len(cfg.Agents))
		for _, name := range cfg.Agents {
			e, err := loadAgentFromRegistry(strings.ToLower(strings.TrimSpace(name)), agentsRegistryDir)
			if err != nil {
				return RuntimeSettings{}, err
			}
			entries = append(entries, e)
		}
		out.Agents, err = resolveAgentEntries(entries, out.Models)
		if err != nil {
			return RuntimeSettings{}, err
		}
	}

	// ENV
	out.Agents = applyLeaderModelEnv(out.Agents)
	if v, ok := parseBoolEnv("YOKE_CURATOR_ENABLED"); ok {
		out.Agents = applyCuratorEnabledOverride(out.Agents, v)
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("YOKE_CURATOR_MIN_TURNS"))); err == nil && v > 0 {
		out.CuratorMinTurns = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("YOKE_CURATOR_MIN_SUB_AGENT_CALLS"))); err == nil && v > 0 {
		out.CuratorMinSubAgentCalls = v
	}
	if raw := strings.TrimSpace(os.Getenv("YOKE_CURATOR_IDLE_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			out.CuratorIdleTimeout = d
		}
	}

	// Options (highest precedence)
	if strings.TrimSpace(opts.SoftSkillsDir) != "" {
		out.SoftSkillsDir = strings.TrimSpace(opts.SoftSkillsDir)
	}
	if strings.TrimSpace(opts.AppName) != "" {
		out.AppName = strings.TrimSpace(opts.AppName)
	}
	if strings.TrimSpace(opts.MCPSConfigPath) != "" {
		out.MCPConfigPath = strings.TrimSpace(opts.MCPSConfigPath)
	}
	if strings.TrimSpace(opts.PermissionsConfigPath) != "" {
		out.PermissionsConfigPath = strings.TrimSpace(opts.PermissionsConfigPath)
	}
	out.Agents = applyLeaderSelectionOverride(out.Agents, opts.ModelProvider, opts.ModelName, opts.ModelBaseURL, opts.ModelAPIKey)
	if opts.CuratorEnabled != nil {
		out.Agents = applyCuratorEnabledOverride(out.Agents, *opts.CuratorEnabled)
	} else if opts.DisableAutoCurate {
		// Backward-compatible alias for explicitly disabling the hook.
		out.Agents = applyCuratorEnabledOverride(out.Agents, false)
	}

	out.Agents = mapAgentEntries(out.Agents, normalizedAgentConfig)
	out.Agents, err = withInheritedModels(out.Agents)
	if err != nil {
		return RuntimeSettings{}, err
	}

	// Squads compose existing agents. Validated against the resolved agent
	// catalogue (post-inheritance) so leader/member references must be
	// enabled, real agents. When the JSON has no squads, synthesize a
	// `default` squad from the enabled agents so callers always have one.
	out.Squads, err = resolveSquadEntries(cfg.Squads, out.Agents)
	if err != nil {
		return RuntimeSettings{}, err
	}
	out.Squads, err = ensureDefaultSquad(out.Squads, out.Agents)
	if err != nil {
		return RuntimeSettings{}, err
	}

	out.ConfigPath = filepath.Clean(out.ConfigPath)
	return out, nil
}

// resolveAPIKeyReference interprets api_key as either a literal key or an
// environment variable name. If an env var with that exact name exists and is
// non-empty, the env value is used.
func resolveAPIKeyReference(v string) string {
	if v == "" {
		return ""
	}
	if resolved := os.Getenv(v); resolved != "" {
		return resolved
	}
	return v
}

func resolveBaseURLReference(v string) string {
	if v == "" {
		return ""
	}
	if resolved := os.Getenv(v); resolved != "" {
		return resolved
	}
	return v
}

func parseBoolEnv(name string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return v, true
}

func loadRuntimeConfig(path string) (runtimeConfigFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return runtimeConfigFile{}, fmt.Errorf("runtime config %q: %w", path, err)
	}
	var cfg runtimeConfigFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		return runtimeConfigFile{}, fmt.Errorf("runtime config %q: decode json: %w", path, err)
	}
	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
