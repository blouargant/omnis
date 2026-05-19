package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil || len(cfg.Servers) != 0 {
		t.Fatalf("Load() = %+v, want empty config", cfg)
	}
}

func TestLoadParsesStdioServer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mcp.json")
	content := `{
  "servers": {
    "demo": {
      "command": "npx",
      "args": ["server"],
      "env": {"FOO": "bar"}
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("len(Servers) = %d, want 1", len(cfg.Servers))
	}
	server := cfg.Servers["demo"]
	if server.Name != "demo" {
		t.Fatalf("Name = %q, want demo (Load must stamp map-key onto server)", server.Name)
	}
	if server.Command != "npx" {
		t.Fatalf("Command = %q", server.Command)
	}
	if server.TransportKind() != TransportStdio {
		t.Fatalf("TransportKind() = %q, want %q", server.TransportKind(), TransportStdio)
	}
	if len(server.Args) != 1 || server.Args[0] != "server" {
		t.Fatalf("Args = %+v", server.Args)
	}
	if server.Env["FOO"] != "bar" {
		t.Fatalf("Env = %+v", server.Env)
	}
}

func TestLoadParsesHTTPServerAndInputs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mcp.json")
	content := `{
  "servers": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {"Authorization": "Bearer ${input:github_pat}"}
    }
  },
  "inputs": [
    {
      "id": "github_pat",
      "type": "promptString",
      "description": "GitHub Personal Access Token",
      "password": true
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	server := cfg.Servers["github"]
	if server.TransportKind() != TransportHTTP {
		t.Fatalf("TransportKind() = %q, want http", server.TransportKind())
	}
	if server.URL != "https://api.githubcopilot.com/mcp/" {
		t.Fatalf("URL = %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer ${input:github_pat}" {
		t.Fatalf("Headers = %+v", server.Headers)
	}
	if len(cfg.Inputs) != 1 {
		t.Fatalf("len(Inputs) = %d, want 1", len(cfg.Inputs))
	}
	in := cfg.Inputs[0]
	if in.ID != "github_pat" || !in.Password || in.Kind() != InputPromptString {
		t.Fatalf("Input = %+v", in)
	}
}

func TestBuildTransportRejectsMissingFields(t *testing.T) {
	t.Parallel()

	if _, err := buildTransport(Server{Name: "no-cmd"}); err == nil {
		t.Fatal("stdio with no command: want error")
	}
	if _, err := buildTransport(Server{Name: "no-url", Type: TransportHTTP}); err == nil {
		t.Fatal("http with no url: want error")
	}
}

func TestHeaderInjectorAttachesHeaders(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer secret")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client := httpClientWithHeaders(map[string]string{"Authorization": "Bearer secret"})
	if client == nil {
		t.Fatal("httpClientWithHeaders returned nil for non-empty map")
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	resp.Body.Close()
}

func TestConfigKeyDistinguishesTransports(t *testing.T) {
	t.Parallel()

	stdio := Server{Name: "x", Command: "foo"}
	httpA := Server{Name: "x", Type: TransportHTTP, URL: "https://a/mcp/"}
	httpB := Server{Name: "x", Type: TransportHTTP, URL: "https://b/mcp/"}
	httpAWithHeader := Server{Name: "x", Type: TransportHTTP, URL: "https://a/mcp/", Headers: map[string]string{"Authorization": "Bearer t"}}

	keys := []string{configKey(stdio), configKey(httpA), configKey(httpB), configKey(httpAWithHeader)}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("configKey collision: keys=%v", keys)
		}
		seen[k] = true
	}
	if configKey(httpA) != configKey(Server{Name: "y", Type: TransportHTTP, URL: "https://a/mcp/"}) {
		t.Fatal("configKey must ignore Name")
	}
}

func TestServerListIsSortedByName(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers: map[string]Server{
			"zeta":  {Command: "z"},
			"alpha": {Command: "a"},
			"mu":    {Command: "m"},
		},
	}
	got := cfg.ServerList()
	if len(got) != 3 || got[0].Name != "alpha" || got[1].Name != "mu" || got[2].Name != "zeta" {
		t.Fatalf("ServerList order = %+v, want alpha mu zeta", got)
	}
}
