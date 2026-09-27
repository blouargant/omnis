// Package packaging holds regression tests for the static packaging assets
// under this directory (the .deb/.rpm login-shell profile script, etc.) that
// are otherwise only ever exercised by actually installing a package.
package packaging

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/permissions"
)

// The shared test deployment must never ship a token in the iapcli config,
// must mount it read-only, must not run as root, must not grant the pod a
// ServiceAccount token, and must keep A2A off.
func TestIaparcTestManifest(t *testing.T) {
	b, err := os.ReadFile("k8s/iaparc-test/omnis.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := string(b)
	for _, want := range []string{
		"namespace: test-system",
		"OMNIS_IDENTITY_MODE",
		"automountServiceAccountToken: false",
		"runAsNonRoot: true",
		"readOnly: true",
		"iapregistrykey",
		"nginx.ingress.kubernetes.io/auth-url",
		"path: /omnis",
		"allowPrivilegeEscalation: false",
		"drop: [ALL]",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
	for _, bad := range []string{"IAPCLI_TOKEN:", "OMNIS_CONFIG_PATH", "OMNIS_USER_ID", "a2a_enabled: true"} {
		if strings.Contains(m, bad) {
			t.Errorf("manifest must not contain %q", bad)
		}
	}
}

// The deployment's .agents layer must route IA Parc requests to an agent that
// actually loads the iaparc skill — without it the router has no squad for
// "list my iaparc projects" and asks the user what iaparc is.
func TestIaparcTestAgentsLayer(t *testing.T) {
	var cfg struct {
		Agents     []string `json:"agents"`
		StartSquad string   `json:"start_squad"`
		Squads     []struct {
			Name    string   `json:"name"`
			Leader  string   `json:"leader"`
			Members []string `json:"members"`
		} `json:"squads"`
	}
	b, err := os.ReadFile("k8s/iaparc-test/agents/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range cfg.Squads {
		if s.Name == "IA Parc" && s.Leader == "none" && len(s.Members) == 1 && s.Members[0] == "iaparc_operator" {
			found = true
		}
	}
	if !found || len(cfg.Agents) == 0 || cfg.Agents[0] != "iaparc_operator" {
		t.Fatalf("agents.json must enable iaparc_operator and a leaderless \"IA Parc\" squad: %+v", cfg)
	}
	// New chats start on the IA Parc squad: on the router, "quels sont mes
	// projets" was ambiguous and the router asked which projects were meant.
	if cfg.StartSquad != "IA Parc" {
		t.Errorf("agents.json must set start_squad \"IA Parc\", got %q", cfg.StartSquad)
	}
	var agent struct {
		Name     string   `json:"name"`
		ModelRef string   `json:"model_ref"`
		Skills   []string `json:"skills"`
		Tools    []string `json:"tools"`
	}
	b, err = os.ReadFile("k8s/iaparc-test/agents/registry/agents/iaparc_operator/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &agent); err != nil {
		t.Fatal(err)
	}
	if agent.Name != "iaparc_operator" || !strings.Contains(strings.Join(agent.Skills, ","), "iaparc") || !strings.Contains(strings.Join(agent.Tools, ","), "Bash") {
		t.Fatalf("iaparc_operator must load the iaparc skill and have Bash: %+v", agent)
	}
	// "balanced" (Scaleway qwen3.6-35b-a3b) returned a tool call as raw Qwen
	// XML text (<parameter=kind>…</tool_call>) on a real IA Parc question, so
	// the turn ended with no tool call. The operator runs on "high".
	if agent.ModelRef != "high" {
		t.Errorf("iaparc_operator must use model_ref \"high\", got %q", agent.ModelRef)
	}
	d, err := os.ReadFile("k8s/iaparc-test/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d), "COPY packaging/k8s/iaparc-test/agents /home/omnis/.agents") {
		t.Fatal("Dockerfile must install the agents layer as /home/omnis/.agents")
	}
	p, err := os.ReadFile("k8s/iaparc-test/agents/permissions.json")
	if err != nil {
		t.Fatal(err)
	}
	// A "*" in the middle of a Bash rule spans several arguments, so
	// "Bash(iapcli resources * get *)" would also allow
	// "iapcli resources pools delete --id get". Every rule must be an explicit
	// command prefix, optionally followed by a trailing " *".
	var perms struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(p, &perms); err != nil {
		t.Fatal(err)
	}
	if len(perms.Permissions.Allow) == 0 {
		t.Fatal("permissions.json must allow the read-only iapcli commands")
	}
	for _, rule := range perms.Permissions.Allow {
		inner, ok := strings.CutPrefix(rule, "Bash(iapcli ")
		if !ok || !strings.HasSuffix(inner, ")") {
			t.Errorf("rule %q must be Bash(iapcli …)", rule)
			continue
		}
		inner = strings.TrimSuffix(strings.TrimSuffix(inner, ")"), " *")
		if strings.Contains(inner, "*") {
			t.Errorf("rule %q has a wildcard before its end: it spans arguments", rule)
		}
		for _, word := range strings.Fields(inner) {
			if mutating[word] {
				t.Errorf("read-only rule %q names the mutating/secret verb %q", rule, word)
			}
		}
	}
}

// mutating lists iapcli words that change state, handle a secret, open a shell
// in a pod, or never return (dashboards): none may appear in a read-only rule.
var mutating = map[string]bool{
	"delete": true, "apply": true, "stop": true, "start": true, "reveal": true,
	"login": true, "logout": true, "add": true, "create": true, "update": true,
	"submit": true, "cancel": true, "pause": true, "resume": true, "exec": true,
	"jwt": true, "reset": true, "refill": true, "clear": true, "block": true,
	"unblock": true, "regenerate": true, "push": true, "build": true, "rmi": true,
	"migrate": true, "watch": true, "listen": true, "enable": true, "remove": true,
	"rollack": true, "context": true, "set": true,
}

// The allow list, run through the real permission engine: reads pass without a
// prompt, and every change, secret, pod shell or dashboard still asks —
// including a mutation chained after a read.
func TestIaparcTestPermissionsDecisions(t *testing.T) {
	c, err := permissions.Load("k8s/iaparc-test/agents/permissions.json")
	if err != nil {
		t.Fatal(err)
	}
	for cmd, wantAllow := range map[string]bool{
		"iapcli projects get":                                  true,
		"iapcli resources bookings quote -f b.yaml":            true,
		"iapcli teams budget --id x":                           true,
		"iapcli gpus usage":                                    true,
		"iapcli user ssh keys":                                 true,
		"iapcli resources pools delete --id get":               false,
		"iapcli projects delete --id x":                        false,
		"iapcli user ssh keys add --name k --key x":            false,
		"iapcli tokens keys reveal --id x":                     false,
		"iapcli projects get && iapcli projects delete --id x": false,
		"iapcli resources bookings watch":                      false,
		"iapcli workspaces exec -i w -- sh":                    false,
	} {
		d, _ := c.CheckArgs("Bash", map[string]any{"command": cmd}, "/tmp")
		if (d == permissions.DecisionAllow) != wantAllow {
			t.Errorf("%q: decision %v, want allow=%v", cmd, d, wantAllow)
		}
	}
}
