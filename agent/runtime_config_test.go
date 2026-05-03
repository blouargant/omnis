package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRuntimeSettingsPrecedence(t *testing.T) {
	t.Setenv("GOAGENT_PROVIDER", "openai_compat")
	t.Setenv("GOAGENT_MODEL", "env-model")
	t.Setenv("GOAGENT_BASE_URL", "https://env-base/v1")
	t.Setenv("GOAGENT_API_KEY", "env-global-key")
	t.Setenv("GOAGENT_CURATOR_ENABLED", "true")
	t.Setenv("CURATOR_KEY_ENV", "resolved-curator-key")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	mustWrite(t, cfgPath, []byte(`
skills_dir: yaml-skills
softskills_dir: yaml-soft
app_name: yaml-app
mcp_config_path: yaml-mcp.yaml
permissions_config_path: yaml-perms.yaml
features:
  curator_enabled: false
models:
  default:
    provider: anthropic
    model: yaml-default-model
    base_url: https://yaml-base/v1
    api_key: YAML_KEY_ENV
  roles:
    orchestrator:
      provider: openai
      model: role-orchestrator-model
    curator:
      model: role-curator-model
      api_key: CURATOR_KEY_ENV
`))
	t.Setenv("YAML_KEY_ENV", "resolved-yaml-key")

	curatorEnabled := false
	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
		SkillsDir:        "cli-skills",
		AppName:          "cli-app",
		ModelProvider:    "openai",
		ModelName:        "cli-model",
		ModelBaseURL:     "https://cli-base/v1",
		ModelAPIKey:      "cli-api-key",
		CuratorEnabled:   &curatorEnabled,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}

	if got := runtime.SkillsDir; got != "cli-skills" {
		t.Fatalf("SkillsDir = %q, want cli-skills", got)
	}
	if got := runtime.SoftSkillsDir; got != "yaml-soft" {
		t.Fatalf("SoftSkillsDir = %q, want yaml-soft", got)
	}
	if got := runtime.AppName; got != "cli-app" {
		t.Fatalf("AppName = %q, want cli-app", got)
	}
	if got := runtime.MCPConfigPath; got != "yaml-mcp.yaml" {
		t.Fatalf("MCPConfigPath = %q, want yaml-mcp.yaml", got)
	}
	if got := runtime.PermissionsConfigPath; got != "yaml-perms.yaml" {
		t.Fatalf("PermissionsConfigPath = %q, want yaml-perms.yaml", got)
	}
	if runtime.CuratorEnabled {
		t.Fatalf("CuratorEnabled = true, want false")
	}
	if got := runtime.DefaultModel.Provider; got != "openai" {
		t.Fatalf("DefaultModel.Provider = %q, want openai", got)
	}
	if got := runtime.DefaultModel.Model; got != "cli-model" {
		t.Fatalf("DefaultModel.Model = %q, want cli-model", got)
	}
	if got := runtime.DefaultModel.BaseURL; got != "https://cli-base/v1" {
		t.Fatalf("DefaultModel.BaseURL = %q, want https://cli-base/v1", got)
	}
	if got := runtime.DefaultModel.APIKey; got != "cli-api-key" {
		t.Fatalf("DefaultModel.APIKey = %q, want cli-api-key", got)
	}

	orch := runtime.RoleSelection("orchestrator")
	if orch.Provider != "openai" || orch.Model != "role-orchestrator-model" {
		t.Fatalf("RoleSelection(orchestrator) = %#v, want provider=openai model=role-orchestrator-model", orch)
	}
	cur := runtime.RoleSelection("curator")
	if cur.Provider != "openai" || cur.Model != "role-curator-model" {
		t.Fatalf("RoleSelection(curator) = %#v, want provider=openai model=role-curator-model", cur)
	}
	if cur.APIKey != "resolved-curator-key" {
		t.Fatalf("RoleSelection(curator).APIKey = %q, want resolved-curator-key", cur.APIKey)
	}
	inv := runtime.RoleSelection("investigator")
	if inv.Provider != "openai" || inv.Model != "cli-model" {
		t.Fatalf("RoleSelection(investigator) = %#v, want provider=openai model=cli-model", inv)
	}
}

func TestResolveRuntimeSettingsAPIKeyLiteralWhenEnvMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
models:
  default:
    provider: openai_compat
    model: test-model
    api_key: sk-literal
`))

	runtime, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if got := runtime.DefaultModel.APIKey; got != "sk-literal" {
		t.Fatalf("DefaultModel.APIKey = %q, want sk-literal", got)
	}
}

func TestResolveRuntimeSettingsDefaultsWithoutConfigFile(t *testing.T) {
	t.Setenv("GOAGENT_PROVIDER", "")
	t.Setenv("GOAGENT_MODEL", "")
	t.Setenv("GOAGENT_CURATOR_ENABLED", "")

	runtime, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if got := runtime.SkillsDir; got != "skills" {
		t.Fatalf("SkillsDir = %q, want skills", got)
	}
	if got := runtime.SoftSkillsDir; got != "softskills" {
		t.Fatalf("SoftSkillsDir = %q, want softskills", got)
	}
	if got := runtime.AppName; got != "agent-toolkit" {
		t.Fatalf("AppName = %q, want agent-toolkit", got)
	}
	if !runtime.CuratorEnabled {
		t.Fatal("CuratorEnabled = false, want true")
	}
}

func TestResolveRuntimeSettingsStrictMissingConfig(t *testing.T) {
	_, err := ResolveRuntimeSettings(Options{ConfigPath: "does-not-exist.yaml", ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want error for missing config")
	}
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
