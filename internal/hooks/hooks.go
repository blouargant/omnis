// Package hooks implements Claude Code-style lifecycle hooks: user-configured
// shell commands that fire at well-defined moments in the agent loop (before /
// after a tool runs, when a prompt is submitted, when the agent stops, on
// session start / end, before compaction, on notifications).
//
// The on-disk format is hooks.json, whose top-level "hooks" object matches
// Claude Code's hooks block verbatim so an existing Claude Code configuration is
// portable into omnis. The engine (run.go) speaks Claude Code's hook input
// (stdin JSON) and output (exit code + optional stdout JSON) protocol.
//
// The package is intentionally self-contained and surface-agnostic: it is wired
// once as a runner-level plugin + bus subscriptions in agent/hooks_plugin.go, so
// the CLI, TUI, and server all get hooks with no per-surface code — the same
// shape as core/permissions.
package hooks

import (
	"encoding/json"
	"os"
	"regexp"
	"sync"
)

// Hook event names. They match Claude Code's event names exactly so a hooks
// block copied from Claude Code works unchanged.
const (
	PreToolUse       = "PreToolUse"
	PostToolUse      = "PostToolUse"
	UserPromptSubmit = "UserPromptSubmit"
	Stop             = "Stop"
	SubagentStop     = "SubagentStop"
	SessionStart     = "SessionStart"
	SessionEnd       = "SessionEnd"
	PreCompact       = "PreCompact"
	Notification     = "Notification"
)

// AllEvents is the canonical ordered list of supported hook events, used by the
// web UI editor and validation.
var AllEvents = []string{
	PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStop,
	SessionStart, SessionEnd, PreCompact, Notification,
}

// Command is one hook command entry. Only `type: "command"` is supported (the
// sole kind Claude Code defines).
type Command struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds; 0 = engine default
}

// Matcher groups hook commands under a tool-name (or sub-event) regexp. An empty
// or "*" matcher matches everything; for events without a subject (Stop,
// SessionStart, …) the matcher is ignored.
type Matcher struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []Command `json:"hooks"`
}

// File is the on-disk hooks.json shape: { "hooks": { "<Event>": [ Matcher ] } }.
type File struct {
	Hooks map[string][]Matcher `json:"hooks"`
}

// Config is a parsed, ready-to-query hook set. The zero value is not usable;
// build one via Load, Parse, or Empty.
type Config struct {
	events map[string][]Matcher

	mu  sync.Mutex
	rec map[string]*regexp.Regexp // compiled-matcher cache
}

// Empty returns a Config with no hooks.
func Empty() *Config {
	return &Config{events: map[string][]Matcher{}, rec: map[string]*regexp.Regexp{}}
}

// Load reads and parses hooks.json from path. A missing file yields an empty
// Config and no error, so hooks are purely opt-in.
func Load(path string) (*Config, error) {
	if path == "" {
		return Empty(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Empty(), nil
		}
		return Empty(), err
	}
	return Parse(data)
}

// Parse builds a Config from raw hooks.json bytes. Empty input is an empty
// Config. Both the wrapped ({"hooks": {…}}) and a bare ({"PreToolUse": [...]})
// shape are accepted for resilience.
func Parse(data []byte) (*Config, error) {
	c := Empty()
	if len(data) == 0 {
		return c, nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return c, err
	}
	if f.Hooks != nil {
		c.events = f.Hooks
		return c, nil
	}
	// Fall back to a bare event map (no "hooks" wrapper).
	var bare map[string][]Matcher
	if err := json.Unmarshal(data, &bare); err == nil && len(bare) > 0 {
		c.events = bare
	}
	return c, nil
}

// HasRules reports whether any event has at least one command configured.
func (c *Config) HasRules() bool {
	if c == nil {
		return false
	}
	for _, ms := range c.events {
		for _, m := range ms {
			if len(m.Hooks) > 0 {
				return true
			}
		}
	}
	return false
}

// Events returns the configured matchers for event (nil when none). Used by the
// engine and tests.
func (c *Config) Events(event string) []Matcher {
	if c == nil {
		return nil
	}
	return c.events[event]
}

// Match returns the hook commands configured for event whose matcher matches
// subject. subject is the tool name (PreToolUse / PostToolUse), the sub-trigger
// (PreCompact: "manual" / "auto"), or "" for events without a subject (in which
// case every matcher under the event applies). Commands preserve config order.
func (c *Config) Match(event, subject string) []Command {
	if c == nil {
		return nil
	}
	var out []Command
	for _, m := range c.events[event] {
		if c.matcherMatches(m.Matcher, subject) {
			out = append(out, m.Hooks...)
		}
	}
	return out
}

// matcherMatches reports whether a matcher pattern matches subject. An empty or
// "*" pattern matches all. A subject-less event (subject == "") matches every
// matcher. The pattern is treated as a Go regexp (unanchored, like Claude Code);
// a pattern that fails to compile falls back to exact string equality.
func (c *Config) matcherMatches(pattern, subject string) bool {
	if pattern == "" || pattern == "*" || subject == "" {
		return true
	}
	re := c.compile(pattern)
	if re == nil {
		return pattern == subject
	}
	return re.MatchString(subject)
}

func (c *Config) compile(pattern string) *regexp.Regexp {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec == nil {
		c.rec = map[string]*regexp.Regexp{}
	}
	if re, ok := c.rec[pattern]; ok {
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		c.rec[pattern] = nil
		return nil
	}
	c.rec[pattern] = re
	return re
}
