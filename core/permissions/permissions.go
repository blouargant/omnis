// Package permissions implements JSON rule-based permission governance
// (Phase 4 / s15). Three tiers are evaluated in order against every tool
// call: always_deny → always_allow → ask_user. Anything that matches no
// rule falls through to ask (the safe default) so unrecognised commands
// require explicit user confirmation.
//
// The plugin returned by NewPlugin wires this into ADK as a
// BeforeToolCallback so denial happens before the tool runs.
package permissions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"
)

// Decision is the outcome of evaluating a tool call against the rule set.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
	DecisionAsk
)

// Rule is one entry in the JSON config. It may be written as a bare string
// (just the pattern) or as an object {pattern, reason, cwd}.
//
// CWD, when non-empty, limits the rule to invocations whose current
// working directory is that path or a sub-directory of it. Used by
// "Allow in this project" persisted approvals so they don't leak to
// other projects.
type Rule struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	re      *regexp.Regexp
}

// UnmarshalJSON accepts either a JSON string (the pattern) or an object
// with explicit pattern/reason fields.
func (r *Rule) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		r.Pattern = s
		return nil
	}
	type raw Rule
	var tmp raw
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*r = Rule(tmp)
	return nil
}

// Rules is the parsed permissions config.
type Rules struct {
	AlwaysDeny  []Rule `json:"always_deny"`
	AlwaysAllow []Rule `json:"always_allow"`
	AskUser     []Rule `json:"ask_user"`
}

// HasRules reports whether any tier contains at least one rule.
func (r *Rules) HasRules() bool {
	return len(r.AlwaysDeny) > 0 || len(r.AlwaysAllow) > 0 || len(r.AskUser) > 0
}

// Merge returns a new Rules combining base with each overlay appended in order.
// Base rules are evaluated first within each tier; overlay rules follow.
// The compiled regexps from each source are preserved.
func Merge(base *Rules, overlays ...*Rules) *Rules {
	merged := &Rules{
		AlwaysDeny:  make([]Rule, len(base.AlwaysDeny)),
		AlwaysAllow: make([]Rule, len(base.AlwaysAllow)),
		AskUser:     make([]Rule, len(base.AskUser)),
	}
	copy(merged.AlwaysDeny, base.AlwaysDeny)
	copy(merged.AlwaysAllow, base.AlwaysAllow)
	copy(merged.AskUser, base.AskUser)
	for _, o := range overlays {
		merged.AlwaysDeny = append(merged.AlwaysDeny, o.AlwaysDeny...)
		merged.AlwaysAllow = append(merged.AlwaysAllow, o.AlwaysAllow...)
		merged.AskUser = append(merged.AskUser, o.AskUser...)
	}
	return merged
}

// Load reads and compiles a JSON rules file. Missing file yields an empty
// rule set (everything allowed).
func Load(path string) (*Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Rules{}, nil
		}
		return nil, err
	}
	var r Rules
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := r.compile(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *Rules) compile() error {
	sets := []*[]Rule{&r.AlwaysDeny, &r.AlwaysAllow, &r.AskUser}
	for _, set := range sets {
		for i := range *set {
			re, err := regexp.Compile("(?i)" + (*set)[i].Pattern)
			if err != nil {
				return fmt.Errorf("invalid pattern %q: %w", (*set)[i].Pattern, err)
			}
			(*set)[i].re = re
		}
	}
	return nil
}

// Check evaluates a tool call against the rule set, scoped to the
// current working directory (used to filter project-scoped rules). The
// input is the JSON-marshaled tool args; the probe is "toolName <input>".
func (r *Rules) Check(toolName, input, cwd string) (Decision, string) {
	probe := toolName + " " + input
	for _, rl := range r.AlwaysDeny {
		if matchRule(&rl, probe, cwd) {
			return DecisionDeny, rl.Reason
		}
	}
	for _, rl := range r.AlwaysAllow {
		if matchRule(&rl, probe, cwd) {
			return DecisionAllow, rl.Reason
		}
	}
	for _, rl := range r.AskUser {
		if matchRule(&rl, probe, cwd) {
			return DecisionAsk, rl.Reason
		}
	}
	return DecisionAsk, "command is not on the allow list — confirm before running"
}

// matchRule reports whether r matches probe under cwd. A rule without a
// CWD constraint matches anywhere; with one, the current working
// directory must be the configured path or a sub-directory of it.
func matchRule(r *Rule, probe, cwd string) bool {
	if r.re == nil || !r.re.MatchString(probe) {
		return false
	}
	if r.CWD == "" {
		return true
	}
	return cwdMatches(r.CWD, cwd)
}

// cwdMatches reports whether candidate is scope or a sub-directory of
// scope. Both paths are cleaned for comparison; a trailing slash on
// scope is tolerated.
func cwdMatches(scope, candidate string) bool {
	if scope == "" || candidate == "" {
		return false
	}
	scope = strings.TrimRight(scope, "/")
	candidate = strings.TrimRight(candidate, "/")
	if scope == candidate {
		return true
	}
	return strings.HasPrefix(candidate, scope+"/")
}

// AskOutcome is what an Asker returns after prompting the user. It
// drives both the immediate decision (allow this call or reject it)
// and any persistence side effects (write a new rule to the user's
// permissions.json).
type AskOutcome int

const (
	// OutcomeDeny rejects this call; the next identical call asks again.
	OutcomeDeny AskOutcome = iota
	// OutcomeAllowOnce permits this call only. Implementations may
	// cache the approval for the rest of the session so identical
	// calls don't re-prompt; nothing is persisted to disk.
	OutcomeAllowOnce
	// OutcomeAllowToolSession permits this call and every later call of
	// the same tool (regardless of arguments) for the rest of the
	// session. The grant is cached in memory only — nothing is persisted
	// to disk and it evaporates when the session ends. Lets the user
	// approve a burst of same-tool calls (e.g. four Writes) with one
	// click without granting a durable, cross-session rule.
	OutcomeAllowToolSession
	// OutcomeAllowProject permits this call and persists a rule in the
	// user permissions file scoped to the current working directory.
	// Future identical calls in this project auto-allow; other
	// projects still ask.
	OutcomeAllowProject
	// OutcomeAllowAlways permits this call and persists a rule with no
	// CWD scope, so identical calls from any project auto-allow.
	OutcomeAllowAlways
)

// Asker is the interface used to prompt the user when an ask_user rule
// fires. Implementations route the prompt to whichever UI surface is
// attached (web UI popup, TUI dialog, terminal). The tool.Context lets
// the asker reach the owning session.
type Asker interface {
	Ask(tc tool.Context, toolName, input, reason string) AskOutcome
}

// StdinAsker prompts on stderr (so it doesn't pollute stdout) and reads
// from stdin. Used by the CLI fallback path when no richer surface is
// installed.
type StdinAsker struct{}

func (StdinAsker) Ask(_ tool.Context, toolName, input, reason string) AskOutcome {
	fmt.Fprintf(os.Stderr,
		"\n[PERMISSION] %s: %s\n  Reason: %s\n"+
			"  [d] deny, [o] allow once, [s] allow all %s this session, [p] allow in project, [a] allow always: ",
		toolName, input, reason, toolName)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return OutcomeDeny
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "o", "once", "y", "yes":
		return OutcomeAllowOnce
	case "s", "session", "tool":
		return OutcomeAllowToolSession
	case "p", "project":
		return OutcomeAllowProject
	case "a", "always", "all":
		return OutcomeAllowAlways
	default:
		return OutcomeDeny
	}
}

// Source provides the active rule set. Implementations may swap rules
// atomically when an underlying file changes (see Reloader).
type Source interface {
	Snapshot() *Rules
}

// Static wraps a fixed *Rules; used in tests and when no live reload is
// needed.
type Static struct{ R *Rules }

func (s *Static) Snapshot() *Rules { return s.R }

// PluginConfig wires the permissions plugin.
type PluginConfig struct {
	Name string
	// Source returns the currently active rule set on every call.
	Source Source
	// Asker prompts the user when the rules say "ask"; required.
	Asker Asker
	// UserConfigPath is where OutcomeAllowProject / OutcomeAllowAlways
	// rules are appended. Optional; if empty, those outcomes degrade
	// to OutcomeAllowOnce (no persistence).
	UserConfigPath string
	// CWDFunc returns the current working directory for cwd-scoped
	// rule matching and project-scoped persistence. Defaults to
	// os.Getwd.
	CWDFunc func() string
	// OnPersist is called after a new rule is appended to
	// UserConfigPath, so the caller can reload the rule set. Optional.
	OnPersist func()
	// Debug enables a [perms] log line per tool call that records the
	// session id, the decision, and (when relevant) where an approval
	// was cached or persisted. Use when diagnosing "did session B
	// inherit session A's approval?".
	Debug bool
}

// SessionCleaner exposes the internal session-approval cache so the
// surrounding agent can wipe a session's cached approvals when the
// session ends (e.g. via EventSessionEnd). NewPluginFromConfig returns
// one alongside the plugin.
type SessionCleaner interface {
	Forget(sessionID string)
}

// NewPlugin returns an ADK plugin that enforces the rules via
// BeforeToolCallback. It loads rules from configPath; if the file is
// missing, the plugin is a no-op.
func NewPlugin(name, configPath string, asker Asker) (*plugin.Plugin, error) {
	rules, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	return NewPluginFromRules(name, rules, asker)
}

// NewPluginFromRules returns an ADK plugin from a fixed Rules set. No
// session cache, no persistence — kept for the simple call sites
// (CLI, tests). For the full feature set use NewPluginFromConfig.
func NewPluginFromRules(name string, rules *Rules, asker Asker) (*plugin.Plugin, error) {
	p, _, err := NewPluginFromConfig(PluginConfig{
		Name:   name,
		Source: &Static{R: rules},
		Asker:  asker,
	})
	return p, err
}

// NewPluginFromConfig wires the permissions plugin with full support
// for live-reloadable rules, a session-scoped approval cache, and the
// AllowProject / AllowAlways persistence path. The returned
// SessionCleaner exposes the internal cache so the caller can call
// Forget(sessionID) on session-end events.
func NewPluginFromConfig(cfg PluginConfig) (*plugin.Plugin, SessionCleaner, error) {
	if cfg.Source == nil {
		return nil, nil, fmt.Errorf("permissions: PluginConfig.Source is required")
	}
	if cfg.Asker == nil {
		cfg.Asker = StdinAsker{}
	}
	if cfg.CWDFunc == nil {
		cfg.CWDFunc = func() string {
			d, _ := os.Getwd()
			return d
		}
	}
	cache := newSessionApprovalCache()
	logf := func(format string, a ...any) {
		if cfg.Debug {
			fmt.Fprintf(os.Stderr, "[perms] "+format+"\n", a...)
		}
	}

	cb := func(tc tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		input := flattenArgs(args)
		cwd := cfg.CWDFunc()
		probeKey := t.Name() + "\x00" + input
		sid := tc.SessionID()

		// Session-cached approvals short-circuit before consulting the
		// rules: a per-tool grant ("allow all <tool> this session") wins
		// over any per-call grant.
		if cache.hasTool(sid, t.Name()) {
			logf("tool-grant hit sid=%q tool=%s", sid, t.Name())
			return nil, nil
		}
		if cache.has(sid, probeKey) {
			logf("cache hit sid=%q tool=%s", sid, t.Name())
			return nil, nil
		}

		decision, reason := cfg.Source.Snapshot().Check(t.Name(), input, cwd)
		switch decision {
		case DecisionDeny:
			logf("DENY sid=%q tool=%s reason=%q", sid, t.Name(), reason)
			return map[string]any{
				"output": fmt.Sprintf("[DENIED] %s: %s", t.Name(), reason),
			}, nil
		case DecisionAllow:
			logf("rule ALLOW sid=%q tool=%s reason=%q", sid, t.Name(), reason)
			return nil, nil
		case DecisionAsk:
			logf("ASK sid=%q tool=%s reason=%q", sid, t.Name(), reason)
			outcome := cfg.Asker.Ask(tc, t.Name(), input, reason)
			switch outcome {
			case OutcomeDeny:
				logf("user DENY sid=%q tool=%s", sid, t.Name())
				return map[string]any{
					"output": fmt.Sprintf("[REJECTED BY USER] %s", t.Name()),
				}, nil
			case OutcomeAllowOnce:
				cache.add(sid, probeKey)
				logf("user ALLOW-ONCE sid=%q tool=%s (cached; will NOT affect other sessions)", sid, t.Name())
				return nil, nil
			case OutcomeAllowToolSession:
				cache.addTool(sid, t.Name())
				logf("user ALLOW-TOOL-SESSION sid=%q tool=%s (every later %s call this session auto-allows; not persisted)", sid, t.Name(), t.Name())
				return nil, nil
			case OutcomeAllowProject:
				if err := persistApproval(cfg.UserConfigPath, t.Name(), input, cwd); err != nil {
					fmt.Fprintf(os.Stderr, "permissions: persist project rule: %v\n", err)
				} else if cfg.OnPersist != nil {
					cfg.OnPersist()
				}
				cache.add(sid, probeKey)
				logf("user ALLOW-PROJECT sid=%q tool=%s cwd=%s (persisted to %s; will affect every session in this directory tree)", sid, t.Name(), cwd, cfg.UserConfigPath)
				return nil, nil
			case OutcomeAllowAlways:
				if err := persistApproval(cfg.UserConfigPath, t.Name(), input, ""); err != nil {
					fmt.Fprintf(os.Stderr, "permissions: persist global rule: %v\n", err)
				} else if cfg.OnPersist != nil {
					cfg.OnPersist()
				}
				cache.add(sid, probeKey)
				logf("user ALLOW-ALWAYS sid=%q tool=%s (persisted to %s; will affect every session everywhere)", sid, t.Name(), cfg.UserConfigPath)
				return nil, nil
			}
		}
		return nil, nil
	}
	p, err := plugin.New(plugin.Config{
		Name:               cfg.Name,
		BeforeToolCallback: llmagent.BeforeToolCallback(cb),
	})
	if err != nil {
		return nil, nil, err
	}
	return p, cache, nil
}

func flattenArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(args); err != nil {
		return fmt.Sprintf("%v", args)
	}
	return strings.TrimRight(buf.String(), "\n")
}
