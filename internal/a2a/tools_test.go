package a2a

import (
	"os"
	"strings"
	"testing"
)

func TestNewTools_NamesAndDescriptions(t *testing.T) {
	agents := []Agent{
		{Name: "new-agent", URL: "http://127.0.0.1:8091/", Description: "local sidecar"},
		{Name: "search.peer", URL: "https://example.com/a2a"},
	}
	tools := NewTools(agents)
	if got, want := len(tools), 2; got != want {
		t.Fatalf("len(tools)=%d, want %d", got, want)
	}

	cases := []struct {
		wantName   string
		wantInDesc []string
	}{
		{"a2a_new-agent", []string{"new-agent", "http://127.0.0.1:8091/", "local sidecar"}},
		{"a2a_search_peer", []string{"search.peer", "https://example.com/a2a"}},
	}
	for i, c := range cases {
		if got := tools[i].Name(); got != c.wantName {
			t.Errorf("tools[%d].Name()=%q, want %q", i, got, c.wantName)
		}
		desc := tools[i].Description()
		for _, sub := range c.wantInDesc {
			if !strings.Contains(desc, sub) {
				t.Errorf("tools[%d].Description() missing %q; full=%q", i, sub, desc)
			}
		}
	}
}

func TestNewTools_SkipsEmptyURL(t *testing.T) {
	tools := NewTools([]Agent{
		{Name: "good", URL: "http://x/"},
		{Name: "bad", URL: ""},
		{Name: "blank", URL: "   "},
	})
	if got, want := len(tools), 1; got != want {
		t.Fatalf("len(tools)=%d, want %d", got, want)
	}
	if tools[0].Name() != "a2a_good" {
		t.Fatalf("tools[0].Name()=%q, want a2a_good", tools[0].Name())
	}
}

func TestSanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"new-agent":     "new-agent",       // hyphens preserved
		"already_ok":    "already_ok",      // underscores preserved
		"with.dots":     "with_dots",       // dots collapsed
		"spaces in it":  "spaces_in_it",    // spaces collapsed
		"weird/path[1]": "weird_path_1_",   // slashes/brackets collapsed
		"  trim  ":      "trim",            // outer whitespace trimmed
		"αβγ":           "_",               // non-ASCII collapsed
	}
	for in, want := range cases {
		if got := SanitizeToolName(in); got != want {
			t.Errorf("SanitizeToolName(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestLoad_ResolvesAgentName(t *testing.T) {
	// Lightweight check that Load populates Agent.Name from the map key, since
	// NewTools relies on Name being set when it comes out of the config file.
	dir := t.TempDir()
	path := dir + "/a2a_config.json"
	body := []byte(`{
		"agents": {
			"new-agent": { "url": "http://127.0.0.1:8091/", "description": "demo" }
		}
	}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	agent, ok := cfg.Agents["new-agent"]
	if !ok {
		t.Fatal("expected agent 'new-agent' in config")
	}
	if agent.Name != "new-agent" {
		t.Fatalf("agent.Name=%q, want new-agent", agent.Name)
	}
	if agent.URL != "http://127.0.0.1:8091/" {
		t.Fatalf("agent.URL=%q", agent.URL)
	}
}

