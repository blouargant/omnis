package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/permissions"
	"github.com/blouargant/yoke/internal/paths"
)

// configFiles holds the absolute filesystem paths of the JSON files that are
// editable from the web UI. Paths are resolved once at server startup from the
// same precedence used by agent.NewAgent and never come from the HTTP client.
type configFiles struct {
	Agent       string // config/agents.json
	Permissions string // config/permissions.json
	MCP         string // config/mcp_config.json
}

// path returns the **read** path for a whitelisted name. Resolved at
// every call so that after a write lands a new override under
// $YOKE_HOME/config, subsequent GETs see the highest-precedence layer.
// The boolean is false for any other name.
func (c configFiles) path(name string) (string, bool) {
	filename, ok := configFileNames[name]
	if !ok {
		return "", false
	}
	// Honour any explicit operator-supplied path (CLI flag / env var)
	// stored in the struct fields, but only when the file actually
	// exists at that location — otherwise fall back to the 3-layer
	// search so writes can fork into $YOKE_HOME on first edit.
	switch name {
	case "agent":
		if c.Agent != "" {
			if st, err := os.Stat(c.Agent); err == nil && !st.IsDir() {
				return c.Agent, true
			}
		}
	case "permissions":
		if c.Permissions != "" {
			if st, err := os.Stat(c.Permissions); err == nil && !st.IsDir() {
				return c.Permissions, true
			}
		}
	case "mcp":
		if c.MCP != "" {
			if st, err := os.Stat(c.MCP); err == nil && !st.IsDir() {
				return c.MCP, true
			}
		}
	}
	return paths.FindConfig(filename), true
}

// writePath returns the **write** target for a whitelisted name. Writes
// always land under paths.ConfigWriteDir() ($YOKE_HOME/config) regardless
// of where the file currently resides, so a user edit on a config
// originally shipped under /etc/yoke or ./config creates a per-user
// override that subsequent reads pick up (file-level layering).
func (c configFiles) writePath(name string) (string, bool) {
	filename, ok := configFileNames[name]
	if !ok {
		return "", false
	}
	return filepath.Join(paths.ConfigWriteDir(), filename), true
}

// configFileNames maps the editor's whitelisted short names to the
// underlying JSON filenames. Used by both path() and writePath().
var configFileNames = map[string]string{
	"agent":       "agent.json",
	"permissions": "permissions.json",
	"mcp":         "mcp_config.json",
}

// resolveConfigFiles determines the absolute paths of the JSON files that
// the web UI may edit. agent.ResolveRuntimeSettings provides the same
// precedence used by agent.NewAgent (defaults → JSON → ENV → Options) so
// the editor always targets the file actually loaded by the running agent.
func resolveConfigFiles(opts agent.Options) configFiles {
	out := configFiles{
		Agent:       firstNonEmpty(opts.ConfigPath, paths.FindConfig("agent.json")),
		Permissions: firstNonEmpty(opts.PermissionsConfigPath, paths.FindConfig("permissions.json")),
		MCP:         firstNonEmpty(opts.MCPSConfigPath, paths.FindConfig("mcp_config.json")),
	}
	settings, err := agent.ResolveRuntimeSettings(opts)
	if err == nil {
		if strings.TrimSpace(settings.ConfigPath) != "" {
			out.Agent = settings.ConfigPath
		}
		if strings.TrimSpace(settings.PermissionsConfigPath) != "" {
			out.Permissions = settings.PermissionsConfigPath
		}
		if strings.TrimSpace(settings.MCPConfigPath) != "" {
			out.MCP = settings.MCPConfigPath
		}
	}
	if abs, err := filepath.Abs(out.Agent); err == nil {
		out.Agent = abs
	}
	if abs, err := filepath.Abs(out.Permissions); err == nil {
		out.Permissions = abs
	}
	if abs, err := filepath.Abs(out.MCP); err == nil {
		out.MCP = abs
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// configFileInfo is the JSON shape returned by /api/config/files.
// Path is intentionally not exposed to the browser: clients reference
// files by their whitelisted name (agent / permissions / mcp).
type configFileInfo struct {
	Name   string    `json:"name"`
	Path   string    `json:"-"`
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime"`
	Exists bool      `json:"exists"`
}

// configFilePayload is the JSON shape for read/write of a single file.
// Path is server-internal (see configFileInfo).
type configFilePayload struct {
	Name    string    `json:"name"`
	Path    string    `json:"-"`
	Content string    `json:"content"`
	MTime   time.Time `json:"mtime"`
}

// effectiveMtimePath returns the path that should be used for mtime checks
// and reporting. After the first UI save (fork-on-first-edit), the write
// target under $YOKE_HOME is authoritative; before the fork, the read source
// is used — ensuring GET and PUT see the same file and stay in sync.
func effectiveMtimePath(readPath, writePath string) string {
	if _, err := os.Stat(writePath); err == nil {
		return writePath
	}
	return readPath
}

// checkMtime verifies the on-disk mtime of path matches want. When want
// is nil it is a no-op. Returns (0, nil) when the caller may proceed,
// otherwise the HTTP status + JSON body to return.
func checkMtime(path string, want *time.Time) (int, gin.H) {
	if want == nil {
		return 0, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusConflict, gin.H{
				"error": "file no longer exists on disk",
			}
		}
		return http.StatusInternalServerError, gin.H{"error": err.Error()}
	}
	if !st.ModTime().Equal(*want) {
		return http.StatusConflict, gin.H{
			"error": "file changed on disk since it was loaded",
			"mtime": st.ModTime(),
		}
	}
	return 0, nil
}

// configWriteRequest is the request body for PUT /api/config/file/:name.
type configWriteRequest struct {
	Content string `json:"content"`
	// MTime, when set, must match the on-disk mtime; otherwise the server
	// returns 409 Conflict (optimistic concurrency).
	MTime *time.Time `json:"mtime,omitempty"`
}

// restartCoordinator is a one-shot signal that the HTTP layer raises when
// the user clicks "Restart server" in the web UI. The actual shutdown +
// re-exec is performed by run() in main.go, which observes Done(). Doing
// the work in run() (rather than in a detached goroutine here) avoids the
// race where the main goroutine returns from select on srv.ListenAndServe
// completion before the goroutine has a chance to call syscall.Exec.
type restartCoordinator struct {
	once sync.Once
	ch   chan struct{}
}

func newRestartCoordinator() *restartCoordinator {
	return &restartCoordinator{ch: make(chan struct{})}
}

// trigger raises the restart signal. Idempotent.
func (r *restartCoordinator) trigger() {
	r.once.Do(func() { close(r.ch) })
}

// Done returns a channel that is closed when a restart has been requested.
func (r *restartCoordinator) Done() <-chan struct{} { return r.ch }

// registerConfigRoutes mounts the configuration editor, hot-reload, and
// restart endpoints. The manager is required for hot-reload; pass nil to
// expose the editor and restart endpoint only.
func registerConfigRoutes(rg *gin.RouterGroup, files configFiles, restart *restartCoordinator, manager *agent.Manager, agentOpts agent.Options) {
	rg.GET("/config/files", func(c *gin.Context) {
		// Resolve at request time: after a PUT lands a new override
		// under $YOKE_HOME/config, the listing reflects it without a
		// server restart.
		agentPath, _ := files.path("agent")
		permissionsPath, _ := files.path("permissions")
		mcpPath, _ := files.path("mcp")
		out := []configFileInfo{
			describeConfigFile("agent", agentPath),
			describeConfigFile("permissions", permissionsPath),
			describeConfigFile("mcp", mcpPath),
		}
		c.JSON(http.StatusOK, gin.H{"files": out})
	})

	rg.GET("/config/file/:name", func(c *gin.Context) {
		paramName := c.Param("name")
		path, ok := files.path(paramName)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		wpath, _ := files.writePath(paramName)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusOK, configFilePayload{Name: paramName, Path: path, Content: ""})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		st, _ := os.Stat(effectiveMtimePath(path, wpath))
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, configFilePayload{
			Name:    paramName,
			Path:    path,
			Content: string(data),
			MTime:   mtime,
		})
	})

	rg.PUT("/config/file/:name", func(c *gin.Context) {
		readPath, ok := files.path(c.Param("name"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		writePath, _ := files.writePath(c.Param("name"))
		var req configWriteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		// Parse-only validation: only round-trip through json.Unmarshal
		// when the file isn't empty (an empty editor buffer should still
		// be writable so users can clear a config).
		if strings.TrimSpace(req.Content) != "" {
			var probe any
			if err := json.Unmarshal([]byte(req.Content), &probe); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
				return
			}
		}
		// After the first fork the write target is authoritative; before
		// it the read source is. effectiveMtimePath picks the right one
		// so GET and PUT always agree on which file's mtime to track.
		if status, body := checkMtime(effectiveMtimePath(readPath, writePath), req.MTime); status != 0 {
			c.JSON(status, body)
			return
		}
		if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := atomicWriteFile(writePath, []byte(req.Content)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		st, _ := os.Stat(writePath)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, configFilePayload{
			Name:    c.Param("name"),
			Path:    writePath,
			Content: req.Content,
			MTime:   mtime,
		})
	})

	rg.POST("/server/restart", func(c *gin.Context) {
		if restart == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "restart not available"})
			return
		}
		restart.trigger()
		c.JSON(http.StatusAccepted, gin.H{"status": "restarting"})
	})

	// POST /config/reload — rebuild the agent generation from the on-disk
	// JSON. In-flight sessions stay pinned to their existing generation so
	// nothing visible changes for them; new sessions get the reloaded
	// config. The old generation is torn down once its session refcount
	// drops to zero.
	rg.POST("/config/reload", func(c *gin.Context) {
		if manager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "hot-reload not available"})
			return
		}
		inst, err := manager.Reload(c.Request.Context(), agentOpts)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		gens := manager.Generations()
		var draining int
		for gen, refs := range gens {
			if gen != inst.Generation {
				draining += refs
			}
		}
		log.Printf("server: hot-reload complete (generation=%d, draining=%d)", inst.Generation, draining)
		c.JSON(http.StatusOK, gin.H{
			"generation":        inst.Generation,
			"draining_sessions": draining,
			"generations":       gens,
		})
	})

	// GET /config/status — exposes the current agent generation + a map of
	// generation → pinned-session count so the UI can show a "n sessions
	// draining on previous version" indicator after a reload.
	rg.GET("/config/status", func(c *gin.Context) {
		if manager == nil {
			c.JSON(http.StatusOK, gin.H{
				"hot_reload_available": false,
				"generation":           1,
				"generations":          map[string]int{"1": 0},
			})
			return
		}
		gens := manager.Generations()
		current := manager.CurrentGeneration()
		var draining int
		for gen, refs := range gens {
			if gen != current {
				draining += refs
			}
		}
		var squadNames []string
		if cur := manager.Current(); cur != nil {
			squadNames = cur.SquadNames()
		}
		c.JSON(http.StatusOK, gin.H{
			"hot_reload_available": true,
			"generation":           current,
			"draining_sessions":    draining,
			"generations":          gens,
			"squads":               squadNames,
		})
	})

	// GET /squads — lists the squads available in the current generation.
	// Used by the web UI to populate the new-chat squad picker and to
	// render squad badges on session list entries.
	rg.GET("/squads", func(c *gin.Context) {
		type squadDTO struct {
			Name               string            `json:"name"`
			Description        string            `json:"description"`
			Leader             string            `json:"leader"`
			Members            []string          `json:"members"`
			MemberDescriptions map[string]string `json:"member_descriptions,omitempty"`
		}
		out := struct {
			Default string     `json:"default"`
			Squads  []squadDTO `json:"squads"`
		}{Default: agent.DefaultSquadName}
		if manager == nil {
			c.JSON(http.StatusOK, out)
			return
		}
		inst := manager.Current()
		if inst == nil {
			c.JSON(http.StatusOK, out)
			return
		}
		out.Default = inst.DefaultName
		settings := inst.Settings
		// Build a quick name → description lookup so the UI can render the
		// member list without a second round-trip.
		descByName := map[string]string{}
		for _, a := range settings.Agents {
			if a.Description != "" {
				descByName[a.Name] = a.Description
			}
		}
		for _, sqCfg := range settings.Squads {
			members := append([]string(nil), sqCfg.Members...)
			memberDescs := map[string]string{}
			for _, m := range members {
				if d := descByName[m]; d != "" {
					memberDescs[m] = d
				}
			}
			out.Squads = append(out.Squads, squadDTO{
				Name:               sqCfg.Name,
				Description:        sqCfg.Description,
				Leader:             sqCfg.Leader,
				Members:            members,
				MemberDescriptions: memberDescs,
			})
		}
		c.JSON(http.StatusOK, out)
	})

	// GET /config/skill-permissions — read-only view of permissions contributed
	// by skills that are linked into any agent's skills directory. Used by the
	// Web UI to display skill-sourced rules alongside the editable base config.
	rg.GET("/config/skill-permissions", func(c *gin.Context) {
		type ruleDTO struct {
			Pattern string `json:"pattern"`
			Reason  string `json:"reason,omitempty"`
		}
		type contribution struct {
			Skill       string    `json:"skill"`
			AlwaysDeny  []ruleDTO `json:"always_deny"`
			AlwaysAllow []ruleDTO `json:"always_allow"`
			AskUser     []ruleDTO `json:"ask_user"`
		}
		toDTO := func(rs []permissions.Rule) []ruleDTO {
			out := make([]ruleDTO, len(rs))
			for i, r := range rs {
				out[i] = ruleDTO{Pattern: r.Pattern, Reason: r.Reason}
			}
			return out
		}

		agentPath, _ := files.path("agent")
		settings, err := agent.ResolveRuntimeSettings(agent.Options{
			ConfigPath:       agentPath,
			ConfigPathStrict: true,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		seen := map[string]bool{}
		var contributions []contribution
		registryDir := paths.SkillsRegistryDir()
		for _, agentCfg := range settings.Agents {
			skillNames := agentCfg.Skills
			if len(skillNames) == 0 {
				// No explicit list — scan all installed skills.
				entries, _ := os.ReadDir(registryDir)
				for _, e := range entries {
					if e.IsDir() {
						skillNames = append(skillNames, e.Name())
					}
				}
			}
			for _, skillName := range skillNames {
				permPath := filepath.Join(registryDir, skillName, "permissions.json")
				key := permPath
				if seen[key] {
					continue
				}
				seen[key] = true
				r, err := permissions.Load(permPath)
				if err != nil || !r.HasRules() {
					continue
				}
				contributions = append(contributions, contribution{
					Skill:       skillName,
					AlwaysDeny:  toDTO(r.AlwaysDeny),
					AlwaysAllow: toDTO(r.AlwaysAllow),
					AskUser:     toDTO(r.AskUser),
				})
			}
		}

		if contributions == nil {
			contributions = []contribution{}
		}
		c.JSON(http.StatusOK, gin.H{"contributions": contributions})
	})

	// Parsed JSON view: lets the browser render structured forms without
	// reparsing the file client-side. The PUT side accepts arbitrary JSON,
	// pretty-prints it, and writes atomically. Comments and original
	// formatting are NOT preserved by this path — clients should fall back
	// to the raw /config/file/:name endpoint when fidelity matters.
	rg.GET("/config/parsed/:name", func(c *gin.Context) {
		name := c.Param("name")
		path, ok := files.path(name)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		wpath, _ := files.writePath(name)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var parsed any
		if len(data) > 0 {
			if err := json.Unmarshal(data, &parsed); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("file is not valid JSON: %v", err)})
				return
			}
		}

		// Special handling for agent config: resolve agents from the registry
		// and return the full RuntimeSettings so the UI sees the resolved
		// agent definitions, not just the names from config/agents.json.
		if name == "agent" && parsed != nil {
			settings, err := agent.ResolveRuntimeSettings(agent.Options{ConfigPathStrict: true})
			if err == nil && parsed != nil {
				// Merge the runtime agents and models back into the parsed config
				// so the UI displays the full agent details.
				if m, ok := parsed.(map[string]any); ok {
					// Build agent objects from resolved configs
					agents := make([]any, 0, len(settings.Agents))
					for _, a := range settings.Agents {
						agents = append(agents, map[string]any{
							"name":                   a.Name,
							"description":            a.Description,
							"enabled":                a.Enabled,
							"leader":                 a.Leader,
							"builtin":                a.BuiltIn,
							"model_ref":              a.ModelRef,
							"provider":               a.Provider,
							"model":                  a.Model,
							"base_url":               a.BaseURL,
							"api_key":                a.APIKey,
							"tools":                  a.Tools,
							"skills":                 a.Skills,
							"softskills_dir":         a.SoftSkillsDir,
							"allow_file_attachments": a.AllowFileAttachments,
							"instruction":            agent.ReadAgentInstruction(a.Name),
						})
					}
					// Sort agents: built-in first, then custom
					sort.SliceStable(agents, func(i, j int) bool {
						aBuiltIn := agents[i].(map[string]any)["builtin"]
						bBuiltIn := agents[j].(map[string]any)["builtin"]
						aIsBuiltIn := aBuiltIn == true
						bIsBuiltIn := bBuiltIn == true
						if aIsBuiltIn != bIsBuiltIn {
							return aIsBuiltIn // built-in comes first
						}
						// Within same category, maintain order
						return false
					})
					m["agents"] = agents
				}
			}
		}

		st, _ := os.Stat(effectiveMtimePath(path, wpath))
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, gin.H{
			"name":  name,
			"data":  parsed,
			"mtime": mtime,
		})
	})

	rg.PUT("/config/parsed/:name", func(c *gin.Context) {
		name := c.Param("name")
		readPath, ok := files.path(name)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		writePath, _ := files.writePath(name)
		var req struct {
			Data  any        `json:"data"`
			MTime *time.Time `json:"mtime,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}

		// Special handling for agent config: extract agents and write them
		// to registry/agents/{name}/agent.json instead of inline.
		if name == "agent" {
			if m, ok := req.Data.(map[string]any); ok {
				if agentsList, ok := m["agents"].([]any); ok {
					// Extract agents from the request and write them to the registry
					agentsRegistry := paths.AgentsRegistryWriteDir()
					agentNames := make([]string, 0, len(agentsList))

					for _, item := range agentsList {
						agentMap, ok := item.(map[string]any)
						if !ok {
							continue
						}
						agentName, ok := agentMap["name"].(string)
						if !ok || agentName == "" {
							continue
						}

						// Write the agent to registry/agents/{name}/agent.json
						agentDir := filepath.Join(agentsRegistry, agentName)
						if err := os.MkdirAll(agentDir, 0o755); err != nil {
							c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("mkdir registry agent: %v", err)})
							return
						}

						// Extract and save instruction separately if provided
						var instruction string
						if instr, ok := agentMap["instruction"].(string); ok {
							instruction = instr
						}

						// Convert the agent map to a clean AgentEntry (remove fields that shouldn't be saved)
						cleanAgent := map[string]any{}
						for k, v := range agentMap {
							// Skip empty values and instruction (saves separately) to keep files clean
							if k == "name" || k == "description" || k == "enabled" || k == "leader" ||
								k == "builtin" || k == "model_ref" || k == "provider" || k == "model" || k == "base_url" ||
								k == "api_key" || k == "tools" || k == "skills" || k == "softskills_dir" ||
								k == "allow_file_attachments" || k == "mcp_config_path" || k == "mcp_servers" ||
								k == "permissions_config_path" {
								if k != "instruction" { // instruction is saved separately
									cleanAgent[k] = v
								}
							}
						}

						agentJSON, err := json.MarshalIndent(cleanAgent, "", "  ")
						if err != nil {
							c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("marshal agent: %v", err)})
							return
						}
						agentJSON = append(agentJSON, '\n')

						agentPath := filepath.Join(agentDir, "agent.json")
						if err := atomicWriteFile(agentPath, agentJSON); err != nil {
							c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("write agent: %v", err)})
							return
						}

						// Save instruction to registry/agents/{name}/instruction.md if provided
						if instruction != "" {
							instructionPath := filepath.Join(agentDir, "instruction.md")
							if err := atomicWriteFile(instructionPath, []byte(instruction)); err != nil {
								c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("write instruction: %v", err)})
								return
							}
						}

						agentNames = append(agentNames, agentName)
					}

					// Update the main config to reference agents by name only
					m["agents"] = agentNames
				}
			}
		}

		out, err := json.MarshalIndent(req.Data, "", "  ")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cannot serialize to JSON: %v", err)})
			return
		}
		out = append(out, '\n')
		if status, body := checkMtime(effectiveMtimePath(readPath, writePath), req.MTime); status != 0 {
			c.JSON(status, body)
			return
		}
		if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := atomicWriteFile(writePath, out); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		st, _ := os.Stat(writePath)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, gin.H{
			"name":    name,
			"content": string(out),
			"mtime":   mtime,
		})
	})
}

func describeConfigFile(name, path string) configFileInfo {
	info := configFileInfo{Name: name, Path: path}
	st, err := os.Stat(path)
	if err != nil {
		return info
	}
	info.Exists = true
	info.Size = st.Size()
	info.MTime = st.ModTime()
	return info
}

// atomicWriteFile writes data to path via a sibling temp file and renames
// it into place. The temp file is removed on any failure. The destination's
// existing file mode is preserved when present; otherwise 0o644 is used.
// The parent directory must already exist — writes fail loudly when it does
// not, which is the right outcome for an editor that targets known files.
func atomicWriteFile(path string, data []byte) error {
	perm := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-cfg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
