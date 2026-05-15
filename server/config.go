package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/permissions"
)

// configFiles holds the absolute filesystem paths of the YAML files that are
// editable from the web UI. Paths are resolved once at server startup from the
// same precedence used by agent.NewAgent and never come from the HTTP client.
type configFiles struct {
	Agent       string // config/agent.yaml
	Permissions string // config/permissions.yaml
	MCP         string // config/mcp_config.yaml
}

// path returns the absolute file path for a whitelisted name (agent /
// permissions / mcp). The boolean is false for any other name.
func (c configFiles) path(name string) (string, bool) {
	switch name {
	case "agent":
		return c.Agent, true
	case "permissions":
		return c.Permissions, true
	case "mcp":
		return c.MCP, true
	default:
		return "", false
	}
}

// resolveConfigFiles determines the absolute paths of the YAML files that
// the web UI may edit. agent.ResolveRuntimeSettings provides the same
// precedence used by agent.NewAgent (defaults → YAML → ENV → Options) so
// the editor always targets the file actually loaded by the running agent.
func resolveConfigFiles(opts agent.Options) configFiles {
	out := configFiles{
		Agent:       firstNonEmpty(opts.ConfigPath, "config/agent.yaml"),
		Permissions: firstNonEmpty(opts.PermissionsConfigPath, "config/permissions.yaml"),
		MCP:         firstNonEmpty(opts.MCPSConfigPath, "config/mcp_config.yaml"),
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
	Name  string    `json:"name"`
	Path  string    `json:"-"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
	Exists bool     `json:"exists"`
}

// configFilePayload is the JSON shape for read/write of a single file.
// Path is server-internal (see configFileInfo).
type configFilePayload struct {
	Name    string    `json:"name"`
	Path    string    `json:"-"`
	Content string    `json:"content"`
	MTime   time.Time `json:"mtime"`
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
	Content string     `json:"content"`
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

// registerConfigRoutes mounts the configuration editor and restart endpoints.
func registerConfigRoutes(rg *gin.RouterGroup, files configFiles, restart *restartCoordinator) {
	rg.GET("/config/files", func(c *gin.Context) {
		out := []configFileInfo{
			describeConfigFile("agent", files.Agent),
			describeConfigFile("permissions", files.Permissions),
			describeConfigFile("mcp", files.MCP),
		}
		c.JSON(http.StatusOK, gin.H{"files": out})
	})

	rg.GET("/config/file/:name", func(c *gin.Context) {
		path, ok := files.path(c.Param("name"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusOK, configFilePayload{Name: c.Param("name"), Path: path, Content: ""})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		st, _ := os.Stat(path)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, configFilePayload{
			Name:    c.Param("name"),
			Path:    path,
			Content: string(data),
			MTime:   mtime,
		})
	})

	rg.PUT("/config/file/:name", func(c *gin.Context) {
		path, ok := files.path(c.Param("name"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		var req configWriteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		// Parse-only YAML validation: anything that round-trips through
		// yaml.Unmarshal into `any` is accepted.
		var probe any
		if err := yaml.Unmarshal([]byte(req.Content), &probe); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid YAML: %v", err)})
			return
		}
		if status, body := checkMtime(path, req.MTime); status != 0 {
			c.JSON(status, body)
			return
		}
		if err := atomicWriteFile(path, []byte(req.Content)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		st, _ := os.Stat(path)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, configFilePayload{
			Name:    c.Param("name"),
			Path:    path,
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

		settings, err := agent.ResolveRuntimeSettings(agent.Options{
			ConfigPath:       files.Agent,
			ConfigPathStrict: true,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		seen := map[string]bool{}
		var contributions []contribution
		for _, agentCfg := range settings.Agents {
			dir := agentCfg.SkillsDir
			if dir == "" {
				dir = settings.SkillsDir
			}
			if dir == "" {
				continue
			}
			absDir, _ := filepath.Abs(dir)
			entries, err := os.ReadDir(absDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				permPath := filepath.Join(absDir, e.Name(), "permissions.yaml")
				key := permPath
				if real, err := filepath.EvalSymlinks(permPath); err == nil {
					key = real
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				r, err := permissions.Load(permPath)
				if err != nil || !r.HasRules() {
					continue
				}
				contributions = append(contributions, contribution{
					Skill:       e.Name(),
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
	// shipping a YAML parser. The PUT side accepts arbitrary JSON, marshals
	// it to YAML and writes atomically. Comments and original formatting
	// are NOT preserved by this path — clients should fall back to the raw
	// /config/file/:name endpoint when fidelity matters.
	rg.GET("/config/parsed/:name", func(c *gin.Context) {
		path, ok := files.path(c.Param("name"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var parsed any
		if len(data) > 0 {
			if err := yaml.Unmarshal(data, &parsed); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("file is not valid YAML: %v", err)})
				return
			}
		}
		st, _ := os.Stat(path)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, gin.H{
			"name":  c.Param("name"),
			"data":  normalizeYAMLForJSON(parsed),
			"mtime": mtime,
		})
	})

	rg.PUT("/config/parsed/:name", func(c *gin.Context) {
		path, ok := files.path(c.Param("name"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown config file"})
			return
		}
		var req struct {
			Data  any        `json:"data"`
			MTime *time.Time `json:"mtime,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		out, err := yaml.Marshal(req.Data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cannot serialize to YAML: %v", err)})
			return
		}
		if status, body := checkMtime(path, req.MTime); status != 0 {
			c.JSON(status, body)
			return
		}
		if err := atomicWriteFile(path, out); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		st, _ := os.Stat(path)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, gin.H{
			"name":    c.Param("name"),
			"content": string(out),
			"mtime":   mtime,
		})
	})
}

// normalizeYAMLForJSON converts map[any]any (yaml.v3 default) into
// map[string]any so the result can round-trip through encoding/json.
func normalizeYAMLForJSON(v any) any {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normalizeYAMLForJSON(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeYAMLForJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalizeYAMLForJSON(val)
		}
		return out
	default:
		return v
	}
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
