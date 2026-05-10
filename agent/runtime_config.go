package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/agent.yaml"

// AgentEntry describes one agent in the YAML runtime config.
type AgentEntry struct {
	Name                  string   `yaml:"name"`
	ModelRef              string   `yaml:"model_ref"`
	Provider              string   `yaml:"provider"`
	Model                 string   `yaml:"model"`
	BaseURL               string   `yaml:"base_url"`
	APIKey                string   `yaml:"api_key"`
	Description           string   `yaml:"description"`
	Instruction           string   `yaml:"instruction"`
	Enabled               *bool    `yaml:"enabled"`
	Mailbox               *bool    `yaml:"mailbox"`
	AllowFileAttachments  *bool    `yaml:"allow_file_attachments"`
	Tools                 []string `yaml:"tools"`
	SkillsDir             string   `yaml:"skills_dir"`
	SoftSkillsDir         string   `yaml:"softskills_dir"`
	MCPConfigPath         string   `yaml:"mcp_config_path"`
	PermissionsConfigPath string   `yaml:"permissions_config_path"`
}

// ModelEntry describes one reusable model profile in YAML runtime config.
type ModelEntry struct {
	Provider                          string  `yaml:"provider"`
	Model                             string  `yaml:"model"`
	BaseURL                           string  `yaml:"base_url"`
	APIKey                            string  `yaml:"api_key"`
	ContextLength                     int     `yaml:"context_length"`
	InputTokenPricePerMillion         float64 `yaml:"input_token_price_per_million"`
	OutputTokenPricePerMillion        float64 `yaml:"output_token_price_per_million"`
	// CachedInputTokenPricePerMillion is the price for prompt tokens served
	// from the provider's prompt cache (Anthropic cache_read,
	// OpenAI prompt_tokens_details.cached_tokens). Defaults to
	// InputTokenPricePerMillion when unset (i.e. no cache discount).
	CachedInputTokenPricePerMillion   float64 `yaml:"cached_input_token_price_per_million"`
	// CacheCreationTokenPricePerMillion is the price for prompt tokens that
	// populate the provider's prompt cache for the first time (Anthropic
	// cache_creation_input_tokens). Defaults to InputTokenPricePerMillion
	// when unset.
	CacheCreationTokenPricePerMillion float64 `yaml:"cache_creation_token_price_per_million"`
}

type runtimeConfigFile struct {
	SkillsDir               string                `yaml:"skills_dir"`
	SoftSkillsDir           string                `yaml:"softskills_dir"`
	AppName                 string                `yaml:"app_name"`
	TokenOptimization       bool                  `yaml:"token_optimization"`
	BashOutputFiltersDir    string                `yaml:"bash_output_filters_dir"`
	BashTimeoutSeconds      int                   `yaml:"bash_timeout_seconds"`
	MCPConfigPath           string                `yaml:"mcp_config_path"`
	PermissionsConfigPath   string                `yaml:"permissions_config_path"`
	Models                  map[string]ModelEntry `yaml:"models"`
	Agents                  []AgentEntry          `yaml:"agents"`
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
	Mailbox                           bool
	AllowFileAttachments              bool
	Tools                             []string
	SkillsDir                         string
	SoftSkillsDir                     string
	MCPConfigPath                     string
	PermissionsConfigPath             string
}

// RuntimeSettings is the merged runtime configuration after precedence
// resolution: defaults -> YAML -> ENV -> Options.
type RuntimeSettings struct {
	ConfigPath              string
	SkillsDir               string
	SoftSkillsDir           string
	AppName                 string
	BashOutputFilterEnabled bool
	BashOutputFiltersDir    string
	BashTimeoutSeconds      int
	MCPConfigPath           string
	PermissionsConfigPath   string
	Models                  map[string]RuntimeModelConfig
	Agents                  []RuntimeAgentConfig
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

func normalizeTools(in []string) []string {
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
	return out
}

func defaultAgents() []RuntimeAgentConfig {
	return []RuntimeAgentConfig{
		{
			Name:    "leader",
			Enabled: true,
			Mailbox: true,
		},
		{
			Name:    "investigator",
			Enabled: true,
			Mailbox: false,
			Tools:   []string{"fs", "mcp"},
		},
		{
			Name:    "summariser",
			Enabled: true,
			Mailbox: false,
			Tools:   []string{},
		},
		{
			Name:    "curator",
			Enabled: true,
			Mailbox: false,
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
		mailbox := name != "curator"
		if e.Mailbox != nil {
			mailbox = *e.Mailbox
		}
		if name == "leader" {
			enabled = true
			mailbox = true
		}
		allowFileAttachments := false
		if e.AllowFileAttachments != nil {
			allowFileAttachments = *e.AllowFileAttachments
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
			Mailbox:                           mailbox,
			AllowFileAttachments:              allowFileAttachments,
			Tools:                             normalizeTools(e.Tools),
			SkillsDir:                         strings.TrimSpace(e.SkillsDir),
			SoftSkillsDir:                     strings.TrimSpace(e.SoftSkillsDir),
			MCPConfigPath:                     strings.TrimSpace(e.MCPConfigPath),
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
	agents = append(agents, RuntimeAgentConfig{Name: "curator", Enabled: enabled, Mailbox: false})
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
	provider := strings.TrimSpace(os.Getenv("GOAGENT_PROVIDER"))
	model := strings.TrimSpace(os.Getenv("GOAGENT_MODEL"))
	baseURL := strings.TrimSpace(os.Getenv("GOAGENT_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("GOAGENT_API_KEY"))
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
		Mailbox:                           in.Mailbox,
		AllowFileAttachments:              in.AllowFileAttachments,
		Tools:                             normalizeTools(in.Tools),
		SkillsDir:                         strings.TrimSpace(in.SkillsDir),
		SoftSkillsDir:                     strings.TrimSpace(in.SoftSkillsDir),
		MCPConfigPath:                     strings.TrimSpace(in.MCPConfigPath),
		PermissionsConfigPath:             strings.TrimSpace(in.PermissionsConfigPath),
	}
}

// ResolveRuntimeSettings loads and merges runtime settings using precedence:
// defaults -> YAML -> ENV -> Options.
func ResolveRuntimeSettings(opts Options) (RuntimeSettings, error) {
	out := RuntimeSettings{
		ConfigPath:              defaultConfigPath,
		SkillsDir:               "skills",
		SoftSkillsDir:           "softskills",
		AppName:                 "agent-toolkit",
		BashOutputFilterEnabled: false,
		BashOutputFiltersDir:    "config/filters",
		BashTimeoutSeconds:      120,
		MCPConfigPath:           "config/mcp_config.yaml",
		PermissionsConfigPath:   "config/permissions.yaml",
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

	// YAML
	if strings.TrimSpace(cfg.SkillsDir) != "" {
		out.SkillsDir = strings.TrimSpace(cfg.SkillsDir)
	}
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
	if len(cfg.Models) > 0 {
		out.Models = normalizeModelCatalog(cfg.Models)
	}
	if len(cfg.Agents) > 0 {
		out.Agents, err = resolveAgentEntries(cfg.Agents, out.Models)
		if err != nil {
			return RuntimeSettings{}, err
		}
	}

	// ENV
	out.Agents = applyLeaderModelEnv(out.Agents)
	if v, ok := parseBoolEnv("GOAGENT_CURATOR_ENABLED"); ok {
		out.Agents = applyCuratorEnabledOverride(out.Agents, v)
	}

	// Options (highest precedence)
	if strings.TrimSpace(opts.SkillsDir) != "" {
		out.SkillsDir = strings.TrimSpace(opts.SkillsDir)
	}
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
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return runtimeConfigFile{}, fmt.Errorf("runtime config %q: decode yaml: %w", path, err)
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
