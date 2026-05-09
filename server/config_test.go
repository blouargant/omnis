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
)

func newTestEngine(t *testing.T, files configFiles) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/api")
	registerConfigRoutes(rg, files, newRestartCoordinator())
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
	r := newTestEngine(t, configFiles{Agent: tmpFile(t, "a.yaml", "x: 1\n")})
	w := do(t, r, http.MethodGet, "/api/config/file/bogus", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	r := newTestEngine(t, configFiles{Agent: tmpFile(t, "a.yaml", "x: 1\n")})
	// gin route is /config/file/:name — the slash in `..` would not match
	// the single-segment param, so we expect a 404 from the router.
	w := do(t, r, http.MethodGet, "/api/config/file/..%2Fetc%2Fpasswd", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRoundTripRaw(t *testing.T) {
	p := tmpFile(t, "a.yaml", "key: original\n")
	r := newTestEngine(t, configFiles{Agent: p})

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
		"content": "key: updated\n",
		"mtime":   got.MTime,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(p)
	if string(data) != "key: updated\n" {
		t.Fatalf("file not updated: %q", data)
	}
}

func TestPutInvalidYAML_400(t *testing.T) {
	p := tmpFile(t, "a.yaml", "key: ok\n")
	r := newTestEngine(t, configFiles{Agent: p})
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "key: [unbalanced\n",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
	// Original file must be untouched.
	data, _ := os.ReadFile(p)
	if string(data) != "key: ok\n" {
		t.Fatalf("file should be unchanged, got %q", data)
	}
}

func TestPutStaleMTime_409(t *testing.T) {
	p := tmpFile(t, "a.yaml", "key: original\n")
	r := newTestEngine(t, configFiles{Agent: p})
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "key: new\n",
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
	missing := filepath.Join(dir, "ghost.yaml")
	r := newTestEngine(t, configFiles{Agent: missing})
	w := do(t, r, http.MethodPut, "/api/config/file/agent", map[string]any{
		"content": "key: new\n",
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
	p := tmpFile(t, "a.yaml", "key: v\n")
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
	p := filepath.Join(dir, "secret.yaml")
	if err := os.WriteFile(p, []byte("k: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(p, []byte("k: 2\n")); err != nil {
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
	info := describeConfigFile("agent", filepath.Join(t.TempDir(), "nope.yaml"))
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
	p := tmpFile(t, "perm.yaml", "always_deny:\n  - rm -rf /\n")
	r := newTestEngine(t, configFiles{Permissions: p})

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

func TestRestartEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/api")
	restart := newRestartCoordinator()
	registerConfigRoutes(rg, configFiles{}, restart)

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
