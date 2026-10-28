package main

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/configedit"
	"github.com/blouargant/omnis/internal/paths"
)

// registerPluginRoutes mounts the Claude Code plugin list + enable/disable
// endpoints. The manager is optional: without it a toggle is persisted but no
// hot-reload fires.
func registerPluginRoutes(rg *gin.RouterGroup, manager *agent.Manager, agentOpts agent.Options) {
	// GET /api/plugins — every discovered plugin, disabled ones included. The
	// catalogue is published by a resolve, so refresh it first: the list must not
	// depend on the editor having been opened.
	rg.GET("/plugins", func(c *gin.Context) {
		_, _ = agent.ResolveRuntimeSettings(agent.Options{ConfigPathStrict: true})
		c.JSON(http.StatusOK, gin.H{"plugins": pluginList()})
	})

	// PUT /api/plugins/:name {enabled} — toggles disabled_plugins in agents.json
	// (written as a delta through the layered config writer) and hot-reloads.
	rg.PUT("/plugins/:name", func(c *gin.Context) {
		name := c.Param("name")
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		_, _ = agent.ResolveRuntimeSettings(agent.Options{ConfigPathStrict: true})
		known := false
		for _, p := range agent.ClaudePlugins() {
			if p.Name == name {
				known = true
				break
			}
		}
		if !known {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown plugin"})
			return
		}
		merged, _, err := configedit.LoadMergedSection("agents.json")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if merged == nil {
			merged = map[string]any{}
		}
		set := map[string]bool{}
		if raw, ok := merged["disabled_plugins"].([]any); ok {
			for _, v := range raw {
				if s, ok := v.(string); ok && s != "" {
					set[s] = true
				}
			}
		}
		if req.Enabled {
			delete(set, name)
		} else {
			set[name] = true
		}
		if len(set) == 0 {
			// The delta writer cannot represent a removed key: when a layer below
			// the write target still disables plugins, only an explicit empty
			// list overrides it. Delete the key only when nothing below has one.
			delete(merged, "disabled_plugins")
			if full, merr := json.Marshal(merged); merr == nil {
				if wp, ok := configedit.WritePath("agent", full); ok {
					base, _ := configedit.BaseBelowLayer("agents.json", paths.Layer(wp))
					if l, _ := base["disabled_plugins"].([]any); len(l) > 0 {
						merged["disabled_plugins"] = []any{}
					}
				}
			}
		} else {
			names := make([]string, 0, len(set))
			for n := range set {
				names = append(names, n)
			}
			sort.Strings(names)
			list := make([]any, len(names))
			for i, n := range names {
				list[i] = n
			}
			merged["disabled_plugins"] = list
		}
		if _, _, err := configedit.WriteSection("agent", merged); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// The write must have produced the requested state; a layer the delta
		// could not override would otherwise leave a silent 200.
		after, _, aerr := configedit.LoadMergedSection("agents.json")
		if aerr != nil || disabledIn(after, name) == req.Enabled {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "plugin state was not changed by the write (a lower config layer still decides it)"})
			return
		}
		reloaded := false
		reloadErr := ""
		if manager != nil {
			if _, rerr := manager.Reload(c.Request.Context(), agentOpts); rerr == nil {
				reloaded = true
			} else {
				reloadErr = rerr.Error()
			}
		}
		// Reload publishes the catalogue; without a manager, resolve here so the
		// response reports the new state.
		if !reloaded {
			_, _ = agent.ResolveRuntimeSettings(agent.Options{ConfigPathStrict: true})
		}
		resp := gin.H{"plugins": pluginList(), "reloaded": reloaded}
		if reloadErr != "" {
			resp["reload_error"] = reloadErr
		}
		c.JSON(http.StatusOK, resp)
	})
}

// pluginList is ClaudePlugins() that never marshals as null.
func pluginList() []agent.PluginInfo {
	if ps := agent.ClaudePlugins(); ps != nil {
		return ps
	}
	return []agent.PluginInfo{}
}

// disabledIn reports whether the merged agents.json lists name as disabled.
func disabledIn(cfg map[string]any, name string) bool {
	raw, _ := cfg["disabled_plugins"].([]any)
	for _, v := range raw {
		if s, _ := v.(string); s == name {
			return true
		}
	}
	return false
}
