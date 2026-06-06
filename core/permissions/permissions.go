// Package permissions implements JSON rule-based permission governance using
// Claude Code's permission nomenclature as yoke's default format. Rules are
// written as Tool(specifier) strings under permissions.{allow, ask, deny}:
//
//	{ "permissions": {
//	    "defaultMode": "default",
//	    "allow": ["Bash(npm run *)", "Read"],
//	    "ask":   ["Bash(git push *)"],
//	    "deny":  ["Bash(rm -rf /*)", "Read(.env)"]
//	} }
//
// Precedence is deny → ask → allow (deny always wins), matching Claude Code.
// A tool call that matches no rule falls through to the mode default (ask in
// default mode), so unrecognised commands require explicit confirmation.
//
// yoke extensions over Claude syntax: an object rule {rule, reason, cwd}
// attaches a prompt reason and a project-scoped cwd; the {regex, tools} object
// form is the raw-regexp escape hatch (matched against "toolName <json args>")
// that ConvertLegacy uses so an upgraded old config behaves identically.
//
// Old-format files (top-level always_deny/always_allow/ask_user) are detected
// and auto-converted on load.
package permissions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// Mode is a permission mode (Claude Code's defaultMode). It changes the
// behavior for tool calls that match no explicit rule, plus the absolute
// bypass / plan shortcuts.
type Mode int

const (
	ModeDefault Mode = iota
	ModeAcceptEdits
	ModePlan
	ModeAuto
	ModeDontAsk
	ModeBypass
)

var modeNames = map[Mode]string{
	ModeDefault:     "default",
	ModeAcceptEdits: "acceptEdits",
	ModePlan:        "plan",
	ModeAuto:        "auto",
	ModeDontAsk:     "dontAsk",
	ModeBypass:      "bypassPermissions",
}

// ParseMode maps a defaultMode string to a Mode (unknown ⇒ ModeDefault).
func ParseMode(s string) Mode {
	for m, name := range modeNames {
		if strings.EqualFold(name, s) {
			return m
		}
	}
	return ModeDefault
}

func (m Mode) String() string { return modeNames[m] }

func (m Mode) MarshalJSON() ([]byte, error) { return json.Marshal(m.String()) }

func (m *Mode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*m = ParseMode(s)
	return nil
}

// PermSet is the allow/ask/deny rule set plus mode + additional directories,
// mirroring Claude Code's "permissions" object.
type PermSet struct {
	DefaultMode           Mode     `json:"defaultMode,omitempty"`
	Allow                 []Rule   `json:"allow"`
	Ask                   []Rule   `json:"ask"`
	Deny                  []Rule   `json:"deny"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

// Config is the top-level permissions.json document.
type Config struct {
	Permissions PermSet `json:"permissions"`
}

// HasRules reports whether any tier contains at least one rule.
func (c *Config) HasRules() bool {
	p := &c.Permissions
	return len(p.Allow) > 0 || len(p.Ask) > 0 || len(p.Deny) > 0
}

// compile parses/compiles every rule. Invalid rules are skipped (dropped) so a
// single bad entry never disables the whole rule set.
func (c *Config) compile() {
	for _, set := range []*[]Rule{&c.Permissions.Allow, &c.Permissions.Ask, &c.Permissions.Deny} {
		out := make([]Rule, 0, len(*set))
		for i := range *set {
			r := (*set)[i]
			if err := r.compile(); err == nil {
				out = append(out, r)
			}
		}
		*set = out
	}
}

// Merge returns a new Config combining base with each overlay appended in
// order (base rules first within each tier). DefaultMode/AdditionalDirectories
// come from base unless empty, in which case the first overlay that sets them
// wins.
func Merge(base *Config, overlays ...*Config) *Config {
	merged := &Config{Permissions: PermSet{
		DefaultMode:           base.Permissions.DefaultMode,
		AdditionalDirectories: append([]string(nil), base.Permissions.AdditionalDirectories...),
		Allow:                 append([]Rule(nil), base.Permissions.Allow...),
		Ask:                   append([]Rule(nil), base.Permissions.Ask...),
		Deny:                  append([]Rule(nil), base.Permissions.Deny...),
	}}
	for _, o := range overlays {
		if o == nil {
			continue
		}
		merged.Permissions.Allow = append(merged.Permissions.Allow, o.Permissions.Allow...)
		merged.Permissions.Ask = append(merged.Permissions.Ask, o.Permissions.Ask...)
		merged.Permissions.Deny = append(merged.Permissions.Deny, o.Permissions.Deny...)
		merged.Permissions.AdditionalDirectories = append(merged.Permissions.AdditionalDirectories, o.Permissions.AdditionalDirectories...)
		if merged.Permissions.DefaultMode == ModeDefault {
			merged.Permissions.DefaultMode = o.Permissions.DefaultMode
		}
	}
	return merged
}

// Load reads, converts (if old-format), and compiles a permissions config.
// A missing file yields an empty config (everything falls through to ask).
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.compile()
	return cfg, nil
}

// parseConfig parses bytes into a Config, accepting three shapes: the nested
// {permissions:{…}}, a bare permissions object {allow,ask,deny,…}, and the old
// {always_deny,…} format (auto-converted).
func parseConfig(data []byte) (*Config, error) {
	if isLegacyFormat(data) {
		legacy, err := parseLegacy(data)
		if err != nil {
			return nil, err
		}
		cfg, _ := ConvertLegacy(legacy)
		return cfg, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if _, ok := probe["permissions"]; ok {
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		return &cfg, nil
	}
	// Bare permissions object.
	var ps PermSet
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, err
	}
	return &Config{Permissions: ps}, nil
}

// matchCtx carries the resolved fields a single Check consults.
type matchCtx struct {
	toolName    string
	probe       string // "toolName <json args>" (legacy regex probe)
	command     string // Bash command arg
	filePath    string // file_path arg
	url         string // url arg (WebFetch)
	cwd         string
	projectRoot string
	home        string
}

// Check evaluates a tool call against the rule set. input is the JSON-marshaled
// tool args (legacy compatibility); CheckArgs is preferred where the structured
// args map is available.
func (c *Config) Check(toolName, input, cwd string) (Decision, string) {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(input), &args)
	return c.CheckArgs(toolName, args, cwd)
}

// CheckArgs evaluates a tool call against the rule set with structured args.
func (c *Config) CheckArgs(toolName string, args map[string]any, cwd string) (Decision, string) {
	ps := &c.Permissions
	ctx := matchCtx{
		toolName:    toolName,
		probe:       toolName + " " + flattenArgs(args),
		command:     stringArg(args, "command"),
		filePath:    stringArg(args, "file_path"),
		url:         stringArg(args, "url"),
		cwd:         cwd,
		projectRoot: findProjectRoot(cwd),
		home:        userHome(),
	}
	mode := ps.DefaultMode

	// 1. Explicit deny always wins.
	if rl, ok := firstMatch(ps.Deny, ctx); ok {
		return DecisionDeny, reasonOr(rl, "denied by rule")
	}
	// 2. bypassPermissions: allow everything else (the hard safety floor in
	//    core/tools/bash.go remains the circuit breaker).
	if mode == ModeBypass {
		return DecisionAllow, "bypassPermissions mode"
	}
	// 3. plan mode is authoritative: reads allowed, edits / non-read-only
	//    shell denied.
	if mode == ModePlan {
		switch {
		case isReadTool(toolName):
			return DecisionAllow, "plan mode (read-only)"
		case isEditTool(toolName):
			return DecisionDeny, "plan mode forbids edits"
		case toolName == "Bash" && ctx.command != "" && !allReadOnly(ctx.command):
			return DecisionDeny, "plan mode forbids non-read-only commands"
		}
	}
	// 4. Explicit ask.
	if rl, ok := firstMatch(ps.Ask, ctx); ok {
		return DecisionAsk, reasonOr(rl, defaultAskReason)
	}
	// 5. Built-in read-only Bash commands run without prompting (overridable
	//    by deny/ask above).
	if toolName == "Bash" && ctx.command != "" && allReadOnly(ctx.command) {
		return DecisionAllow, "read-only command"
	}
	// 6. acceptEdits auto-allows edits in the working dir + common fs commands.
	if mode == ModeAcceptEdits {
		if isEditTool(toolName) && underWorkingDir(ctx.filePath, cwd, ps) {
			return DecisionAllow, "acceptEdits mode"
		}
		if toolName == "Bash" && ctx.command != "" && isCommonFsCommand(ctx.command) {
			return DecisionAllow, "acceptEdits mode"
		}
	}
	// 7. Explicit allow.
	if toolName == "Bash" && ctx.command != "" {
		if bashAllowed(ps.Allow, ctx) {
			return DecisionAllow, "allowed by rule"
		}
	} else if rl, ok := firstMatch(ps.Allow, ctx); ok {
		return DecisionAllow, reasonOr(rl, "allowed by rule")
	}
	// 8. Mode-based default.
	if mode == ModeDontAsk {
		return DecisionDeny, "dontAsk mode: tool not pre-approved"
	}
	return DecisionAsk, defaultAskReason
}

const defaultAskReason = "command is not on the allow list — confirm before running"

func reasonOr(r *Rule, fallback string) string {
	if r != nil && r.Reason != "" {
		return r.Reason
	}
	return fallback
}

// firstMatch returns the first rule in the tier that matches the call. Bash
// specs match if ANY subcommand matches (so a deny/ask on a compound command
// fires when any part triggers it).
func firstMatch(rules []Rule, ctx matchCtx) (*Rule, bool) {
	for i := range rules {
		if ruleMatches(&rules[i], ctx) {
			return &rules[i], true
		}
	}
	return nil, false
}

// ruleMatches reports whether a single rule matches the call.
func ruleMatches(r *Rule, ctx matchCtx) bool {
	if r.CWD != "" && !cwdMatches(r.CWD, ctx.cwd) {
		return false
	}
	// Regex escape hatch (legacy semantics): match the whole probe, scoped to
	// the optional tools list.
	if r.re != nil {
		return matchesToolList(r.Tools, ctx.toolName) && r.re.MatchString(ctx.probe)
	}
	sp := r.spec
	if sp == nil {
		return false
	}
	if sp.IsRegex {
		return sp.re.MatchString(ctx.probe)
	}
	if !specApplies(sp.Tool, ctx.toolName) {
		return false
	}
	switch sp.Tool {
	case "Bash":
		return bashSpecMatchesAny(sp, ctx.command)
	case "Read", "Edit", "Write":
		return sp.pathMatch(ctx.filePath, ctx.cwd, ctx.projectRoot, ctx.home)
	case "mcp":
		return mcpMatch(sp.Arg, ctx.toolName)
	case "Agent":
		if sp.Bare {
			return true
		}
		return agentMatch(sp.Arg, ctx.toolName)
	case "WebFetch":
		if sp.Bare {
			return true
		}
		return domainMatch(sp.Arg, ctx.url)
	default:
		// Bare tool name whose class covers this call.
		return sp.Bare
	}
}

// bashSpecMatchesAny reports whether a Bash spec matches any subcommand.
func bashSpecMatchesAny(sp *Spec, command string) bool {
	if sp.Bare {
		return true
	}
	for _, sub := range bashSubcommands(command) {
		if sp.bashGlobMatch(sub) {
			return true
		}
	}
	return false
}

// bashAllowed reports whether every subcommand of a compound Bash command is
// covered by the allow tier. Legacy whole-probe regex allow rules short-circuit
// (preserving old behavior); native glob specs are matched per-subcommand, and
// built-in read-only commands count as covered.
func bashAllowed(allow []Rule, ctx matchCtx) bool {
	for i := range allow {
		r := &allow[i]
		if r.CWD != "" && !cwdMatches(r.CWD, ctx.cwd) {
			continue
		}
		if r.re != nil && matchesToolList(r.Tools, "Bash") && r.re.MatchString(ctx.probe) {
			return true
		}
		if r.spec != nil && r.spec.IsRegex && r.spec.re.MatchString(ctx.probe) {
			return true
		}
	}
	subs := bashSubcommands(ctx.command)
	for _, sub := range subs {
		if isReadOnlyCommand(sub) {
			continue
		}
		covered := false
		for i := range allow {
			r := &allow[i]
			if r.spec == nil || r.spec.Tool != "Bash" {
				continue
			}
			if r.CWD != "" && !cwdMatches(r.CWD, ctx.cwd) {
				continue
			}
			if r.spec.bashGlobMatch(sub) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// allReadOnly reports whether every subcommand of a (possibly compound) command
// is a recognised read-only command.
func allReadOnly(command string) bool {
	subs := bashSubcommands(command)
	if len(subs) == 0 {
		return false
	}
	for _, sub := range subs {
		if !isReadOnlyCommand(sub) {
			return false
		}
	}
	return true
}

func isReadTool(t string) bool {
	switch t {
	case "Read", "Grep", "Glob", "mime":
		return true
	}
	return false
}

func isEditTool(t string) bool {
	switch t {
	case "Edit", "Write", "revert":
		return true
	}
	return false
}

// commonFsCommands are the filesystem commands acceptEdits mode auto-allows.
var commonFsCommands = map[string]bool{
	"mkdir": true, "touch": true, "mv": true, "cp": true, "rmdir": true, "ln": true,
}

func isCommonFsCommand(command string) bool {
	subs := bashSubcommands(command)
	if len(subs) == 0 {
		return false
	}
	for _, sub := range subs {
		f := strings.Fields(sub)
		if len(f) == 0 || !commonFsCommands[f[0]] {
			return false
		}
	}
	return true
}

// underWorkingDir reports whether an edit's target path is inside the session
// cwd or one of the configured additional directories.
func underWorkingDir(filePath, cwd string, ps *PermSet) bool {
	if filePath == "" {
		return true // no path arg (e.g. some edit tools) — trust the mode
	}
	abs := resolveAbsPath(filePath, cwd)
	if cwdMatches(cwd, abs) {
		return true
	}
	for _, d := range ps.AdditionalDirectories {
		if cwdMatches(d, abs) {
			return true
		}
	}
	return false
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// matchesToolList reports whether a tools scope (legacy regex rules) applies to
// toolName. An empty list applies to every tool.
func matchesToolList(tools []string, toolName string) bool {
	if len(tools) == 0 {
		return true
	}
	for _, t := range tools {
		if strings.EqualFold(t, toolName) {
			return true
		}
	}
	return false
}

// cwdMatches reports whether candidate is scope or a sub-directory of scope.
func cwdMatches(scope, candidate string) bool {
	if scope == "" || candidate == "" {
		return false
	}
	scope = strings.TrimRight(filepath.Clean(scope), "/")
	candidate = strings.TrimRight(filepath.Clean(candidate), "/")
	if scope == candidate {
		return true
	}
	return strings.HasPrefix(candidate, scope+"/")
}

var (
	homeOnce      sync.Once
	homeDir       string
	projRootCache sync.Map // cwd → project root
)

func userHome() string {
	homeOnce.Do(func() { homeDir, _ = os.UserHomeDir() })
	return homeDir
}

// findProjectRoot walks up from cwd to the nearest directory containing a .git
// (dir or file, for worktrees), falling back to cwd. Cached per cwd.
func findProjectRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	if v, ok := projRootCache.Load(cwd); ok {
		return v.(string)
	}
	root, d := "", cwd
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			root = d
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if root == "" {
		root = cwd
	}
	projRootCache.Store(cwd, root)
	return root
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

// AskOutcome is what an Asker returns after prompting the user. It drives both
// the immediate decision and any persistence side effects.
type AskOutcome int

const (
	// OutcomeDeny rejects this call; the next identical call asks again.
	OutcomeDeny AskOutcome = iota
	// OutcomeAllowOnce permits this call only (session-cached, not persisted).
	OutcomeAllowOnce
	// OutcomeAllowToolSession permits every later call of the same tool this
	// session (in-memory only).
	OutcomeAllowToolSession
	// OutcomeAllowProject persists a cwd-scoped allow rule.
	OutcomeAllowProject
	// OutcomeAllowAlways persists an unscoped allow rule.
	OutcomeAllowAlways
)

// Asker prompts the user when an ask rule fires.
type Asker interface {
	Ask(tc tool.Context, toolName, input, reason string) AskOutcome
}

// StdinAsker prompts on stderr and reads from stdin (CLI fallback).
type StdinAsker struct{}

func (StdinAsker) Ask(_ tool.Context, toolName, input, reason string) AskOutcome {
	fmt.Fprintf(os.Stderr,
		"\n[PERMISSION] %s: %s\n  Reason: %s\n"+
			"  [o] allow once (default, press Enter), [s] allow all %s this session, [p] allow in project, [a] allow always, [d] deny: ",
		toolName, input, reason, toolName)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return OutcomeDeny
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "", "o", "once", "y", "yes":
		return OutcomeAllowOnce
	case "s", "session", "tool":
		return OutcomeAllowToolSession
	case "p", "project":
		return OutcomeAllowProject
	case "a", "always", "all":
		return OutcomeAllowAlways
	case "d", "deny", "n", "no":
		return OutcomeDeny
	default:
		return OutcomeDeny
	}
}

// Source provides the active config. Implementations may swap atomically when
// an underlying file changes (see Reloader).
type Source interface {
	Snapshot() *Config
}

// Static wraps a fixed *Config; used in tests and when no live reload is needed.
type Static struct{ C *Config }

func (s *Static) Snapshot() *Config { return s.C }

// PluginConfig wires the permissions plugin.
type PluginConfig struct {
	Name           string
	Source         Source
	Asker          Asker
	UserConfigPath string
	CWDFunc        func(tc tool.Context) string
	OnPersist      func()
	Debug          bool
}

// SessionCleaner exposes the internal session-approval cache so the caller can
// wipe a session's cached approvals on session end.
type SessionCleaner interface {
	Forget(sessionID string)
}

// NewPlugin returns an ADK plugin enforcing the rules in configPath (missing
// file ⇒ no-op).
func NewPlugin(name, configPath string, asker Asker) (*plugin.Plugin, error) {
	cfg, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	return NewPluginFromRules(name, cfg, asker)
}

// NewPluginFromRules returns an ADK plugin from a fixed Config (no session
// cache, no persistence). For the full feature set use NewPluginFromConfig.
func NewPluginFromRules(name string, cfg *Config, asker Asker) (*plugin.Plugin, error) {
	p, _, err := NewPluginFromConfig(PluginConfig{
		Name:   name,
		Source: &Static{C: cfg},
		Asker:  asker,
	})
	return p, err
}

// NewPluginFromConfig wires the permissions plugin with live-reloadable rules,
// a session-scoped approval cache, and the AllowProject/AllowAlways persistence
// path.
func NewPluginFromConfig(cfg PluginConfig) (*plugin.Plugin, SessionCleaner, error) {
	if cfg.Source == nil {
		return nil, nil, fmt.Errorf("permissions: PluginConfig.Source is required")
	}
	if cfg.Asker == nil {
		cfg.Asker = StdinAsker{}
	}
	if cfg.CWDFunc == nil {
		cfg.CWDFunc = func(tool.Context) string {
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
		cwd := cfg.CWDFunc(tc)
		probeKey := t.Name() + "\x00" + input
		sid := tc.SessionID()

		if cache.hasTool(sid, t.Name()) {
			logf("tool-grant hit sid=%q tool=%s", sid, t.Name())
			return nil, nil
		}
		if cache.has(sid, probeKey) {
			logf("cache hit sid=%q tool=%s", sid, t.Name())
			return nil, nil
		}

		decision, reason := cfg.Source.Snapshot().CheckArgs(t.Name(), args, cwd)
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
				logf("user ALLOW-ONCE sid=%q tool=%s", sid, t.Name())
				return nil, nil
			case OutcomeAllowToolSession:
				cache.addTool(sid, t.Name())
				logf("user ALLOW-TOOL-SESSION sid=%q tool=%s", sid, t.Name())
				return nil, nil
			case OutcomeAllowProject:
				if err := persistApproval(cfg.UserConfigPath, t.Name(), input, cwd); err != nil {
					fmt.Fprintf(os.Stderr, "permissions: persist project rule: %v\n", err)
				} else if cfg.OnPersist != nil {
					cfg.OnPersist()
				}
				cache.add(sid, probeKey)
				logf("user ALLOW-PROJECT sid=%q tool=%s cwd=%s", sid, t.Name(), cwd)
				return nil, nil
			case OutcomeAllowAlways:
				if err := persistApproval(cfg.UserConfigPath, t.Name(), input, ""); err != nil {
					fmt.Fprintf(os.Stderr, "permissions: persist global rule: %v\n", err)
				} else if cfg.OnPersist != nil {
					cfg.OnPersist()
				}
				cache.add(sid, probeKey)
				logf("user ALLOW-ALWAYS sid=%q tool=%s", sid, t.Name())
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
