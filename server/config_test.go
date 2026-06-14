package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/internal/paths"
)

// seedConfigFile pins $YOKE_HOME at a fresh temp directory and writes the
// given config under paths.ConfigWriteDir() so that the editor's read path
// (FindConfig) and write path (ConfigWriteDir) point at the same file.
// Returns the file's absolute path.
func seedConfigFile(t *testing.T, filename, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	t.Setenv("YOKE_CONFIG_DIRS", home)
	p := filepath.Join(home, filename)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// editorFiles returns a configFiles populated from $YOKE_HOME so editor
// handlers resolve read/write paths under the test's pinned home.
func editorFiles() configFiles {
	return configFiles{
		Agent:       paths.FindConfig("agent.json"),
		Permissions: paths.FindConfig("permissions.json"),
		MCP:         paths.FindConfig("mcp_config.json"),
	}
}

func newTestEngine(t *testing.T, files configFiles) *gin.Engine {
	t.Helper()
	// Belt-and-braces: every test using the config editor gets its own
	// $YOKE_HOME so a misaligned readPath/writePath (a regression bug)
	// can never write into the developer's real $HOME/.yoke. seedConfigFile
	// already does this; this defends every other test path.
	if os.Getenv("YOKE_HOME") == "" {
		t.Setenv("YOKE_HOME", t.TempDir())
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/api")
	registerConfigRoutes(rg, files, newRestartCoordinator(), nil, agent.Options{})
	return r
}

func tmpFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func do(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGetUnknownFile_404(t *testing.T) {
	r := newTestEngine(t, configFiles{Agent: tmpFile(t, "a.json", "{\"x\":1}\n")})
	w := do(t, r, http.MethodGet, "/api/config/file/bogus", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	r := newTestEngine(t, configFiles{Agent: tmpFile(t, "a.json", "{\"x\":1}\n")})
	// gin route is /config/file/:name — the slash in `..` would not match
	// the single-segment param, so we expect a 404 from the router.
	w := do(t, r, http.MethodGet, "/api/config/file/..%2Fetc%2Fpasswd", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRoundTripRaw(t *testing.T) {
	p := seedConfigFile(t, "agents.json", "{\"key\":\"original\"}\n")
	r := newTestEngine(t, editorFiles())

	w := do(t, r, http.MethodGet, "/api/config/file/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got configFilePayload
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Content, "original") {
		t.Fatalf("missing original content: %q", got.Content)
	}

	w = do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "{\"key\":\"updated\"}\n",
		"mtime":   got.MTime,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(p)
	if string(data) != "{\"key\":\"updated\"}\n" {
		t.Fatalf("file not updated: %q", data)
	}
}

func TestPutInvalidJSON_400(t *testing.T) {
	p := tmpFile(t, "a.json", "{\"key\":\"ok\"}\n")
	r := newTestEngine(t, configFiles{Agent: p})
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "{key: unbalanced",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
	// Original file must be untouched.
	data, _ := os.ReadFile(p)
	if string(data) != "{\"key\":\"ok\"}\n" {
		t.Fatalf("file should be unchanged, got %q", data)
	}
}

func TestPutStaleMTime_409(t *testing.T) {
	p := tmpFile(t, "a.json", "{\"key\":\"original\"}\n")
	r := newTestEngine(t, configFiles{Agent: p})
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "{\"key\":\"new\"}\n",
		// epoch time will not match the file's mtime
		"mtime": "1970-01-01T00:00:00Z",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutMissingFileWithMTime_409(t *testing.T) {
	// File is configured but does not yet exist on disk; client passing an
	// mtime means it thinks it was editing a real file. checkMtime must
	// reject the write rather than silently creating the file.
	dir := t.TempDir()
	missing := filepath.Join(dir, "ghost.json")
	r := newTestEngine(t, configFiles{Agent: missing})
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "{\"key\":\"new\"}\n",
		"mtime":   "2025-01-01T00:00:00Z",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("file must not have been created, stat err=%v", err)
	}
}

func TestNoPathLeakInResponses(t *testing.T) {
	p := tmpFile(t, "a.json", "{\"key\":\"v\"}\n")
	r := newTestEngine(t, configFiles{Agent: p})

	w := do(t, r, http.MethodGet, "/api/config/file/agent", nil)
	if strings.Contains(w.Body.String(), p) || strings.Contains(w.Body.String(), "\"path\"") {
		t.Fatalf("raw GET leaked path: %s", w.Body.String())
	}
	w = do(t, r, http.MethodGet, "/api/config/files", nil)
	if strings.Contains(w.Body.String(), p) || strings.Contains(w.Body.String(), "\"path\"") {
		t.Fatalf("listing leaked path: %s", w.Body.String())
	}
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if strings.Contains(w.Body.String(), p) || strings.Contains(w.Body.String(), "\"path\"") {
		t.Fatalf("parsed GET leaked path: %s", w.Body.String())
	}
}

func TestAtomicWritePreservesMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(p, []byte("{\"k\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(p, []byte("{\"k\":2}\n")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode not preserved: got %o want 600", st.Mode().Perm())
	}
}

func TestDescribeConfigFile_Missing(t *testing.T) {
	info := describeConfigFile("agent", filepath.Join(t.TempDir(), "nope.json"))
	if info.Exists {
		t.Fatal("Exists must be false for a missing file")
	}
	if info.Size != 0 {
		t.Fatalf("Size must be 0, got %d", info.Size)
	}
	if info.Name != "agent" {
		t.Fatalf("Name not preserved: %q", info.Name)
	}
}

func TestParsedRoundTrip(t *testing.T) {
	p := seedConfigFile(t, "permissions.json", "{\"always_deny\":[\"rm -rf /\"]}\n")
	r := newTestEngine(t, editorFiles())

	w := do(t, r, http.MethodGet, "/api/config/parsed/permissions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	data, _ := got["data"].(map[string]any)
	if data == nil {
		t.Fatalf("expected map data, got %v", got["data"])
	}

	w = do(t, r, http.MethodPut, "/api/config/parsed/permissions", map[string]any{
		"data": map[string]any{
			"always_deny": []any{"sudo rm"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	out, _ := os.ReadFile(p)
	if !strings.Contains(string(out), "sudo rm") {
		t.Fatalf("file content unexpected: %q", out)
	}
}

// hooks.json is exposed through the generic config editor routes (it is just
// another whitelisted name), so the Settings → Hooks form can read/write it.
func TestHooksParsedRoundTrip(t *testing.T) {
	p := seedConfigFile(t, "hooks.json", "{\"hooks\":{}}\n")
	r := newTestEngine(t, editorFiles())

	w := do(t, r, http.MethodGet, "/api/config/parsed/hooks", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	w = do(t, r, http.MethodPut, "/api/config/parsed/hooks", map[string]any{
		"data": map[string]any{
			"hooks": map[string]any{
				"PreToolUse": []any{map[string]any{
					"matcher": "Write",
					"hooks":   []any{map[string]any{"type": "command", "command": "./guard.sh"}},
				}},
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	out, _ := os.ReadFile(p)
	if !strings.Contains(string(out), "./guard.sh") {
		t.Fatalf("hooks.json not persisted: %q", out)
	}
}

// localProjectSetup chdirs into a fresh temp dir, creates a project-local
// configuration directory there (.agents/ or agents/, per dirName), and pins
// $YOKE_HOME at a separate temp so the editor's read/write resolution can
// pick between the two layers. Returns (projectRoot, localDir, home).
func localProjectSetup(t *testing.T, dirName string) (string, string, string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	local := filepath.Join(proj, dirName)
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	t.Setenv("YOKE_CONFIG_DIRS", "")
	return proj, local, home
}

// seedLocalSkill creates a skill at <localDir>/skills/<name>/SKILL.md so it is
// resolvable only from the local layer.
func seedLocalSkill(t *testing.T, localDir, name string) {
	t.Helper()
	dir := filepath.Join(localDir, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test skill\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLayerAwareWrite_AgentsConfigLocalOnlySkill(t *testing.T) {
	for _, dirName := range []string{".agents", "agents"} {
		t.Run(dirName, func(t *testing.T) {
			_, local, home := localProjectSetup(t, dirName)
			seedLocalSkill(t, local, "local-skill")
			// Seed a custom agent under the local registry that references the
			// local-only skill. The web UI editor branch loads the agent's
			// declared skills via paths.AgentsRegistrySearchDirs.
			agentDir := filepath.Join(local, "registry/agents/custom-agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			agentBody := `{"name":"custom-agent","skills":["local-skill"]}` + "\n"
			if err := os.WriteFile(filepath.Join(agentDir, "agent.json"), []byte(agentBody), 0o644); err != nil {
				t.Fatal(err)
			}

			r := newTestEngine(t, editorFiles())
			req := map[string]any{
				"content": `{"agents":["custom-agent"]}` + "\n",
			}
			w := do(t, r, http.MethodPut, "/api/config/file/agent", req)
			if w.Code != http.StatusOK {
				t.Fatalf("put: %d %s", w.Code, w.Body.String())
			}
			// Local-layer save expected.
			localPath := filepath.Join(local, "agents.json")
			if _, err := os.Stat(localPath); err != nil {
				t.Fatalf("expected %s, stat err=%v", localPath, err)
			}
			// $YOKE_HOME must remain untouched.
			if _, err := os.Stat(filepath.Join(home, "agents.json")); err == nil {
				t.Fatalf("user-layer agents.json should not exist")
			}
		})
	}
}

func TestLayerAwareWrite_NoLocalRefStaysOnUser(t *testing.T) {
	_, local, home := localProjectSetup(t, ".agents")
	// No skill, no agent — empty local dir, so any save should still target user.
	_ = local

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": `{"agents":["leader"]}` + "\n",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "agents.json")); err != nil {
		t.Fatalf("expected user-layer save, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "agents.json")); err == nil {
		t.Fatalf("local agents.json should not exist (no local-only refs)")
	}
}

func TestLayerAwareWrite_ParsedAgentRoutesPerAgent(t *testing.T) {
	_, local, home := localProjectSetup(t, ".agents")
	seedLocalSkill(t, local, "local-skill")

	r := newTestEngine(t, editorFiles())
	// PUT a parsed agents.json with one custom agent that references the local skill.
	w := do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{
		"data": map[string]any{
			"agents": []any{
				map[string]any{
					"name":        "new-agent",
					"description": "uses local skill",
					"skills":      []any{"local-skill"},
				},
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	// The per-agent agent.json must land in the local registry.
	wantAgent := filepath.Join(local, "registry/agents/new-agent/agent.json")
	if _, err := os.Stat(wantAgent); err != nil {
		t.Fatalf("expected %s, stat err=%v", wantAgent, err)
	}
	// And so should the top-level agents.json.
	if _, err := os.Stat(filepath.Join(local, "agents.json")); err != nil {
		t.Fatalf("expected local agents.json, stat err=%v", err)
	}
	// The user-layer registry must not contain the agent.
	if _, err := os.Stat(filepath.Join(home, "registry/agents/new-agent/agent.json")); err == nil {
		t.Fatalf("user-layer registry should not have the local-promoted agent")
	}
}

func TestRestartEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/api")
	restart := newRestartCoordinator()
	registerConfigRoutes(rg, configFiles{}, restart, nil, agent.Options{})

	w := do(t, r, http.MethodPost, "/api/server/restart", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", w.Code, w.Body.String())
	}
	select {
	case <-restart.Done():
		// ok
	default:
		t.Fatal("restart signal was not raised")
	}

	// Second call must remain idempotent and still return 202.
	w = do(t, r, http.MethodPost, "/api/server/restart", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("second call: want 202, got %d", w.Code)
	}
}
