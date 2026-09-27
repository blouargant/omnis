// Package packaging holds regression tests for the static packaging assets
// under this directory (the .deb/.rpm login-shell profile script, etc.) that
// are otherwise only ever exercised by actually installing a package.
package packaging

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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
		Agents []string `json:"agents"`
		Squads []struct {
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
	var agent struct {
		Name   string   `json:"name"`
		Skills []string `json:"skills"`
		Tools  []string `json:"tools"`
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
	for _, bad := range []string{"delete", "apply", "stop", "reveal", "login"} {
		if strings.Contains(string(p), bad) {
			t.Errorf("read-only allow list must not mention %q", bad)
		}
	}
}
