package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestPromptsRouteListsDefaultsOverridesAndReach(t *testing.T) {
	layer := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OPENAI_API_KEY", "sk-secret-marker-123")
	write := func(rel, body string) {
		p := filepath.Join(layer, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("registry/agents/leader/agent.json", `{"name": "leader", "enabled": true, "leader": true, "tools": ["fs"]}`)
	write("registry/agents/leader/instruction.md", "You are leader.")
	write("agents.json", `{"agents": ["leader"], "router_squad": "none", "squads": [{"name": "system", "leader": "leader", "members": []}]}`)
	write("prompt_overrides.json", `{"prompts": {"block.choice": "MY CHOICE RULE"}}`)

	ctx := context.Background()
	opts := toolkitagent.Options{DeferModelErrors: true}
	infra, err := toolkitagent.BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer infra.Close()
	inst, err := toolkitagent.BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatal(err)
	}
	mgr := toolkitagent.NewManager(infra, inst)
	defer mgr.Close()

	srv := httptest.NewServer(newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), Manager: mgr, rootCtx: ctx}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/prompts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body struct {
		Prompts []struct {
			ID       string   `json:"id"`
			Default  string   `json:"default"`
			Override *string  `json:"override"`
			Agents   []string `json:"agents"`
		} `json:"prompts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, p := range body.Prompts {
		byID[p.ID] = i
		if i > 0 && body.Prompts[i-1].ID > p.ID {
			t.Errorf("not sorted at %s", p.ID)
		}
	}
	choice := body.Prompts[byID["block.choice"]]
	golden, _ := os.ReadFile(filepath.Join("..", "agent", "testdata", "prompts_golden", "choice.txt"))
	if choice.Override == nil || *choice.Override != "MY CHOICE RULE" {
		t.Errorf("block.choice override = %v", choice.Override)
	}
	if !strings.Contains(string(golden), choice.Default) || choice.Default == "" {
		t.Errorf("block.choice default is not the shipped text: %q", choice.Default)
	}
	if !contains(choice.Agents, "leader") {
		t.Errorf("block.choice agents = %v", choice.Agents)
	}
	i, ok := byID["tool.Bash"]
	if !ok {
		t.Fatalf("no tool.Bash entry")
	}
	if bash := body.Prompts[i]; !contains(bash.Agents, "leader") || bash.Default == "" || bash.Override != nil {
		t.Errorf("tool.Bash entry = %+v", bash)
	}
	if steer := body.Prompts[byID["contract.root"]]; steer.Override != nil {
		t.Errorf("contract.root has an override")
	}
	all, _ := json.Marshal(body)
	if strings.Contains(string(all), "sk-secret-marker-123") {
		t.Errorf("the response leaks an env value")
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
