// Package mcp wires Model Context Protocol servers from a YAML config
// (Phase 5 / s21) using ADK's mcptoolset and the official MCP SDK. v1
// supports stdio servers (the most common kind).
package mcp

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
	"gopkg.in/yaml.v3"
)

// Server is one entry in the YAML config.
type Server struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// Config is the top-level YAML structure.
type Config struct {
	Servers []Server `yaml:"servers"`
}

// Load parses the YAML at `path`. A missing file returns an empty config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Toolsets builds one ADK toolset per configured MCP server.
func (c *Config) Toolsets() ([]tool.Toolset, error) {
	var out []tool.Toolset
	for _, s := range c.Servers {
		cmd := exec.Command(s.Command, s.Args...)
		cmd.Env = os.Environ()
		for k, v := range s.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		ts, err := mcptoolset.New(mcptoolset.Config{
			Transport: &mcp.CommandTransport{Command: cmd},
		})
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", s.Name, err)
		}
		out = append(out, ts)
	}
	return out, nil
}
