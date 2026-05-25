package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRuntimeSettingsPrecedence(t *testing.T) {
	t.Setenv("YOKE_CURATOR_ENABLED", "true")
	t.Setenv("CURATOR_KEY_ENV", "resolved-curator-key")
	t.Setenv("JSON_KEY_ENV", "resolved-json-key")

	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader", ModelRef: "leader-default"},
		{Name: "curator", ModelRef: "curator-fast", Enabled: ptrBool(false)},
		{Name: "investigator", Enabled: ptrBool(false)},
	})

	cfgPath := filepath.Join(dir, "agent.json")
	mustWrite(t, cfgPath, []byte(`{
  "skills_dir": "json-skills",
  "softskills_dir": "json-soft",
  "app_name": "json-app",
  "mcp_config_path": "json-mcp.json",
  "permissions_config_path": "json-perms.json",
  "agents": ["leader", "curator", "investigator"]
}`))
	mustWrite(t, filepath.Join(dir, "models.json"), []byte(`{
  "models": {
    "leader-default": {
      "provider": "anthropic",
      "model": "json-default-model",
      "base_url": "https://json-base/v1",
      "api_key": "JSON_KEY_ENV",
      "context_length": 200000,
      "input_token_price_per_million": 3,
      "output_token_price_per_million": 15
    },
    "curator-fast": {
      "provider": "openai",
      "model": "role-curator-model",
      "api_key": "CURATOR_KEY_ENV",
      "context_length": 128000
    }
  }
}`))

	curatorEnabled := false
	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
		AppName:          "cli-app",
		CuratorEnabled:   &curatorEnabled,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}

	if got := runtime.SoftSkillsDir; got != "json-soft" {
		t.Fatalf("SoftSkillsDir = %q, want json-soft", got)
	}
	if got := runtime.AppName; got != "cli-app" {
		t.Fatalf("AppName = %q, want cli-app", got)
	}
	if got := runtime.MCPConfigPath; got != "json-mcp.json" {
		t.Fatalf("MCPConfigPath = %q, want json-mcp.json", got)
	}
	if got := runtime.PermissionsConfigPath; got != "json-perms.json" {
		t.Fatalf("PermissionsConfigPath = %q, want json-perms.json", got)
	}

	leader, ok := runtime.AgentConfig("leader")
	if !ok {
		t.Fatal("leader config missing")
	}
	if got := leader.Provider; got != "anthropic" {
		t.Fatalf("leader.Provider = %q, want anthropic", got)
	}
	if got := leader.Model; got != "json-default-model" {
		t.Fatalf("leader.Model = %q, want json-default-model", got)
	}
	if got := leader.BaseURL; got != "https://json-base/v1" {
		t.Fatalf("leader.BaseURL = %q, want https://json-base/v1", got)
	}
	if got := leader.APIKey; got != "resolved-json-key" {
		t.Fatalf("leader.APIKey = %q, want resolved-json-key", got)
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
	if inv.Leader {
		t.Fatal("investigator.Leader = true, want false")
	}
	// No model_ref of its own — inherits the leader's resolved model fields.
	if inv.Provider != "anthropic" || inv.Model != "json-default-model" {
		t.Fatalf("investigator = %#v, want inherited provider=anthropic model=json-default-model", inv)
	}
}

func TestResolveRuntimeSettingsBaseURLFromProviderEnv(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader", ModelRef: "default"},
	})

	path := filepath.Join(dir, "agent.json")
	mustWrite(t, path, []byte(`{
  "agents": ["leader"]
}`))
	mustWrite(t, filepath.Join(dir, "models.json"), []byte(`{
  "providers": {
    "compat": {"kind": "openai_compat", "base_url": "BASE_URL_ENV"}
  },
  "models": {
    "default": {"provider_ref": "compat", "model": "fallback"}
  }
}`))
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

func TestResolveRuntimeSettingsRejectsLegacyModelsInAgentsJSON(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
	})

	path := filepath.Join(dir, "agent.json")
	mustWrite(t, path, []byte(`{
  "agents": ["leader"],
  "models": {
    "default": {"provider": "openai_compat", "model": "fallback"}
  }
}`))

	_, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want hard-break error for legacy models in agents.json")
	}
	if !strings.Contains(err.Error(), "models.json") {
		t.Fatalf("error %q should mention models.json migration path", err)
	}
}

func TestResolveRuntimeSettingsProviderRefInheritance(t *testing.T) {
	t.Setenv("PROVIDER_KEY_ENV", "resolved-provider-key")

	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader", ModelRef: "anth"},
	})

	path := filepath.Join(dir, "agent.json")
	mustWrite(t, path, []byte(`{
  "agents": ["leader"]
}`))
	mustWrite(t, filepath.Join(dir, "models.json"), []byte(`{
  "providers": {
    "anthropic-prod": {
      "kind": "anthropic",
      "base_url": "https://api.anthropic.com",
      "api_key": "PROVIDER_KEY_ENV"
    }
  },
  "models": {
    "anth": {
      "provider_ref": "anthropic-prod",
      "model": "claude-sonnet-4-6",
      "context_length": 200000
    }
  }
}`))

	runtime, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	leader, ok := runtime.AgentConfig("leader")
	if !ok {
		t.Fatal("leader config missing")
	}
	if got := leader.Provider; got != "anthropic" {
		t.Fatalf("leader.Provider = %q, want anthropic (inherited from provider kind)", got)
	}
	if got := leader.BaseURL; got != "https://api.anthropic.com" {
		t.Fatalf("leader.BaseURL = %q, want https://api.anthropic.com", got)
	}
	if got := leader.APIKey; got != "resolved-provider-key" {
		t.Fatalf("leader.APIKey = %q, want resolved-provider-key", got)
	}
}

func TestResolveRuntimeSettingsUnknownProviderRef(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader", ModelRef: "broken"},
	})

	path := filepath.Join(dir, "agent.json")
	mustWrite(t, path, []byte(`{"agents": ["leader"]}`))
	mustWrite(t, filepath.Join(dir, "models.json"), []byte(`{
  "models": {
    "broken": {"provider_ref": "missing", "model": "x"}
  }
}`))

	_, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want unknown provider_ref error")
	}
}

func TestResolveRuntimeSettingsUnknownModelRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	mustWrite(t, path, []byte(`{
  "agents": [
    {"name": "leader", "model_ref": "does-not-exist"}
  ]
}`))

	_, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want unknown model_ref error")
	}
}

func TestResolveRuntimeSettingsDefaultsWithoutConfigFile(t *testing.T) {
	t.Setenv("YOKE_CURATOR_ENABLED", "")
	// Pin path roots so assertions are stable regardless of the host's
	// real $HOME or ./config layout.
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	t.Setenv("YOKE_CONFIG_DIRS", home)

	runtime, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if got, want := runtime.SoftSkillsDir, filepath.Join(home, "softskills"); got != want {
		t.Fatalf("SoftSkillsDir = %q, want %q", got, want)
	}
	if got := runtime.AppName; got != "yoke" {
		t.Fatalf("AppName = %q, want yoke", got)
	}
	if runtime.BashOutputFilterEnabled {
		t.Fatal("BashOutputFilterEnabled = true, want false")
	}
	if got, want := runtime.BashOutputFiltersDir, filepath.Join(home, "filters"); got != want {
		t.Fatalf("BashOutputFiltersDir = %q, want %q", got, want)
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

func TestResolveRuntimeSettingsBashOutputFilterFromConfig(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
	})

	path := filepath.Join(dir, "agent.json")
	mustWrite(t, path, []byte(`{
  "token_optimization": true,
  "bash_output_filters_dir": "config/custom-filters",
  "agents": ["leader"]
}`))

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
	_, err := ResolveRuntimeSettings(Options{ConfigPath: "does-not-exist.json", ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want error for missing config")
	}
}

func TestResolveRuntimeSettingsRequiresLeader(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "investigator"},
	})

	path := filepath.Join(dir, "agent.json")
	mustWrite(t, path, []byte(`{
  "agents": ["investigator"]
}`))

	_, err := ResolveRuntimeSettings(Options{ConfigPath: path, ConfigPathStrict: true})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() error = nil, want missing leader error")
	}
}

func TestDefaultAgentInstructionsDescribeEvidenceContract(t *testing.T) {
	// Set up a temp registry with instruction files.
	dir := t.TempDir()
	t.Setenv("YOKE_HOME", dir)
	registryDir := filepath.Join(dir, "registry", "agents")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", registryDir, err)
	}

	// Copy instruction files from the real registry.
	for _, name := range []string{"leader", "investigator", "summariser"} {
		srcPath := filepath.Join("..", "registry", "agents", name, "instruction.md")
		content, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("reading %s: %v", srcPath, err)
		}
		dstDir := filepath.Join(registryDir, name)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dstDir, err)
		}
		dstPath := filepath.Join(dstDir, "instruction.md")
		if err := os.WriteFile(dstPath, content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", dstPath, err)
		}
	}

	// Copy default instruction.
	defaultSrcPath := filepath.Join("..", "registry", "agents", "default.md")
	defaultContent, err := os.ReadFile(defaultSrcPath)
	if err != nil {
		t.Fatalf("reading %s: %v", defaultSrcPath, err)
	}
	defaultDstPath := filepath.Join(registryDir, "default.md")
	if err := os.WriteFile(defaultDstPath, defaultContent, 0o644); err != nil {
		t.Fatalf("writing %s: %v", defaultDstPath, err)
	}

	// Now run the tests with instructions available.
	tests := []struct {
		name string
		want []string
	}{
		{
			name: "leader",
			want: []string{
				"focused evidence questions to the 'investigator' sub-agent",
				"compact cited findings",
				"oversized raw tool output",
				"150-250 lines or 2k-4k tokens",
				"do not summarise concise investigator evidence briefs",
			},
		},
		{
			name: "investigator",
			want: []string{
				"compact evidence brief",
				"exact sources",
				"confidence",
				"open questions",
				"Quote only decisive excerpts",
			},
		},
		{
			name: "summariser",
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
			instruction := defaultAgentInstruction(tt.name)
			for _, want := range tt.want {
				if !strings.Contains(instruction, want) {
					t.Fatalf("defaultAgentInstruction(%q) missing %q\n%s", tt.name, want, instruction)
				}
			}
		})
	}
}

func TestSubAgentCapabilitiesBlockIncludesRoleUsageGuidance(t *testing.T) {
	block := buildSubAgentCapabilitiesBlock([]RuntimeAgentConfig{
		{Name: "leader", Enabled: true, Leader: true},
		{Name: "investigator", Enabled: true, Tools: []string{"fs", "Skill"}},
		{Name: "summariser", Enabled: true, Tools: []string{}},
		{Name: "curator", Enabled: true},
	}, RuntimeSettings{})

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

func TestSubAgentCapabilitiesBlockIncludesBriefingGuidance(t *testing.T) {
	block := buildSubAgentCapabilitiesBlock([]RuntimeAgentConfig{
		{Name: "investigator", Enabled: true, Tools: []string{"fs"}},
	}, RuntimeSettings{})

	want := []string{
		"Briefing a sub-agent",
		"fresh session",
		"ONLY the `request` string",
		"file paths it should read",
		"prior findings",
		"20-line rule",
		"scratch file",
		"$YOKE_HOME/logs/brief_",
	}
	for _, s := range want {
		if !strings.Contains(block, s) {
			t.Fatalf("capabilities block missing briefing guidance %q\n%s", s, block)
		}
	}
}

func TestSubAgentCapabilitiesBlockSurfacesMCPServers(t *testing.T) {
	block := buildSubAgentCapabilitiesBlock([]RuntimeAgentConfig{
		{Name: "investigator", Enabled: true, Tools: []string{"Bash", "mcp"}, MCPServers: []string{"github", "kubernetes"}},
		{Name: "summariser", Enabled: true, Tools: []string{"mcp"}},
		{Name: "web_agent", Enabled: true, Tools: []string{"Bash"}, MCPServers: []string{"github"}},
	}, RuntimeSettings{})

	if !strings.Contains(block, "MCP servers: github, kubernetes") {
		t.Fatalf("expected investigator MCP servers line in block:\n%s", block)
	}
	if strings.Contains(block, "**summariser**") && strings.Contains(block[strings.Index(block, "**summariser**"):], "MCP servers:") {
		t.Fatalf("summariser has no MCPServers configured; line should not appear\n%s", block)
	}
	if strings.Contains(block, "**web_agent**") && strings.Contains(block[strings.Index(block, "**web_agent**"):], "MCP servers:") {
		t.Fatalf("web_agent has no `mcp` tool group; line should not appear\n%s", block)
	}
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ptrBool is a helper to create a pointer to a boolean value for test struct initialization.
func ptrBool(b bool) *bool {
	return &b
}

// setupAgentsRegistry creates the registry/agents directory structure for tests.
// It writes each agent definition to registry/agents/{name}/agent.json and
// sets YOKE_HOME to the base directory so paths.AgentsRegistryDir() resolves correctly.
func setupAgentsRegistry(t *testing.T, baseDir string, agents []AgentEntry) {
	t.Helper()
	t.Setenv("YOKE_HOME", baseDir)
	registryDir := filepath.Join(baseDir, "registry", "agents")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", registryDir, err)
	}

	for _, a := range agents {
		name := strings.ToLower(strings.TrimSpace(a.Name))
		if name == "" {
			continue
		}
		agentDir := filepath.Join(registryDir, name)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", agentDir, err)
		}

		agentPath := filepath.Join(agentDir, "agent.json")
		b, _ := json.Marshal(a)
		mustWrite(t, agentPath, b)

		// Copy instruction file if it exists in the real registry.
		srcInstructionPath := filepath.Join("..", "registry", "agents", name, "instruction.md")
		if content, err := os.ReadFile(srcInstructionPath); err == nil {
			dstInstructionPath := filepath.Join(agentDir, "instruction.md")
			mustWrite(t, dstInstructionPath, content)
		}
	}

	// Also copy the default instruction if it exists.
	if content, err := os.ReadFile(filepath.Join("..", "registry", "agents", "default.md")); err == nil {
		mustWrite(t, filepath.Join(registryDir, "default.md"), content)
	}
}
