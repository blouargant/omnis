package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/configedit"
)

// --- pure helpers ---------------------------------------------------------

func TestNormalizeSection(t *testing.T) {
	cases := map[string]string{
		"agent": "agents", "agents": "agents", "Models": "models",
		"perms": "permissions", "mcp_servers": "mcp", "pref": "preferences",
		"hook": "hooks", "server.yaml": "server",
	}
	for in, want := range cases {
		if got := normalizeSection(in); got != want {
			t.Errorf("normalizeSection(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseBool(t *testing.T) {
	for _, s := range []string{"true", "1", "yes", "on"} {
		if b, err := parseBool(s); err != nil || !b {
			t.Errorf("parseBool(%q) should be true", s)
		}
	}
	for _, s := range []string{"false", "0", "no", "off"} {
		if b, err := parseBool(s); err != nil || b {
			t.Errorf("parseBool(%q) should be false", s)
		}
	}
	if _, err := parseBool("maybe"); err == nil {
		t.Errorf("parseBool(maybe) should error")
	}
}

func TestRedact(t *testing.T) {
	in := map[string]any{
		"providers": map[string]any{
			"p": map[string]any{"kind": "openai", "base_url": "http://x", "api_key": "SECRET"},
		},
	}
	out := redact(in).(map[string]any)
	p := out["providers"].(map[string]any)["p"].(map[string]any)
	if p["api_key"] != "***set***" {
		t.Errorf("api_key not redacted: %v", p["api_key"])
	}
	if p["base_url"] != "http://x" {
		t.Errorf("base_url should be visible: %v", p["base_url"])
	}
}

func TestCredentialDetection(t *testing.T) {
	if !pointerTouchesCredential("/providers/p/api_key") {
		t.Errorf("pointer with api_key should be sensitive")
	}
	if pointerTouchesCredential("/models/premium/context_length") {
		t.Errorf("context_length pointer should not be sensitive")
	}
	if !valueTouchesCredential(map[string]any{"api_key": "x"}) {
		t.Errorf("value with api_key should be sensitive")
	}
	if valueTouchesCredential(map[string]any{"model": "claude"}) {
		t.Errorf("plain model value should not be sensitive")
	}
}

func TestAvailableThemes(t *testing.T) {
	tmp := t.TempDir()
	themes := filepath.Join(tmp, "css", "themes")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"github-dark.css", "vscode-light.css", "notcss.txt"} {
		if err := os.WriteFile(filepath.Join(themes, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OMNIS_WEB_DIR", tmp)
	got := availableThemes()
	if len(got) != 2 || got[0] != "github-dark" || got[1] != "vscode-light" {
		t.Fatalf("availableThemes=%v", got)
	}
}

// --- functional (temp config dir) ----------------------------------------

// stubConfirmer records the prompt and returns a fixed verdict.
type stubConfirmer struct {
	approve bool
	called  bool
	prompt  string
}

func (s *stubConfirmer) Confirm(_ context.Context, _ string, summary string) (bool, error) {
	s.called = true
	s.prompt = summary
	return s.approve, nil
}

func tempEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("OMNIS_HOME", tmp)
	t.Setenv("OMNIS_CONFIG_DIRS", tmp)
	return tmp
}

// fakeCtx embeds adk.ToolContext (nil) and overrides only SessionID() — the single
// method the settings handlers reach (via confirmSensitive). Any other method
// would panic, but the tested paths never call them.
type fakeCtx struct{ adk.ToolContext }

func (fakeCtx) SessionID() string { return "test-session" }

func testCtx() adk.ToolContext { return fakeCtx{} }

func TestSetPreference(t *testing.T) {
	tempEnv(t)
	// No theme catalogue (OMNIS_WEB_DIR unset → "web" missing): any theme accepted.
	if _, err := setPreference(testCtx(), setPreferenceIn{Key: "theme", Value: "github-dark"}); err != nil {
		t.Fatalf("set theme: %v", err)
	}
	if got := configedit.ReadPreferences()["theme"]; got != "github-dark" {
		t.Fatalf("theme=%v", got)
	}
	// Invalid locale rejected.
	if _, err := setPreference(testCtx(), setPreferenceIn{Key: "locale", Value: "xx"}); err == nil {
		t.Fatalf("expected invalid locale error")
	}
	// notifications coerces to bool.
	if _, err := setPreference(testCtx(), setPreferenceIn{Key: "notifications", Value: "true"}); err != nil {
		t.Fatalf("set notifications: %v", err)
	}
	if got := configedit.ReadPreferences()["notifications"]; got != true {
		t.Fatalf("notifications=%v", got)
	}
}

func TestSetPreferenceResetToDefault(t *testing.T) {
	tempEnv(t)
	// Seed concrete values first.
	_ = configedit.SetPreference("theme", "github-dark")
	_ = configedit.SetPreference("locale", "fr")

	// Empty theme is the documented default sentinel — must be accepted and write "".
	res, err := setPreference(testCtx(), setPreferenceIn{Key: "theme", Value: ""})
	if err != nil {
		t.Fatalf("empty theme should be accepted: %v", err)
	}
	if !res.OK {
		t.Fatalf("empty theme not applied: %#v", res)
	}
	if got, ok := configedit.ReadPreferences()["theme"]; !ok || got != "" {
		t.Fatalf("empty theme should persist as \"\", got %v (present=%v)", got, ok)
	}

	// The "default" alias works too.
	if _, err := setPreference(testCtx(), setPreferenceIn{Key: "theme", Value: "default"}); err != nil {
		t.Fatalf("default alias should be accepted: %v", err)
	}

	// Resetting locale unsets the key (revert to browser/English default).
	if _, err := setPreference(testCtx(), setPreferenceIn{Key: "locale", Value: ""}); err != nil {
		t.Fatalf("empty locale should be accepted: %v", err)
	}
	if _, ok := configedit.ReadPreferences()["locale"]; ok {
		t.Fatalf("empty locale should delete the key")
	}
}

func TestUpdateConfigSensitiveGate(t *testing.T) {
	tempEnv(t)
	reloaded := false
	deps := Deps{RequestReload: func() bool { reloaded = true; return true }}

	// Deny path: confirmer rejects → nothing written, reload not called.
	deny := &stubConfirmer{approve: false}
	SetConfirmer(deny)
	res, err := updateConfig(deps)(testCtx(), updateConfigIn{
		Section: "permissions", Pointer: "/permissions/allow/-", ValueJSON: `"Bash(kubectl get *)"`,
	})
	if err != nil {
		t.Fatalf("update (deny): %v", err)
	}
	if res.OK || !deny.called || reloaded {
		t.Fatalf("deny path should not apply: ok=%v called=%v reloaded=%v", res.OK, deny.called, reloaded)
	}

	// Approve path: confirmer accepts → rule written + reload fired.
	approve := &stubConfirmer{approve: true}
	SetConfirmer(approve)
	res, err = updateConfig(deps)(testCtx(), updateConfigIn{
		Section: "permissions", Pointer: "/permissions/allow/-", ValueJSON: `"Bash(kubectl get *)"`,
	})
	if err != nil {
		t.Fatalf("update (approve): %v", err)
	}
	if !res.OK || !approve.called || !reloaded {
		t.Fatalf("approve path should apply: %#v reloaded=%v", res, reloaded)
	}
	parsed, _, _, _, _ := configedit.ReadSection("permissions")
	allow := parsed.(map[string]any)["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(kubectl get *)" {
		t.Fatalf("permission rule not written: %#v", allow)
	}
	SetConfirmer(nil)
}

func TestUpdateConfigRoutineNoConfirm(t *testing.T) {
	tempEnv(t)
	SetConfirmer(&stubConfirmer{approve: false}) // would deny if consulted
	defer SetConfirmer(nil)
	deps := Deps{RequestReload: func() bool { return true }}
	// A non-sensitive mcp edit must NOT consult the confirmer.
	res, err := updateConfig(deps)(testCtx(), updateConfigIn{
		Section: "mcp", Pointer: "/servers/demo", ValueJSON: `{"command":"demo"}`,
	})
	if err != nil {
		t.Fatalf("update mcp: %v", err)
	}
	if !res.OK {
		t.Fatalf("routine mcp edit should apply directly: %#v", res)
	}
}

func TestSetModelRestartRequired(t *testing.T) {
	tmp := tempEnv(t)
	// Seed a models.json whose embed_model_ref points at "emb".
	seed := `{
      "embed_model_ref": "emb",
      "providers": {"p": {"kind": "openai_compat", "base_url": "http://x", "api_key": "K"}},
      "models": {"emb": {"provider_ref": "p", "model": "text-embed", "dim": 1024, "embedding": true}}
    }`
	if err := os.WriteFile(filepath.Join(tmp, "models.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := Deps{RequestReload: func() bool { return true }}
	res, err := setModel(deps)(testCtx(), setModelIn{ModelRef: "emb", Dim: intPtr(768)})
	if err != nil {
		t.Fatalf("set_model: %v", err)
	}
	if !res.OK || !res.RestartRequired {
		t.Fatalf("changing embedder dim should require restart: %#v", res)
	}
	// A non-embedder model change should NOT require a restart.
	res, err = setModel(deps)(testCtx(), setModelIn{ModelRef: "other", Model: strPtr("claude-x")})
	if err != nil {
		t.Fatalf("set_model new: %v", err)
	}
	if res.RestartRequired {
		t.Fatalf("non-embedder change should not require restart: %#v", res)
	}
}

func TestGetSettingsListsSections(t *testing.T) {
	tempEnv(t)
	out, err := getSettings(testCtx(), getSettingsIn{})
	if err != nil {
		t.Fatalf("get_settings: %v", err)
	}
	if len(out.Sections) == 0 {
		t.Fatalf("expected section catalogue")
	}
}

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }
