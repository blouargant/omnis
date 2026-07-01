package permissions

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Rule is one permission entry in the new (Claude Code-style) nomenclature.
//
// It is written in JSON as either:
//
//   - a bare string — the Claude-native form, e.g. "Bash(npm run *)",
//     "Read(.env)", "Edit", "mcp__puppeteer__*", "Agent(Explore)"; or
//   - an object {rule, reason, cwd} carrying omnis extensions: a human
//     readable reason for the prompt, and a cwd that scopes the rule to a
//     project tree (used by "Allow in this project" persisted grants —
//     Claude Code has no equivalent); or
//   - an object {regex, tools, reason, cwd} — the omnis regex escape hatch.
//     When `regex` is set the rule matches exactly like the legacy engine:
//     the compiled pattern is tested against the probe "toolName <json args>",
//     optionally scoped to `tools`. This is what ConvertLegacy emits so an
//     upgraded config behaves byte-for-byte like the old one.
//
// `rule` and `regex` are mutually exclusive in practice; when both are set
// `regex` wins.
type Rule struct {
	Rule   string   `json:"rule,omitempty"`
	Regex  string   `json:"regex,omitempty"`
	Reason string   `json:"reason,omitempty"`
	CWD    string   `json:"cwd,omitempty"`
	Tools  []string `json:"tools,omitempty"`

	spec *Spec          // parsed from Rule (nil for regex rules)
	re   *regexp.Regexp // compiled from Regex (nil for spec rules)
}

// ruleObj is the object shape used for (un)marshalling, kept separate so the
// custom Rule (Un)MarshalJSON can also accept/emit the bare-string form.
type ruleObj struct {
	Rule   string   `json:"rule,omitempty"`
	Regex  string   `json:"regex,omitempty"`
	Reason string   `json:"reason,omitempty"`
	CWD    string   `json:"cwd,omitempty"`
	Tools  []string `json:"tools,omitempty"`
}

// UnmarshalJSON accepts a bare string (the spec) or the object form.
func (r *Rule) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		r.Rule = s
		return nil
	}
	var o ruleObj
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	r.Rule = o.Rule
	r.Regex = o.Regex
	r.Reason = o.Reason
	r.CWD = o.CWD
	r.Tools = o.Tools
	return nil
}

// MarshalJSON emits the bare-string form when the rule carries nothing but a
// spec, and the object form otherwise (so reason/cwd/regex/tools round-trip).
func (r Rule) MarshalJSON() ([]byte, error) {
	if r.Regex == "" && r.Reason == "" && r.CWD == "" && len(r.Tools) == 0 {
		return json.Marshal(r.Rule)
	}
	return json.Marshal(ruleObj{
		Rule:   r.Rule,
		Regex:  r.Regex,
		Reason: r.Reason,
		CWD:    r.CWD,
		Tools:  r.Tools,
	})
}

// compile parses/compiles the rule so it is ready for matching. A regex rule
// compiles its pattern (case-insensitive, as the legacy engine did); a spec
// rule parses its Tool(specifier) string.
func (r *Rule) compile() error {
	if r.Regex != "" {
		re, err := regexp.Compile("(?i)" + r.Regex)
		if err != nil {
			return err
		}
		r.re = re
		return nil
	}
	sp := parseSpec(r.Rule)
	if err := sp.compile(); err != nil {
		return err
	}
	r.spec = &sp
	return nil
}

// Spec is a parsed Tool(specifier) permission entry.
type Spec struct {
	// Tool is the rule's tool class: "Bash", "Read", "Edit", "Write",
	// "WebFetch", "mcp", "Agent", or "" for a tool-less /regex/ form.
	Tool string
	// Arg is the specifier inside the parentheses (empty for a bare tool
	// name like "Bash"). For mcp rules Arg holds the full mcp__… string.
	Arg string
	// Bare is true for a tool name with no parentheses (matches every call
	// of that tool class).
	Bare bool
	// IsRegex marks the /regex/ string sugar (a tool-less or tool-scoped
	// raw pattern). The compiled form lives in re.
	IsRegex bool

	re   *regexp.Regexp // for IsRegex
	glob *regexp.Regexp // for Bash glob specs
}

// parseSpec parses a permission string into a Spec. Recognised shapes:
//
//	Bash(npm run *)     → {Tool:"Bash", Arg:"npm run *"}
//	Bash                → {Tool:"Bash", Bare:true}
//	Bash(*)             → {Tool:"Bash", Bare:true}
//	Read(.env)          → {Tool:"Read", Arg:".env"}
//	WebFetch(domain:x)  → {Tool:"WebFetch", Arg:"domain:x"}
//	mcp__srv__tool      → {Tool:"mcp", Arg:"mcp__srv__tool"}
//	Agent(Explore)      → {Tool:"Agent", Arg:"Explore"}
//	/regex/             → {Tool:"", IsRegex:true, Arg:"regex"}
func parseSpec(raw string) Spec {
	s := strings.TrimSpace(raw)
	// Tool-less raw regex sugar: /pattern/.
	if len(s) >= 2 && strings.HasPrefix(s, "/") && strings.HasSuffix(s, "/") {
		return Spec{IsRegex: true, Arg: s[1 : len(s)-1]}
	}
	// MCP rules have no parentheses; they are matched by prefix on the tool name.
	if strings.HasPrefix(s, "mcp__") {
		return Spec{Tool: "mcp", Arg: s}
	}
	// Tool(specifier).
	if i := strings.IndexByte(s, '('); i >= 0 && strings.HasSuffix(s, ")") {
		tool := strings.TrimSpace(s[:i])
		arg := s[i+1 : len(s)-1]
		if arg == "" || arg == "*" {
			return Spec{Tool: tool, Bare: true}
		}
		return Spec{Tool: tool, Arg: arg}
	}
	// Bare tool name.
	return Spec{Tool: s, Bare: true}
}

func (s *Spec) compile() error {
	if s.IsRegex {
		re, err := regexp.Compile("(?i)" + s.Arg)
		if err != nil {
			return err
		}
		s.re = re
		return nil
	}
	if s.Tool == "Bash" && !s.Bare && s.Arg != "" {
		re, err := compileBashGlob(s.Arg)
		if err != nil {
			return err
		}
		s.glob = re
	}
	return nil
}

// toolClasses returns the rule tool-class names that apply to a call of the
// given omnis tool, mirroring Claude Code's fan-out ("Edit rules apply to all
// edit tools; Read rules apply to Grep/Glob").
func toolClasses(omnisTool string) []string {
	switch omnisTool {
	case "Bash":
		return []string{"Bash"}
	case "Read", "Grep", "Glob", "mime",
		// Read-only language-server code intelligence — as safe as Read/Grep, so
		// an allowed Read rule covers them and they don't prompt.
		"lsp_document_symbols", "lsp_workspace_symbol", "lsp_definition",
		"lsp_references", "lsp_hover", "lsp_diagnostics":
		return []string{"Read"}
	case "Edit", "revert", "MultiEdit":
		return []string{"Edit"}
	case "lsp_rename":
		// A project-wide rename writes many files; gate it like any edit so
		// Edit allow/deny/ask rules and acceptEdits mode cover it.
		return []string{"Edit"}
	case "Write":
		return []string{"Write", "Edit"}
	default:
		if strings.HasPrefix(omnisTool, "mcp__") {
			return []string{"mcp"}
		}
		// Everything else (sub-agent tools, coordination tools) is matched by
		// the Agent class and by bare-name equality.
		return []string{"Agent"}
	}
}

// specApplies reports whether a spec's Tool class covers a call of omnisTool.
func specApplies(specTool, omnisTool string) bool {
	if specTool == "" { // tool-less regex sugar applies to everything
		return true
	}
	for _, c := range toolClasses(omnisTool) {
		if strings.EqualFold(c, specTool) {
			return true
		}
	}
	// Bare equality fallback: a rule named exactly after the tool (e.g.
	// "summariser" or an explicit "Grep") matches that tool directly.
	return strings.EqualFold(specTool, omnisTool)
}
