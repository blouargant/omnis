// Package permissions implements JSON rule-based permission governance
// (Phase 4 / s15). Three tiers are evaluated in order against every tool
// call: always_deny → always_allow → ask_user. Anything that matches no
// rule is implicitly allowed.
//
// The plugin returned by NewPlugin wires this into ADK as a
// BeforeToolCallback so denial happens before the tool runs.
package permissions

import (
	"bufio"
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
// (just the pattern) or as an object {pattern, reason}.
type Rule struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason,omitempty"`
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

// Check evaluates a tool call. The input is rendered as a flat string
// "toolName arg1 arg2 ..." for matching.
func (r *Rules) Check(toolName string, input string) (Decision, string) {
	probe := toolName + " " + input
	for _, rl := range r.AlwaysDeny {
		if rl.re != nil && rl.re.MatchString(probe) {
			return DecisionDeny, rl.Reason
		}
	}
	for _, rl := range r.AlwaysAllow {
		if rl.re != nil && rl.re.MatchString(probe) {
			return DecisionAllow, rl.Reason
		}
	}
	for _, rl := range r.AskUser {
		if rl.re != nil && rl.re.MatchString(probe) {
			return DecisionAsk, rl.Reason
		}
	}
	return DecisionAllow, ""
}

// Asker is the interface used to prompt the user when an ask_user rule
// fires. The default StdinAsker reads y/N from stdin.
type Asker interface {
	Ask(toolName, input, reason string) bool
}

// StdinAsker prompts on stderr (so it doesn't pollute stdout) and reads
// from stdin.
type StdinAsker struct{}

func (StdinAsker) Ask(toolName, input, reason string) bool {
	fmt.Fprintf(os.Stderr, "\n[PERMISSION] %s: %s\n  Reason: %s\n  Allow? [y/N] ", toolName, input, reason)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(sc.Text()))
	return t == "y" || t == "yes"
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

// NewPluginFromRules returns an ADK plugin from a pre-loaded (and possibly
// merged) Rules set. Use this instead of NewPlugin when rules come from
// multiple sources (e.g. base config + skill overlays).
func NewPluginFromRules(name string, rules *Rules, asker Asker) (*plugin.Plugin, error) {
	if asker == nil {
		asker = StdinAsker{}
	}
	cb := func(_ tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		input := flattenArgs(args)
		decision, reason := rules.Check(t.Name(), input)
		switch decision {
		case DecisionDeny:
			return map[string]any{
				"output": fmt.Sprintf("[DENIED] %s: %s", t.Name(), reason),
			}, nil
		case DecisionAsk:
			if !asker.Ask(t.Name(), input, reason) {
				return map[string]any{
					"output": fmt.Sprintf("[REJECTED BY USER] %s", t.Name()),
				}, nil
			}
		}
		return nil, nil
	}
	return plugin.New(plugin.Config{
		Name:               name,
		BeforeToolCallback: llmagent.BeforeToolCallback(cb),
	})
}

func flattenArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(b)
}
