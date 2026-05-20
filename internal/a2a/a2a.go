// Package a2a manages configuration for Agent-to-Agent (A2A) protocol
// connections. Each entry in a2a_config.json describes one remote A2A
// agent endpoint that Yoke can delegate work to.
//
// Config format:
//
//	{
//	  "agents": {
//	    "my-agent": {
//	      "url": "https://agent.example.com",
//	      "description": "Optional description",
//	      "headers": {"Authorization": "Bearer ${input:my_token}"}
//	    }
//	  },
//	  "inputs": [
//	    {"id": "my_token", "type": "promptString", "description": "API token", "password": true}
//	  ]
//	}
//
// The `${input:id}` template syntax may appear inside any string field and
// is resolved interactively at first connect, identical to MCP inputs.
package a2a

import (
	"encoding/json"
	"os"
	"sort"
)

// Agent describes one A2A agent connection.
// The `name` of the agent is the key in the parent Config.Agents map; it is
// populated on Load so callers can use it without separately tracking the key.
type Agent struct {
	Name        string            `json:"-"`
	URL         string            `json:"url"`
	Description string            `json:"description,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// Input is one user-supplied value referenced from agent config via
// "${input:id}" templates. Same shape as MCP inputs so UX is consistent.
type Input struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Password    bool     `json:"password,omitempty"`
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// Config is the top-level JSON structure for a2a_config.json.
type Config struct {
	Agents map[string]Agent `json:"agents"`
	Inputs []Input          `json:"inputs,omitempty"`
}

// AgentList returns the agents in stable lexicographic order so callers get
// a deterministic iteration sequence regardless of map insertion order.
func (c *Config) AgentList() []Agent {
	if c == nil || len(c.Agents) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Agents))
	for k := range c.Agents {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]Agent, 0, len(names))
	for _, n := range names {
		a := c.Agents[n]
		a.Name = n
		out = append(out, a)
	}
	return out
}

// Load parses the JSON at path. A missing file returns an empty config so a
// fresh install with no A2A agents boots cleanly.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Agents: map[string]Agent{}}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Agents == nil {
		c.Agents = map[string]Agent{}
	}
	for k, a := range c.Agents {
		a.Name = k
		c.Agents[k] = a
	}
	return &c, nil
}
