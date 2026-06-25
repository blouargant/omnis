package permissions

import (
	"fmt"
	"strings"
)

// ParseBytes parses (auto-converting old-format) permissions JSON bytes into a
// compiled Config. Used by consumers that hold raw bytes (e.g. remote registry
// installs) rather than a file path.
func ParseBytes(data []byte) (*Config, error) {
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	cfg.compile()
	return cfg, nil
}

// RuleKey returns a canonical identity string for a rule, used for de-duplication
// and "already installed" checks.
func RuleKey(r Rule) string {
	if r.Regex != "" {
		return "regex|" + strings.Join(r.Tools, ",") + "|" + r.Regex + "|" + r.CWD
	}
	return "rule|" + r.Rule + "|" + r.CWD
}

// ConvertLegacy upgrades an old-format rule set (regex tiers) to the new
// Config. Each old rule becomes a regex-escape-hatch Rule {regex, tools, reason,
// cwd}, so the converted config matches byte-for-byte like the old engine (the
// regex is still tested against "toolName <json args>", with the same tool/cwd
// scoping). Returns the config and any conversion notes.
func ConvertLegacy(legacy *legacyRules) (*Config, []string) {
	if legacy == nil {
		return &Config{}, nil
	}
	conv := func(in []legacyRule) []Rule {
		out := make([]Rule, 0, len(in))
		for _, r := range in {
			out = append(out, Rule{
				Regex:  r.Pattern,
				Reason: r.Reason,
				CWD:    r.CWD,
				Tools:  r.Tools,
			})
		}
		return out
	}
	cfg := &Config{Permissions: PermSet{
		DefaultMode: ModeDefault,
		Deny:        conv(legacy.AlwaysDeny),
		Allow:       conv(legacy.AlwaysAllow),
		Ask:         conv(legacy.AskUser),
	}}
	return cfg, nil
}

// ImportClaudeSettings parses a Claude Code settings.json (or a bare
// permissions object, or a omnis Config) into a Config, returning warnings for
// rules that don't map onto a omnis tool. Tool names are kept verbatim — omnis's
// tool-class fan-out (toolClasses) maps Claude's Read/Edit/Write onto omnis's
// concrete tools at match time.
func ImportClaudeSettings(data []byte) (*Config, []string, error) {
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, nil, err
	}
	cfg.compile()

	var warnings []string
	seenWebFetch := false
	check := func(tier string, rules []Rule) {
		for i := range rules {
			sp := rules[i].spec
			if sp == nil || sp.IsRegex {
				continue
			}
			if sp.Tool == "WebFetch" && !seenWebFetch {
				seenWebFetch = true
				warnings = append(warnings, "WebFetch rules are parsed but inert — omnis has no gated WebFetch tool (web fetch runs inside the web_agent sub-agent).")
			}
		}
	}
	check("deny", cfg.Permissions.Deny)
	check("ask", cfg.Permissions.Ask)
	check("allow", cfg.Permissions.Allow)

	if cfg.Permissions.DefaultMode == ModeAuto {
		warnings = append(warnings, fmt.Sprintf("defaultMode %q is treated like %q (no background classifier).", "auto", "default"))
	}
	return cfg, warnings, nil
}
