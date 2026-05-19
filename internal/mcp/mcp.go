// Package mcp wires Model Context Protocol servers from a JSON config
// shaped after VS Code's MCP schema:
//
//	{
//	  "servers": {
//	    "github": {
//	      "type": "http",
//	      "url": "https://api.githubcopilot.com/mcp/",
//	      "headers": {"Authorization": "Bearer ${input:github_pat}"}
//	    }
//	  },
//	  "inputs": [
//	    {"id": "github_pat", "type": "promptString",
//	     "description": "GitHub Personal Access Token", "password": true}
//	  ]
//	}
//
// Two transports are supported: "stdio" (default) launches a child
// process; "http" connects to a remote MCP server via the streamable-
// HTTP transport. The `${input:id}` template syntax may appear inside
// any string field — env values, header values, command args, the URL,
// the command name — and is resolved interactively at first connect
// using the askuser registry. See secrets.go for the resolver.
package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
)

// Transport kind constants for Server.Type.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Input kind constants for Input.Type.
const (
	InputPromptString = "promptString" // text entry; mask if Password=true
	InputPickString   = "pickString"   // pick one from Options
)

// Server describes one MCP server. The `name` of the server is the key
// in the parent Config.Servers map; it is populated on Load so callers
// can use it without separately tracking the map key.
type Server struct {
	Name    string            `json:"-"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// TransportKind returns the normalised transport kind for s, defaulting
// to "stdio" when unset or unrecognised.
func (s Server) TransportKind() string {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case TransportHTTP:
		return TransportHTTP
	default:
		return TransportStdio
	}
}

// Input is one user-supplied value referenced from servers via
// "${input:id}" templates. Matches the subset of VS Code's mcp.json
// input schema we support: promptString and pickString.
type Input struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Password    bool     `json:"password,omitempty"`
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// Kind returns the normalised input kind, defaulting to promptString.
func (in Input) Kind() string {
	switch strings.ToLower(strings.TrimSpace(in.Type)) {
	case strings.ToLower(InputPickString):
		return InputPickString
	default:
		return InputPromptString
	}
}

// Config is the top-level JSON structure.
type Config struct {
	Servers map[string]Server `json:"servers"`
	Inputs  []Input           `json:"inputs,omitempty"`
}

// ServerList returns the servers in stable iteration order (lexicographic
// by name). The pool relies on a fixed order to release handles in
// reverse-acquire order on rollback.
func (c *Config) ServerList() []Server {
	if c == nil || len(c.Servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Servers))
	for k := range c.Servers {
		names = append(names, k)
	}
	// Stable order avoids non-determinism in dedup / logs.
	sortStrings(names)
	out := make([]Server, 0, len(names))
	for _, n := range names {
		s := c.Servers[n]
		s.Name = n
		out = append(out, s)
	}
	return out
}

// inputByID returns the input with the given id, or false if no input
// is defined with that id. Used by the resolver to look up an input
// referenced from a "${input:id}" template.
func (c *Config) inputByID(id string) (Input, bool) {
	if c == nil {
		return Input{}, false
	}
	for _, in := range c.Inputs {
		if in.ID == id {
			return in, true
		}
	}
	return Input{}, false
}

// Load parses the JSON at `path`. A missing file returns an empty
// config (so a fresh install with no MCP servers boots cleanly).
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Servers: map[string]Server{}}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Servers == nil {
		c.Servers = map[string]Server{}
	}
	// Stamp each Server with its map-key name so callers don't need to
	// pass the key alongside.
	for k, s := range c.Servers {
		s.Name = k
		c.Servers[k] = s
	}
	return &c, nil
}

// Toolsets builds one ADK toolset per configured MCP server. This is
// the non-pooled path used by ad-hoc callers (tests, examples); the
// server uses Pool.AcquireAll for refcounted sharing across agent
// generations and for input-template resolution.
func (c *Config) Toolsets() ([]tool.Toolset, error) {
	var out []tool.Toolset
	for _, s := range c.ServerList() {
		ts, err := buildToolset(s)
		if err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, nil
}

// buildToolset returns the ADK toolset for one server, dispatching on
// the configured transport. Used by both Toolsets() and the pool path
// (after input resolution).
func buildToolset(s Server) (tool.Toolset, error) {
	transport, err := buildTransport(s)
	if err != nil {
		return nil, err
	}
	ts, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", s.Name, err)
	}
	return ts, nil
}

// buildTransport constructs the underlying MCP transport for one
// server. Callers must have already resolved any "${input:id}"
// references before invoking it — buildTransport itself does no
// templating.
func buildTransport(s Server) (mcp.Transport, error) {
	switch s.TransportKind() {
	case TransportHTTP:
		if strings.TrimSpace(s.URL) == "" {
			return nil, fmt.Errorf("mcp server %q: http transport requires `url`", s.Name)
		}
		return &mcp.StreamableClientTransport{
			Endpoint:   s.URL,
			HTTPClient: httpClientWithHeaders(s.Headers),
		}, nil
	default:
		if strings.TrimSpace(s.Command) == "" {
			return nil, fmt.Errorf("mcp server %q: stdio transport requires `command`", s.Name)
		}
		cmd := exec.Command(s.Command, s.Args...)
		cmd.Env = os.Environ()
		for k, v := range s.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	}
}

// httpClientWithHeaders returns an *http.Client that injects the given
// headers on every request. Returns nil when there are no custom
// headers, letting the SDK fall back to its default client.
func httpClientWithHeaders(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	hs := make(map[string]string, len(headers))
	for k, v := range headers {
		hs[k] = v
	}
	return &http.Client{
		Transport: &headerInjector{base: http.DefaultTransport, headers: hs},
	}
}

// headerInjector is an http.RoundTripper that sets the configured
// headers on every outgoing request before delegating to the base
// transport. Headers already set on the request (e.g. Mcp-Session-Id
// from the SDK) are not overwritten.
type headerInjector struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	return h.base.RoundTrip(req)
}

// sortStrings is a tiny indirection so we don't need to import sort
// from the top of this file just for one call. The standard library
// sort is pulled in by pool.go already; this keeps imports lean.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
