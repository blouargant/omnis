package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestLoadMissingRulesReturnsEmptySet(t *testing.T) {
	t.Parallel()

	rules, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rules == nil {
		t.Fatal("Load() returned nil rules")
	}
	if decision, _ := rules.Check("Bash", "ls", ""); decision != DecisionAsk {
		t.Fatalf("Check() = %v, want ask (safe default for empty rule set)", decision)
	}
}

func TestRulesCheckFallsThroughToAskByDefault(t *testing.T) {
	t.Parallel()

	rules := &Rules{}
	if err := rules.compile(); err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	cases := []string{
		`{"command":"some-novel-command"}`,
		`{"command":"git add ."}`,
		`{"command":"make"}`,
	}
	for _, in := range cases {
		if decision, _ := rules.Check("Bash", in, ""); decision != DecisionAsk {
			t.Fatalf("Check(%q) = %v, want ask", in, decision)
		}
	}
}

func TestRulesCheckHonorsDenyAllowAndAsk(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "permissions.json")
	content := `{
  "always_deny": [
    {"pattern": "rm\\s+-rf", "reason": "destructive"}
  ],
  "always_allow": ["ls"],
  "ask_user": [
    {"pattern": "kubectl", "reason": "cluster access"}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rules, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if decision, reason := rules.Check("Bash", `{"command":"rm -rf /tmp/demo"}`, ""); decision != DecisionDeny || reason != "destructive" {
		t.Fatalf("deny Check() = (%v, %q)", decision, reason)
	}
	if decision, _ := rules.Check("Bash", `{"command":"ls -la"}`, ""); decision != DecisionAllow {
		t.Fatalf("allow Check() = %v", decision)
	}
	if decision, reason := rules.Check("Bash", `{"command":"kubectl get pods"}`, ""); decision != DecisionAsk || reason != "cluster access" {
		t.Fatalf("ask Check() = (%v, %q)", decision, reason)
	}
}

func TestCheckHonorsCWDScope(t *testing.T) {
	t.Parallel()

	rules := &Rules{
		AlwaysAllow: []Rule{
			{Pattern: `^Bash \{"command":"do-thing"`, CWD: "/home/u/proj-a"},
		},
	}
	if err := rules.compile(); err != nil {
		t.Fatalf("compile() = %v", err)
	}

	cases := []struct {
		cwd  string
		want Decision
	}{
		{"/home/u/proj-a", DecisionAllow},
		{"/home/u/proj-a/sub/dir", DecisionAllow}, // sub-directory matches
		{"/home/u/proj-b", DecisionAsk},
		{"", DecisionAsk}, // no cwd → cwd-scoped rule doesn't fire
	}
	for _, c := range cases {
		got, _ := rules.Check("Bash", `{"command":"do-thing"}`, c.cwd)
		if got != c.want {
			t.Errorf("cwd=%q got=%v want=%v", c.cwd, got, c.want)
		}
	}
}

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
	if c.has("", "k") {
		t.Fatal("empty session id matched")
	}
	c.Forget("s1")
	if c.has("s1", "k") {
		t.Fatal("Forget did not clear")
	}
}

func TestSessionApprovalCacheToolGrant(t *testing.T) {
	t.Parallel()

	c := newSessionApprovalCache()
	if c.hasTool("s1", "Write") {
		t.Fatal("empty cache reports tool grant")
	}
	c.addTool("s1", "Write")
	if !c.hasTool("s1", "Write") {
		t.Fatal("tool grant miss after add")
	}
	if c.hasTool("s1", "Bash") {
		t.Fatal("tool grant leaked across tools")
	}
	if c.hasTool("s2", "Write") {
		t.Fatal("tool grant leaked across sessions")
	}
	if c.hasTool("", "Write") {
		t.Fatal("empty session id matched")
	}
	c.Forget("s1")
	if c.hasTool("s1", "Write") {
		t.Fatal("Forget did not clear tool grant")
	}
}

func TestBuildApprovalRuleBroadensFileTools(t *testing.T) {
	t.Parallel()

	// File tools broaden to "this tool on any path" so approving the
	// first of N writes covers the rest.
	pat, _ := buildApprovalRule("Write", `{"file_path":"/proj/a.txt","content":"x"}`, "/proj")
	re, err := regexp.Compile("(?i)" + pat)
	if err != nil {
		t.Fatalf("compile broadened pattern %q: %v", pat, err)
	}
	for _, fp := range []string{"/proj/a.txt", "/proj/sub/b.yaml", "/proj/c"} {
		probe := `Write {"file_path":"` + fp + `","content":"x"}`
		if !re.MatchString(probe) {
			t.Errorf("broadened Write rule did not match %q", probe)
		}
	}
	if re.MatchString(`Read {"file_path":"/proj/a.txt"}`) {
		t.Error("Write rule should not match a Read probe")
	}

	// Bash stays exact-command — no blanket shell allow persisted.
	pat, _ = buildApprovalRule("Bash", `{"command":"mkdir -p /proj/k8s"}`, "/proj")
	re, err = regexp.Compile("(?i)" + pat)
	if err != nil {
		t.Fatalf("compile bash pattern %q: %v", pat, err)
	}
	if !re.MatchString(`Bash {"command":"mkdir -p /proj/k8s"}`) {
		t.Error("bash rule did not match its own command")
	}
	if re.MatchString(`Bash {"command":"rm -rf /"}`) {
		t.Error("bash rule must not match a different command")
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
		t.Fatalf("persist again (should be idempotent): %v", err)
	}
	if err := persistApproval(path, "Bash", `{"command":"jq foo"}`, ""); err != nil {
		t.Fatalf("persist global: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var on Rules
	if err := json.Unmarshal(data, &on); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(on.AlwaysAllow) != 2 {
		t.Fatalf("expected 2 rules, got %d: %s", len(on.AlwaysAllow), string(data))
	}
	if on.AlwaysAllow[0].CWD != "/home/u/proj" {
		t.Errorf("project rule cwd missing: %+v", on.AlwaysAllow[0])
	}
	if on.AlwaysAllow[1].CWD != "" {
		t.Errorf("global rule should have no cwd: %+v", on.AlwaysAllow[1])
	}
}

func TestReloaderPicksUpFileChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	overlay := filepath.Join(dir, "user.json")

	if err := os.WriteFile(base, []byte(`{"always_allow":[{"pattern":"^Bash .*\"command\":\"ls\""}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewReloader(base, []string{overlay}, nil)
	if got, _ := r.Snapshot().Check("Bash", `{"command":"ls"}`, ""); got != DecisionAllow {
		t.Fatalf("initial: want Allow, got %v", got)
	}
	if got, _ := r.Snapshot().Check("Bash", `{"command":"jq foo"}`, ""); got != DecisionAsk {
		t.Fatalf("initial: want Ask for unlisted, got %v", got)
	}

	// Write the overlay and force a refresh.
	if err := os.WriteFile(overlay, []byte(`{"always_allow":[{"pattern":"^Bash .*\"command\":\"jq foo\""}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// mtime resolution can be coarse on some filesystems; nudge it to a
	// distinctly later timestamp before forcing the refresh.
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(overlay, future, future)

	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got, _ := r.Snapshot().Check("Bash", `{"command":"jq foo"}`, ""); got != DecisionAllow {
		t.Fatalf("after reload: want Allow for jq, got %v", got)
	}
}
