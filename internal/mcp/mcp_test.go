package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil || len(cfg.Servers) != 0 {
		t.Fatalf("Load() = %+v, want empty config", cfg)
	}
}

func TestLoadParsesServers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mcp.yaml")
	content := "servers:\n  - name: demo\n    command: npx\n    args: [server]\n    env:\n      FOO: bar\n"
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
	server := cfg.Servers[0]
	if server.Name != "demo" || server.Command != "npx" {
		t.Fatalf("server = %+v", server)
	}
	if len(server.Args) != 1 || server.Args[0] != "server" {
		t.Fatalf("Args = %+v", server.Args)
	}
	if server.Env["FOO"] != "bar" {
		t.Fatalf("Env = %+v", server.Env)
	}
}
