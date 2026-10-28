package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/configedit"
)

func pluginsEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/api")
	registerConfigRoutes(rg, editorFiles(), newRestartCoordinator(), nil, agent.Options{})
	registerPluginRoutes(rg, nil, agent.Options{})
	return r
}

func listPlugins(t *testing.T, r http.Handler) []map[string]any {
	t.Helper()
	w := do(t, r, http.MethodGet, "/api/plugins", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Plugins []map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Plugins
}

func TestPluginsRouteListsAndToggles(t *testing.T) {
	_, home := pluginServerEnv(t, "", "")
	r := pluginsEngine(t)
	// The catalogue is published by a resolve; the GET route must not depend on
	// the editor having been opened first.
	ps := listPlugins(t, r)
	if len(ps) != 1 || ps[0]["name"] != "demo" || ps[0]["enabled"] != true {
		t.Fatalf("want demo enabled, got %v", ps)
	}

	w := do(t, r, http.MethodPut, "/api/plugins/demo", map[string]any{"enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	user := readJSONFile(t, filepath.Join(home, "agents.json"))
	dp, _ := user["disabled_plugins"].([]any)
	if len(dp) != 1 || dp[0] != "demo" {
		t.Fatalf("disabled_plugins = %v", user["disabled_plugins"])
	}
	if ps = listPlugins(t, r); ps[0]["enabled"] != false {
		t.Fatalf("want demo disabled, got %v", ps)
	}

	w = do(t, r, http.MethodPut, "/api/plugins/demo", map[string]any{"enabled": true})
	if w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	merged, _, err := configedit.LoadMergedSection("agents.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := merged["disabled_plugins"]; ok {
		t.Fatalf("disabled_plugins must be removed when empty: %v", merged["disabled_plugins"])
	}
	if ps = listPlugins(t, r); ps[0]["enabled"] != true {
		t.Fatalf("want demo enabled again, got %v", ps)
	}

	if w = do(t, r, http.MethodPut, "/api/plugins/nope", map[string]any{"enabled": false}); w.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin: want 404, got %d", w.Code)
	}
}
