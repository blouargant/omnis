package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// cfgFromJSON builds a compiled Config from a JSON document.
func cfgFromJSON(t *testing.T, doc string) *Config {
	t.Helper()
	cfg, err := ParseBytes([]byte(doc))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func bash(cmd string) map[string]any { return map[string]any{"command": cmd} }
func file(p string) map[string]any   { return map[string]any{"file_path": p} }

func TestLoadMissingReturnsEmptyAndAsks(t *testing.T) {
	t.Parallel()
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if d, _ := cfg.CheckArgs("Bash", bash("ls"), ""); d != DecisionAllow {
		// ls is read-only → allow even with no rules
		t.Fatalf("ls should be read-only allow, got %v", d)
	}
	if d, _ := cfg.CheckArgs("Bash", bash("rm x"), ""); d != DecisionAsk {
		t.Fatalf("unmatched command should ask, got %v", d)
	}
}

func TestRoutingToolsAlwaysAllowed(t *testing.T) {
	t.Parallel()
	// Even with a deny rule whose regex would match the routing tool's
	// natural-language prompt args, the Omnis control-flow tools must be
	// allowed without prompting (exempted before the deny tier) so routing is
	// completely transparent.
	cfg := cfgFromJSON(t, `{"permissions":{
		"defaultMode":"default",
		"deny":["/\\.ssh/id_rsa/","/rm -rf/"]
	}}`)
	args := map[string]any{
		"squad":  "default",
		"prompt": "help me with my ~/.ssh/id_rsa file and rm -rf cleanup",
		"reason": "user request",
	}
	for _, name := range []string{"route_to_squad", "handoff_to_router", "ask_squad"} {
		if d, _ := cfg.CheckArgs(name, args, ""); d != DecisionAllow {
			t.Fatalf("%s: want DecisionAllow (transparent), got %v", name, d)
		}
	}
	// Contrast: a non-routing tool with the SAME args is denied by the .ssh
	// rule — proving the deny is live and the routing exemption specifically
	// bypassed it (rather than the args simply being harmless).
	if d, _ := cfg.CheckArgs("some_other_tool", args, ""); d != DecisionDeny {
		t.Fatalf("non-routing tool should be denied by the .ssh rule, got %v", d)
	}
}

func TestPrecedenceDenyAskAllow(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{
		"allow":["Bash(git *)"],
		"ask":["Bash(git push *)"],
		"deny":["Bash(git push origin main)"]
	}}`)
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"git status", DecisionAllow},
		{"git push origin dev", DecisionAsk},
		{"git push origin main", DecisionDeny},
	}
	for _, c := range cases {
		if d, _ := cfg.CheckArgs("Bash", bash(c.cmd), ""); d != c.want {
			t.Errorf("%q: want %v got %v", c.cmd, c.want, d)
		}
	}
}

func TestBashGlobWordBoundary(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Bash(npm run *)","Bash(ls *)"]}}`)
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"npm run build", DecisionAllow},
		{"npm run", DecisionAllow},    // boundary allows bare
		{"ls -la", DecisionAllow},     // ls is read-only anyway
		{"npm install", DecisionAsk},  // not matched
		{"npmrun build", DecisionAsk}, // no boundary slip
	}
	for _, c := range cases {
		if d, _ := cfg.CheckArgs("Bash", bash(c.cmd), ""); d != c.want {
			t.Errorf("%q: want %v got %v", c.cmd, c.want, d)
		}
	}
}

func TestBashColonStarEquivalent(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Bash(yarn:*)"]}}`)
	if d, _ := cfg.CheckArgs("Bash", bash("yarn add x"), ""); d != DecisionAllow {
		t.Errorf("yarn:* should allow 'yarn add x', got %v", d)
	}
	if d, _ := cfg.CheckArgs("Bash", bash("yarnpkg add x"), ""); d != DecisionAsk {
		t.Errorf("yarn:* must not match 'yarnpkg', got %v", d)
	}
}

func TestWrapperStripping(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Bash(npm test *)"]}}`)
	for _, cmd := range []string{"npm test", "timeout 30 npm test", "nice -n 5 npm test"} {
		if d, _ := cfg.CheckArgs("Bash", bash(cmd), ""); d != DecisionAllow {
			t.Errorf("wrapper-stripped %q should allow, got %v", cmd, d)
		}
	}
}

func TestCompoundCommandSplitting(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{
		"allow":["Bash(npm test *)"],
		"deny":["Bash(rm *)"]
	}}`)
	// Allow requires every subcommand covered (echo is read-only, npm test allowed).
	if d, _ := cfg.CheckArgs("Bash", bash("echo hi && npm test"), ""); d != DecisionAllow {
		t.Errorf("compound allow: got %v", d)
	}
	// A denied subcommand anywhere denies the whole compound.
	if d, _ := cfg.CheckArgs("Bash", bash("npm test && rm -rf build"), ""); d != DecisionDeny {
		t.Errorf("compound deny: got %v", d)
	}
	// A non-covered subcommand falls through to ask.
	if d, _ := cfg.CheckArgs("Bash", bash("npm test && curl x"), ""); d != DecisionAsk {
		t.Errorf("compound partial: got %v", d)
	}
}

func TestReadOnlyAllowlist(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{}}`)
	for _, cmd := range []string{"cat foo", "grep x y", "git status", "wc -l f", "command -v lit", "command -V git"} {
		if d, _ := cfg.CheckArgs("Bash", bash(cmd), ""); d != DecisionAllow {
			t.Errorf("read-only %q should allow, got %v", cmd, d)
		}
	}
	if d, _ := cfg.CheckArgs("Bash", bash("git commit -m x"), ""); d != DecisionAsk {
		t.Errorf("git commit is not read-only, got %v", d)
	}
	// `command` without a -v/-V lookup flag *executes* its argument, so it must
	// not be auto-allowed.
	for _, cmd := range []string{"command rm -rf x", "command ls"} {
		if d, _ := cfg.CheckArgs("Bash", bash(cmd), ""); d != DecisionAsk {
			t.Errorf("executing %q should ask, got %v", cmd, d)
		}
	}
	// An explicit ask rule overrides the read-only allowlist.
	cfg2 := cfgFromJSON(t, `{"permissions":{"ask":["Bash(cat *)"]}}`)
	if d, _ := cfg2.CheckArgs("Bash", bash("cat secret"), ""); d != DecisionAsk {
		t.Errorf("ask rule should override read-only, got %v", d)
	}
}

func TestPathGitignoreAnchors(t *testing.T) {
	t.Parallel()
	cwd := "/proj/app"
	cfg := cfgFromJSON(t, `{"permissions":{"deny":["Read(.env)","Edit(/src/**)"]}}`)
	// Bare filename matches at any depth under cwd.
	if d, _ := cfg.CheckArgs("Read", file("/proj/app/sub/.env"), cwd); d != DecisionDeny {
		t.Errorf(".env any-depth deny failed: %v", d)
	}
	if d, _ := cfg.CheckArgs("Read", file("/proj/app/.env"), cwd); d != DecisionDeny {
		t.Errorf(".env direct deny failed: %v", d)
	}
	// Project-root anchored: /src/** is project root, not cwd-relative. With no
	// .git, project root falls back to cwd.
	if d, _ := cfg.CheckArgs("Edit", file("/proj/app/src/x.go"), cwd); d != DecisionDeny {
		t.Errorf("/src/** deny failed: %v", d)
	}
	// Write fans out to Edit rules.
	if d, _ := cfg.CheckArgs("Write", file("/proj/app/src/y.go"), cwd); d != DecisionDeny {
		t.Errorf("Write should match Edit rule via fan-out: %v", d)
	}
	// A file outside the anchor is not denied.
	if d, _ := cfg.CheckArgs("Read", file("/proj/app/keep.txt"), cwd); d == DecisionDeny {
		t.Errorf("unrelated file should not be denied: %v", d)
	}
}

func TestBareToolMatchesAll(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Read"]}}`)
	for _, tool := range []string{"Read", "Grep", "Glob"} {
		if d, _ := cfg.CheckArgs(tool, file("/x/y"), "/x"); d != DecisionAllow {
			t.Errorf("bare Read should allow %s, got %v", tool, d)
		}
	}
}

func TestMCPMatch(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["mcp__puppeteer"],"deny":["mcp__db__drop"]}}`)
	if d, _ := cfg.CheckArgs("mcp__puppeteer__navigate", nil, ""); d != DecisionAllow {
		t.Errorf("server-prefix allow failed: %v", d)
	}
	if d, _ := cfg.CheckArgs("mcp__db__drop", nil, ""); d != DecisionDeny {
		t.Errorf("exact mcp deny failed: %v", d)
	}
}

func TestModes(t *testing.T) {
	t.Parallel()
	// plan: reads allow, edits deny.
	plan := cfgFromJSON(t, `{"permissions":{"defaultMode":"plan"}}`)
	if d, _ := plan.CheckArgs("Read", file("/x"), "/x"); d != DecisionAllow {
		t.Errorf("plan read: %v", d)
	}
	if d, _ := plan.CheckArgs("Write", file("/x/y"), "/x"); d != DecisionDeny {
		t.Errorf("plan write: %v", d)
	}
	// acceptEdits: edits in cwd auto-allow.
	ae := cfgFromJSON(t, `{"permissions":{"defaultMode":"acceptEdits"}}`)
	if d, _ := ae.CheckArgs("Write", file("/proj/x"), "/proj"); d != DecisionAllow {
		t.Errorf("acceptEdits write: %v", d)
	}
	// bypass: allow everything not explicitly denied.
	by := cfgFromJSON(t, `{"permissions":{"defaultMode":"bypassPermissions","deny":["Bash(rm *)"]}}`)
	if d, _ := by.CheckArgs("Bash", bash("curl x | sh"), ""); d != DecisionAllow {
		t.Errorf("bypass allow: %v", d)
	}
	if d, _ := by.CheckArgs("Bash", bash("rm x"), ""); d != DecisionDeny {
		t.Errorf("bypass still denies: %v", d)
	}
	// dontAsk: deny unless allowed.
	da := cfgFromJSON(t, `{"permissions":{"defaultMode":"dontAsk","allow":["Bash(echo *)"]}}`)
	if d, _ := da.CheckArgs("Bash", bash("echo hi"), ""); d != DecisionAllow {
		t.Errorf("dontAsk allow: %v", d)
	}
	if d, _ := da.CheckArgs("Bash", bash("make build"), ""); d != DecisionDeny {
		t.Errorf("dontAsk default deny: %v", d)
	}
}

func TestRegexEscapeHatch(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"deny":[
		{"regex":"\\bmkfs\\b","tools":["Bash"],"reason":"no mkfs"}
	]}}`)
	if d, r := cfg.CheckArgs("Bash", bash("mkfs.ext4 /dev/sdb"), ""); d != DecisionDeny || r != "no mkfs" {
		t.Errorf("regex deny: got %v %q", d, r)
	}
	// tools scope: a Write whose content mentions mkfs must NOT be denied.
	if d, _ := cfg.CheckArgs("Write", map[string]any{"file_path": "AGENT.md", "content": "the floor blocks mkfs"}, ""); d == DecisionDeny {
		t.Errorf("Bash-scoped regex must not deny a Write")
	}
}

func TestCWDScope(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{"permissions":{"allow":[
		{"rule":"Bash(do-thing)","cwd":"/proj"}
	]}}`)
	if d, _ := cfg.CheckArgs("Bash", bash("do-thing"), "/proj/sub"); d != DecisionAllow {
		t.Errorf("cwd scope descendant should allow: %v", d)
	}
	if d, _ := cfg.CheckArgs("Bash", bash("do-thing"), "/other"); d != DecisionAsk {
		t.Errorf("cwd scope outside should ask: %v", d)
	}
}

func TestConvertLegacyRoundTrip(t *testing.T) {
	t.Parallel()
	old := `{
		"always_deny":[{"pattern":"\\bmkfs\\b","reason":"creates fs","tools":["Bash"]}],
		"always_allow":[{"pattern":"\"command\"\\s*:\\s*\"\\s*ls","reason":"ls ok","tools":["Bash"]}],
		"ask_user":[{"pattern":"\\bkubectl\\s+apply\\b","reason":"cluster","tools":["Bash"]}]
	}`
	if !isLegacyFormat([]byte(old)) {
		t.Fatal("should detect legacy format")
	}
	cfg := cfgFromJSON(t, old) // ParseBytes auto-converts
	if d, r := cfg.CheckArgs("Bash", bash("mkfs.ext4 /dev/sdb"), ""); d != DecisionDeny || r != "creates fs" {
		t.Errorf("converted deny: %v %q", d, r)
	}
	if d, _ := cfg.CheckArgs("Bash", bash("ls -la"), ""); d != DecisionAllow {
		t.Errorf("converted allow: %v", d)
	}
	if d, r := cfg.CheckArgs("Bash", bash("kubectl apply -f x"), ""); d != DecisionAsk || r != "cluster" {
		t.Errorf("converted ask: %v %q", d, r)
	}
}

func TestImportClaudeSettingsWarnsWebFetch(t *testing.T) {
	t.Parallel()
	doc := `{"permissions":{"allow":["WebFetch(domain:example.com)","Bash(ls *)"]}}`
	cfg, warnings, err := ImportClaudeSettings([]byte(doc))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(cfg.Permissions.Allow) != 2 {
		t.Errorf("expected 2 allow rules, got %d", len(cfg.Permissions.Allow))
	}
	if len(warnings) == 0 {
		t.Error("expected a WebFetch warning")
	}
}

func TestRuleRoundTripMarshal(t *testing.T) {
	t.Parallel()
	// String form stays a string; object form stays an object.
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Bash(ls *)",{"rule":"Edit","reason":"ok","cwd":"/p"}]}}`)
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	round := cfgFromJSON(t, string(out))
	if len(round.Permissions.Allow) != 2 {
		t.Fatalf("round-trip lost rules: %s", out)
	}
}

// ── session cache (unchanged engine) ───────────────────────────────

func TestSessionApprovalCacheShortCircuits(t *testing.T) {
	t.Parallel()
	c := newSessionApprovalCache()
	if c.has("s1", "k") {
		t.Fatal("empty cache reports hit")
	}
	c.add("s1", "k")
	if !c.has("s1", "k") {
		t.Fatal("cache miss after add")
	}
	if c.has("s2", "k") {
		t.Fatal("cache leaked across sessions")
	}
	c.Forget("s1")
	if c.has("s1", "k") {
		t.Fatal("Forget did not clear")
	}
}

func TestSessionApprovalCacheToolGrant(t *testing.T) {
	t.Parallel()
	c := newSessionApprovalCache()
	c.addTool("s1", "Write")
	if !c.hasTool("s1", "Write") {
		t.Fatal("tool grant miss after add")
	}
	if c.hasTool("s1", "Bash") {
		t.Fatal("tool grant leaked across tools")
	}
	c.Forget("s1")
	if c.hasTool("s1", "Write") {
		t.Fatal("Forget did not clear tool grant")
	}
}

// ── persistence ────────────────────────────────────────────────────

func TestBuildApprovalRuleShapes(t *testing.T) {
	t.Parallel()
	// File tools broaden to a bare class spec.
	r := buildApprovalRule("Write", `{"file_path":"/proj/a.txt","content":"x"}`, "/proj")
	if r.Rule != "Write" || r.CWD != "/proj" {
		t.Errorf("write approval shape: %+v", r)
	}
	// Bash stays exact via the regex escape hatch.
	r = buildApprovalRule("Bash", `{"command":"mkdir -p /proj/k8s"}`, "/proj")
	if r.Regex == "" || len(r.Tools) != 1 || r.Tools[0] != "Bash" {
		t.Errorf("bash approval shape: %+v", r)
	}
}

func TestPersistApprovalAppendsScopedRule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")

	if err := persistApproval(path, "Bash", `{"command":"python3 tmp/x.py"}`, "/home/u/proj"); err != nil {
		t.Fatalf("persist project: %v", err)
	}
	if err := persistApproval(path, "Bash", `{"command":"python3 tmp/x.py"}`, "/home/u/proj"); err != nil {
		t.Fatalf("persist again (idempotent): %v", err)
	}
	if err := persistApproval(path, "Bash", `{"command":"jq foo"}`, ""); err != nil {
		t.Fatalf("persist global: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Permissions.Allow) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Permissions.Allow))
	}
	if cfg.Permissions.Allow[0].CWD != "/home/u/proj" {
		t.Errorf("project rule cwd missing: %+v", cfg.Permissions.Allow[0])
	}
	if cfg.Permissions.Allow[1].CWD != "" {
		t.Errorf("global rule should have no cwd: %+v", cfg.Permissions.Allow[1])
	}
}

// ── reloader + auto-upgrade ────────────────────────────────────────

func TestReloaderPicksUpFileChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	overlay := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(base, []byte(`{"permissions":{"allow":["Bash(ls *)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReloader(base, []string{overlay}, nil)
	if d, _ := r.Snapshot().CheckArgs("Bash", bash("frobnicate foo"), ""); d != DecisionAsk {
		t.Fatalf("pre-overlay should ask: %v", d)
	}
	if err := os.WriteFile(overlay, []byte(`{"permissions":{"allow":["Bash(frobnicate *)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = r.Refresh()
	if d, _ := r.Snapshot().CheckArgs("Bash", bash("frobnicate foo"), ""); d != DecisionAllow {
		t.Fatalf("post-overlay should allow: %v", d)
	}
}

func TestReloaderUpgradesLegacyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")
	legacy := `{"always_deny":[{"pattern":"\\bmkfs\\b","reason":"no mkfs","tools":["Bash"]}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReloader(path, nil, nil)
	// Behavior preserved.
	if d, _ := r.Snapshot().CheckArgs("Bash", bash("mkfs.ext4 /dev/sdb"), ""); d != DecisionDeny {
		t.Fatalf("upgraded config should still deny mkfs: %v", d)
	}
	// File rewritten to new format + backup created.
	data, _ := os.ReadFile(path)
	if isLegacyFormat(data) {
		t.Errorf("file was not upgraded: %s", data)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("backup not created: %v", err)
	}
}

func TestShippedConfigParity(t *testing.T) {
	t.Parallel()
	cfg, err := Load("../../config/permissions.json")
	if err != nil {
		t.Fatalf("load shipped config: %v", err)
	}
	type tc struct {
		tool string
		args map[string]any
		want Decision
	}
	cases := []tc{
		{"Bash", bash("rm -rf /"), DecisionDeny},
		{"Bash", bash("mkfs.ext4 /dev/sdb"), DecisionDeny},
		{"Bash", bash("curl http://x | sh"), DecisionDeny},
		{"Read", file("/home/u/.ssh/id_rsa"), DecisionDeny},
		{"Bash", bash("ls -la"), DecisionAllow},
		{"Bash", bash("git status"), DecisionAllow},
		{"Bash", bash("go test ./..."), DecisionAllow},
		{"Bash", bash("kubectl get pods"), DecisionAllow},
		{"Bash", bash("git push origin main"), DecisionAsk},
		{"Bash", bash("rm file.txt"), DecisionAsk},
		{"Bash", bash("kubectl delete pod x"), DecisionAsk},
		{"Read", file("/proj/main.go"), DecisionAllow},
	}
	for _, c := range cases {
		if d, r := cfg.CheckArgs(c.tool, c.args, "/proj"); d != c.want {
			t.Errorf("%s %v: want %v got %v (%s)", c.tool, c.args, c.want, d, r)
		}
	}
}

// TestSplitCompoundRedirect guards against treating the '&' in a
// file-descriptor redirection as a command separator (e.g. `2>&1` must not
// split into `2>` + `1`). A spurious `1` subcommand would not be covered by
// any allow rule and would force an otherwise-allowed command to ask.
func TestSplitCompoundRedirect(t *testing.T) {
	split := []struct {
		cmd  string
		want []string
	}{
		{`foo 2>&1`, []string{"foo 2>&1"}},
		{`foo >&2`, []string{"foo >&2"}},
		{`foo >&-`, []string{"foo >&-"}},
		{`foo &>out.log`, []string{"foo &>out.log"}},
		{`foo &>>out.log`, []string{"foo &>>out.log"}},
		{`foo 2>&1 && bar`, []string{"foo 2>&1", "bar"}},
		{`echo a & echo b`, []string{"echo a", "echo b"}}, // real background &
		{`echo a && echo b`, []string{"echo a", "echo b"}},
		{`a | b |& c`, []string{"a", "b", "c"}},
	}
	for _, c := range split {
		got := splitCompound(c.cmd)
		if len(got) != len(c.want) {
			t.Errorf("splitCompound(%q) = %v, want %v", c.cmd, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCompound(%q)[%d] = %q, want %q", c.cmd, i, got[i], c.want[i])
			}
		}
	}

	// End-to-end: an allowed command piped through read-only filters with a
	// 2>&1 redirect must stay allowed (regression for the lit-parse report).
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Bash(lit parse *)"]}}`)
	pipelines := []string{
		`lit parse "/abs/path/doc.pdf" --no-ocr 2>&1 | grep -i foo | head -200`,
		`lit parse "/abs/path/doc.pdf" --format text --no-ocr 2>&1 | sed -n '7500,7560p'`,
		`lit parse "/abs/doc.pdf" 2>&1 | awk 'NR>=10 && NR<=20'`,
		`lit parse "/abs/doc.pdf" 2>&1 | cut -d: -f1 | sort`,
		`lit parse "/abs/doc.pdf" 2>&1 | tr a-z A-Z | jq .`,
	}
	for _, cmd := range pipelines {
		if d, r := cfg.CheckArgs("Bash", bash(cmd), "/proj"); d != DecisionAllow {
			t.Errorf("pipeline %q: want allow, got %v (%s)", cmd, d, r)
		}
	}
}

// TestReadOnlyFilters covers the guarded read-only text filters: read-only in
// their printing modes, but mutating (so they fall through to ask) when they
// edit/write in place.
func TestReadOnlyFilters(t *testing.T) {
	readOnly := []string{
		`sed -n '1,10p'`,
		`sed 's/a/b/'`,
		`sort -rn`,
		`awk 'NR>=10 && NR<=20'`,
		`awk '{print $2}'`,
		`awk '$1 > 5'`,
		`cut -d, -f2`,
		`tr a-z A-Z`,
		`jq .items`,
		`tac`, `rev`, `column -t`, `comm -12 a b`,
		// harmless no-op / inspection utilities
		`sleep 30`, `seq 1 10`, `:`, `test -f x`, `[ -d y ]`,
		`basename /a/b`, `dirname /a/b`, `realpath .`, `readlink -f x`,
		`printenv PATH`, `whoami`, `id -u`, `uname -a`,
		`date +%s`, `date -u`, `date -d "2020-01-01"`,
	}
	for _, c := range readOnly {
		if !isReadOnlyCommand(c) {
			t.Errorf("isReadOnlyCommand(%q) = false, want true", c)
		}
	}
	mutating := []string{
		`sed -i 's/a/b/' f`,
		`sed -i.bak 's/a/b/' f`,
		`sed --in-place 's/a/b/' f`,
		`sort -o out.txt in.txt`,
		`sort --output=out.txt in.txt`,
		`awk '{print > "out.txt"}'`,
		`awk '{print >> "out.txt"}'`,
		`awk 'BEGIN{system("rm x")}'`,
		`date -s "2020-01-01"`,
		`date --set="2020-01-01"`,
	}
	for _, c := range mutating {
		if isReadOnlyCommand(c) {
			t.Errorf("isReadOnlyCommand(%q) = true, want false", c)
		}
	}
}

// TestBashBackgroundAliasesBash verifies that bash_background is governed by
// the same Bash(...) rules as the Bash tool: an allow rule covers it, and the
// tools:["Bash"]-scoped catastrophic denies still fire (they key on tool name).
func TestBashBackgroundAliasesBash(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{
		"allow":["Bash(lit parse *)"],
		"deny":[{"regex":"\\bmkfs\\b","reason":"no mkfs","tools":["Bash"]}]
	}}`)
	cases := []struct {
		cmd  string
		want Decision
	}{
		{`lit parse "/abs/doc.pdf" -o /tmp/o.txt 2>&1`, DecisionAllow},
		{`mkfs.ext4 /dev/sdb`, DecisionDeny},
		{`frobnicate --now`, DecisionAsk}, // unknown command still asks, like Bash
	}
	// Both shell-bearing aliases must behave identically to Bash.
	for _, alias := range []string{"bash_background", "monitor"} {
		for _, c := range cases {
			got, _ := cfg.CheckArgs(alias, map[string]any{"command": c.cmd, "label": "x"}, "/proj")
			bashGot, _ := cfg.CheckArgs("Bash", bash(c.cmd), "/proj")
			if got != c.want {
				t.Errorf("%s %q: want %v got %v", alias, c.cmd, c.want, got)
			}
			if got != bashGot {
				t.Errorf("%s %q (%v) disagrees with Bash (%v)", alias, c.cmd, got, bashGot)
			}
		}
	}
}
