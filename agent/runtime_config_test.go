package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRuntimeSettingsPrecedence(t *testing.T) {
	t.Setenv("YOKE_PROVIDER", "openai_compat")
	t.Setenv("YOKE_MODEL", "env-model")
	t.Setenv("YOKE_BASE_URL", "https://env-base/v1")
	t.Setenv("YOKE_API_KEY", "env-global-key")
	t.Setenv("YOKE_CURATOR_ENABLED", "true")
	t.Setenv("CURATOR_KEY_ENV", "resolved-curator-key")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	mustWrite(t, cfgPath, []byte(`
skills_dir: yaml-skills
softskills_dir: yaml-soft
app_name: yaml-app
mcp_config_path: yaml-mcp.yaml
permissions_config_path: yaml-perms.yaml
models:
  leader-default:
    provider: anthropic
    model: yaml-default-model
    base_url: https://yaml-base/v1
    api_key: YAML_KEY_ENV
    context_length: 200000
    input_token_price_per_million: 3
    output_token_price_per_million: 15
  curator-fast:
    model: role-curator-model
    api_key: CURATOR_KEY_ENV
    context_length: 128000
agents:
  - name: leader
    model_ref: leader-default
  - name: curator
    model_ref: curator-fast
    enabled: false
  - name: investigator
    model_ref: leader-default
    provider: openai
    model: role-investigator-model
    enabled: false
    mailbox: false
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

	leader, ok := runtime.AgentConfig("leader")
	if !ok {
		t.Fatal("leader config missing")
	}
	if got := leader.Provider; got != "openai" {
		t.Fatalf("leader.Provider = %q, want openai", got)
	}
	if got := leader.Model; got != "cli-model" {
		t.Fatalf("leader.Model = %q, want cli-model", got)
	}
	if got := leader.BaseURL; got != "https://cli-base/v1" {
		t.Fatalf("leader.BaseURL = %q, want https://cli-base/v1", got)
	}
	if got := leader.APIKey; got != "cli-api-key" {
		t.Fatalf("leader.APIKey = %q, want cli-api-key", got)
	}
	if got := leader.ContextLength; got != 200000 {
		t.Fatalf("leader.ContextLength = %d, want 200000", got)
	}
	if got := leader.InputTokenPricePerMillion; got != 3 {
		t.Fatalf("leader.InputTokenPricePerMillion = %v, want 3", got)
	}
	if got := leader.OutputTokenPricePerMillion; got != 15 {
		t.Fatalf("leader.OutputTokenPricePerMillion = %v, want 15", got)
	}

	cur, ok := runtime.AgentConfig("curator")
	if !ok {
		t.Fatal("curator config missing")
	}
	if cur.Enabled {
		t.Fatal("curator.Enabled = true, want false")
	}
	if got := cur.Provider; got != "openai" {
		t.Fatalf("curator.Provider = %q, want openai", got)
	}
	if got := cur.Model; got != "role-curator-model" {
		t.Fatalf("curator.Model = %q, want role-curator-model", got)
	}
	if got := cur.APIKey; got != "resolved-curator-key" {
		t.Fatalf("curator.APIKey = %q, want resolved-curator-key", got)
	}
	if got := cur.ContextLength; got != 128000 {
		t.Fatalf("curator.ContextLength = %d, want 128000", got)
	}

	inv, ok := runtime.AgentConfig("investigator")
	if !ok {
		t.Fatal("investigator config missing")
	}
	if inv.Enabled {
		t.Fatal("investigator.Enabled = true, want false")
	}
	if inv.Mailbox {
		t.Fatal("investigator.Mailbox = true, want false")
	}
	if inv.Provider != "openai" || inv.Model != "role-investigator-model" {
		t.Fatalf("investigator = %#v, want provider=openai model=role-investigator-model", inv)
	}
}

func TestResolveRuntimeSettingsAPIKeyLiteralWhenEnvMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
agents:
  - name: leader
    model_ref: default
    provider: openai_compat
    model: test-model
    api_key: sk-literal
models:
  default:
    provider: openai_compat
    model: fallback
`))

	runtime, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	leader, ok := runtime.AgentConfig("leader")
	if !ok {
		t.Fatal("leader config missing")
	}
	if got := leader.APIKey; got != "sk-literal" {
		t.Fatalf("leader.APIKey = %q, want sk-literal", got)
	}
}

func TestResolveRuntimeSettingsBaseURLFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
agents:
  - name: leader
    model_ref: default
    provider: openai_compat
    model: test-model
    base_url: BASE_URL_ENV
models:
  default:
    provider: openai_compat
    model: fallback
`))
	t.Setenv("BASE_URL_ENV", "https://resolved-base-url/v1")

	runtime, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	leader, ok := runtime.AgentConfig("leader")
	if !ok {
		t.Fatal("leader config missing")
	}
	if got := leader.BaseURL; got != "https://resolved-base-url/v1" {
		t.Fatalf("leader.BaseURL = %q, want https://resolved-base-url/v1", got)
	}
}

func TestResolveRuntimeSettingsBaseURLLiteralWhenEnvMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
agents:
  - name: leader
    model_ref: default
    provider: openai_compat
    model: test-model
    base_url: https://literal-base-url/v1
models:
  default:
    provider: openai_compat
    model: fallback
`))

	runtime, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	leader, ok := runtime.AgentConfig("leader")
	if !ok {
		t.Fatal("leader config missing")
	}
	if got := leader.BaseURL; got != "https://literal-base-url/v1" {
		t.Fatalf("leader.BaseURL = %q, want https://literal-base-url/v1", got)
	}
}

func TestResolveRuntimeSettingsUnknownModelRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
agents:
  - name: leader
    model_ref: does-not-exist
`))

	_, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want unknown model_ref error")
	}
}

func TestResolveRuntimeSettingsDefaultsWithoutConfigFile(t *testing.T) {
	t.Setenv("YOKE_PROVIDER", "")
	t.Setenv("YOKE_MODEL", "")
	t.Setenv("YOKE_CURATOR_ENABLED", "")

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
	if got := runtime.AppName; got != "yoke" {
		t.Fatalf("AppName = %q, want yoke", got)
	}
	if runtime.BashOutputFilterEnabled {
		t.Fatal("BashOutputFilterEnabled = true, want false")
	}
	if got := runtime.BashOutputFiltersDir; got != "config/filters" {
		t.Fatalf("BashOutputFiltersDir = %q, want config/filters", got)
	}
	if _, ok := runtime.AgentConfig("leader"); !ok {
		t.Fatal("default leader config missing")
	}
	curator, ok := runtime.AgentConfig("curator")
	if !ok {
		t.Fatal("default curator config missing")
	}
	if !curator.Enabled {
		t.Fatal("curator.Enabled = false, want true")
	}
}

func TestResolveRuntimeSettingsBashOutputFilterFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
token_optimization: true
bash_output_filters_dir: config/custom-filters
agents:
  - name: leader
    provider: openai_compat
    model: test
`))

	runtime, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if !runtime.BashOutputFilterEnabled {
		t.Fatal("BashOutputFilterEnabled = false, want true")
	}
	if got := runtime.BashOutputFiltersDir; got != "config/custom-filters" {
		t.Fatalf("BashOutputFiltersDir = %q, want config/custom-filters", got)
	}
}

func TestResolveRuntimeSettingsStrictMissingConfig(t *testing.T) {
	_, err := ResolveRuntimeSettings(Options{ConfigPath: "does-not-exist.yaml", ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want error for missing config")
	}
}

func TestResolveRuntimeSettingsRequiresLeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	mustWrite(t, path, []byte(`
agents:
  - name: investigator
    provider: openai
`))

	_, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want missing leader error")
	}
}

func TestDefaultAgentInstructionsDescribeEvidenceContract(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		want        []string
	}{
		{
			name:        "leader",
			instruction: defaultAgentInstruction("leader"),
			want: []string{
				"focused evidence questions to the 'investigator' sub-agent",
				"compact cited findings",
				"oversized raw tool output",
				"150-250 lines or 2k-4k tokens",
				"do not summarise concise investigator evidence briefs",
			},
		},
		{
			name:        "investigator",
			instruction: defaultAgentInstruction("investigator"),
			want: []string{
				"compact evidence brief",
				"exact sources",
				"confidence",
				"open questions",
				"Quote only decisive excerpts",
			},
		},
		{
			name:        "summariser",
			instruction: defaultAgentInstruction("summariser"),
			want: []string{
				"Preserve source anchors",
				"file paths",
				"line numbers",
				"resource ids",
				"Distinguish facts from guesses",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, want := range tt.want {
				if !strings.Contains(tt.instruction, want) {
					t.Fatalf("defaultAgentInstruction(%q) missing %q\n%s", tt.name, want, tt.instruction)
				}
			}
		})
	}
}

func TestSubAgentCapabilitiesBlockIncludesRoleUsageGuidance(t *testing.T) {
	block := buildSubAgentCapabilitiesBlock([]RuntimeAgentConfig{
		{Name: "leader", Enabled: true},
		{Name: "investigator", Enabled: true, Mailbox: true, Tools: []string{"fs", "skills"}},
		{Name: "summariser", Enabled: true, Mailbox: true, Tools: []string{}},
		{Name: "curator", Enabled: true},
	}, RuntimeSettings{SkillsDir: t.TempDir()})

	want := []string{
		"**investigator**",
		"Delegate focused evidence questions here",
		"compact cited findings",
		"Do not routinely send these reports to summariser",
		"**summariser**",
		"Send oversized raw output",
		"lossy structured brief",
		"preserves source anchors",
	}
	for _, s := range want {
		if !strings.Contains(block, s) {
			t.Fatalf("capabilities block missing %q\n%s", s, block)
		}
	}
	if strings.Contains(block, "**leader**") || strings.Contains(block, "**curator**") {
		t.Fatalf("capabilities block should exclude leader and curator\n%s", block)
	}
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
