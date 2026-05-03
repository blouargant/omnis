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

// RoleModelConfig describes model selection for one role.
type RoleModelConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
}

type runtimeConfigFile struct {
	SkillsDir             string                     `yaml:"skills_dir"`
	SoftSkillsDir         string                     `yaml:"softskills_dir"`
	AppName               string                     `yaml:"app_name"`
	MCPConfigPath         string                     `yaml:"mcp_config_path"`
	PermissionsConfigPath string                     `yaml:"permissions_config_path"`
	Features              runtimeConfigFeatures      `yaml:"features"`
	Models                runtimeConfigModelSettings `yaml:"models"`
}

type runtimeConfigFeatures struct {
	CuratorEnabled *bool `yaml:"curator_enabled"`
}

type runtimeConfigModelSettings struct {
	Default RoleModelConfig            `yaml:"default"`
	Roles   map[string]RoleModelConfig `yaml:"roles"`
}

// RuntimeSettings is the merged runtime configuration after precedence
// resolution: defaults -> YAML -> ENV -> Options.
type RuntimeSettings struct {
	ConfigPath            string
	SkillsDir             string
	SoftSkillsDir         string
	AppName               string
	MCPConfigPath         string
	PermissionsConfigPath string
	CuratorEnabled        bool
	DefaultModel          RoleModelConfig
	RoleModels            map[string]RoleModelConfig
}

// RoleSelection returns the effective provider/model for a role.
func (s RuntimeSettings) RoleSelection(role string) RoleModelConfig {
	role = strings.ToLower(strings.TrimSpace(role))
	rm, ok := s.RoleModels[role]
	if !ok {
		return s.DefaultModel
	}
	out := RoleModelConfig{
		Provider: rm.Provider,
		Model:    rm.Model,
		BaseURL:  rm.BaseURL,
		APIKey:   rm.APIKey,
	}
	if strings.TrimSpace(out.Provider) == "" {
		out.Provider = s.DefaultModel.Provider
	}
	// If a role changes provider but leaves model empty, keep model empty so
	// llm.NewWith can apply the provider-specific default model.
	if strings.TrimSpace(out.Model) == "" && strings.TrimSpace(rm.Provider) == "" {
		out.Model = s.DefaultModel.Model
	}
	if strings.TrimSpace(out.BaseURL) == "" && strings.TrimSpace(rm.Provider) == "" {
		out.BaseURL = s.DefaultModel.BaseURL
	}
	if strings.TrimSpace(out.APIKey) == "" && strings.TrimSpace(rm.Provider) == "" {
		out.APIKey = s.DefaultModel.APIKey
	}
	return out
}

// ResolveRuntimeSettings loads and merges runtime settings using precedence:
// defaults -> YAML -> ENV -> Options.
func ResolveRuntimeSettings(opts Options) (RuntimeSettings, error) {
	out := RuntimeSettings{
		ConfigPath:            defaultConfigPath,
		SkillsDir:             "skills",
		SoftSkillsDir:         "softskills",
		AppName:               "agent-toolkit",
		MCPConfigPath:         "config/mcp_config.yaml",
		PermissionsConfigPath: "config/permissions.yaml",
		CuratorEnabled:        true,
		RoleModels:            map[string]RoleModelConfig{},
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
	if strings.TrimSpace(cfg.MCPConfigPath) != "" {
		out.MCPConfigPath = strings.TrimSpace(cfg.MCPConfigPath)
	}
	if strings.TrimSpace(cfg.PermissionsConfigPath) != "" {
		out.PermissionsConfigPath = strings.TrimSpace(cfg.PermissionsConfigPath)
	}
	if cfg.Features.CuratorEnabled != nil {
		out.CuratorEnabled = *cfg.Features.CuratorEnabled
	}
	out.DefaultModel = normalizedRoleModel(cfg.Models.Default)
	for role, sel := range cfg.Models.Roles {
		normalizedRole := strings.ToLower(strings.TrimSpace(role))
		if normalizedRole == "" {
			continue
		}
		out.RoleModels[normalizedRole] = normalizedRoleModel(sel)
	}

	// ENV
	if v := strings.TrimSpace(os.Getenv("GOAGENT_PROVIDER")); v != "" {
		out.DefaultModel.Provider = v
	}
	if v := strings.TrimSpace(os.Getenv("GOAGENT_MODEL")); v != "" {
		out.DefaultModel.Model = v
	}
	if v := strings.TrimSpace(os.Getenv("GOAGENT_BASE_URL")); v != "" {
		out.DefaultModel.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("GOAGENT_API_KEY")); v != "" {
		out.DefaultModel.APIKey = v
	}
	if v, ok := parseBoolEnv("GOAGENT_CURATOR_ENABLED"); ok {
		out.CuratorEnabled = v
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
	if strings.TrimSpace(opts.ModelProvider) != "" {
		out.DefaultModel.Provider = strings.TrimSpace(opts.ModelProvider)
	}
	if strings.TrimSpace(opts.ModelName) != "" {
		out.DefaultModel.Model = strings.TrimSpace(opts.ModelName)
	}
	if strings.TrimSpace(opts.ModelBaseURL) != "" {
		out.DefaultModel.BaseURL = strings.TrimSpace(opts.ModelBaseURL)
	}
	if strings.TrimSpace(opts.ModelAPIKey) != "" {
		out.DefaultModel.APIKey = strings.TrimSpace(opts.ModelAPIKey)
	}
	for role, sel := range opts.RoleModels {
		normalizedRole := strings.ToLower(strings.TrimSpace(role))
		if normalizedRole == "" {
			continue
		}
		out.RoleModels[normalizedRole] = normalizedRoleModel(sel)
	}
	if opts.CuratorEnabled != nil {
		out.CuratorEnabled = *opts.CuratorEnabled
	} else if opts.DisableAutoCurate {
		// Backward-compatible alias for explicitly disabling the hook.
		out.CuratorEnabled = false
	}

	out.ConfigPath = filepath.Clean(out.ConfigPath)
	return out, nil
}

func normalizedRoleModel(in RoleModelConfig) RoleModelConfig {
	return RoleModelConfig{
		Provider: strings.TrimSpace(in.Provider),
		Model:    strings.TrimSpace(in.Model),
		BaseURL:  strings.TrimSpace(in.BaseURL),
		APIKey:   resolveAPIKeyReference(strings.TrimSpace(in.APIKey)),
	}
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
