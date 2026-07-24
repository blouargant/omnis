package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestUpdateCollectionFleetFields verifies that PATCH /api/collections/:name
// threads the fleet project fields (role/engine/depends_on/claude_allowed_tools)
// through to CollectionProfileFull, and that GET …/context returns them back.
func TestUpdateCollectionFleetFields(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Svc"); err != nil {
		t.Fatal(err)
	}
	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}

	if w := do(http.MethodPatch, "/api/collections/Svc", map[string]any{
		"role":                 "project",
		"engine":               "claude",
		"depends_on":           []string{"Other"},
		"claude_allowed_tools": []string{"Read", "Bash(go test:*)"},
	}); w.Code != http.StatusOK {
		t.Fatalf("PATCH status %d: %s", w.Code, w.Body.String())
	}

	got := sessions.CollectionProfileFull("Svc")
	if got.Role != "project" || got.Engine != "claude" {
		t.Fatalf("role/engine not set: %+v", got)
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != "Other" {
		t.Fatalf("depends_on not set: %+v", got.DependsOn)
	}
	if len(got.ClaudeAllowedTools) != 2 {
		t.Fatalf("allowlist not set: %+v", got.ClaudeAllowedTools)
	}

	// GET …/context returns the same fields.
	var ctxResp struct {
		Role               string   `json:"role"`
		Engine             string   `json:"engine"`
		DependsOn          []string `json:"depends_on"`
		ClaudeAllowedTools []string `json:"claude_allowed_tools"`
	}
	w := do(http.MethodGet, "/api/collections/Svc/context", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ctxResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ctxResp.Role != "project" || ctxResp.Engine != "claude" {
		t.Fatalf("GET context role/engine mismatch: %+v", ctxResp)
	}
	if len(ctxResp.DependsOn) != 1 || ctxResp.DependsOn[0] != "Other" {
		t.Fatalf("GET context depends_on mismatch: %+v", ctxResp.DependsOn)
	}
	if len(ctxResp.ClaudeAllowedTools) != 2 {
		t.Fatalf("GET context claude_allowed_tools mismatch: %+v", ctxResp.ClaudeAllowedTools)
	}
}

// TestUpdateCollectionRejectsBadEngine verifies the closed-set validation on
// role/engine: an unrecognised value 400s and leaves the profile untouched.
func TestUpdateCollectionRejectsBadEngine(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Bad"); err != nil {
		t.Fatal(err)
	}
	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}

	if w := do(http.MethodPatch, "/api/collections/Bad", map[string]any{"engine": "python"}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad engine, got %d: %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPatch, "/api/collections/Bad", map[string]any{"role": "folder"}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad role, got %d: %s", w.Code, w.Body.String())
	}
	got := sessions.CollectionProfileFull("Bad")
	if got.Role != "" || got.Engine != "" {
		t.Fatalf("rejected values must not be applied: %+v", got)
	}
}

// TestUpdateCollectionRejectsMixedBadFleetAtomically verifies that a PATCH
// mixing a valid per-collection scalar (memory_size) with an invalid fleet
// field (engine) is rejected wholesale — the closed-set validation on
// role/engine runs before the scalar block writes, so the valid scalar is
// never persisted alongside the rejected fleet value. "Profile unchanged on
// rejection" must hold for the whole request body, not just the fleet block.
func TestUpdateCollectionRejectsMixedBadFleetAtomically(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Mixed"); err != nil {
		t.Fatal(err)
	}
	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}

	// Seed a known memory_size first so we can tell it was left untouched.
	if w := do(http.MethodPatch, "/api/collections/Mixed", map[string]any{"memory_size": "small"}); w.Code != http.StatusOK {
		t.Fatalf("seed PATCH status %d: %s", w.Code, w.Body.String())
	}
	if got := sessions.CollectionProfileFull("Mixed"); got.MemorySize != "small" {
		t.Fatalf("seed memory_size not applied: %+v", got)
	}

	// A mixed body: valid scalar + invalid fleet field. Must 400 and must NOT
	// persist the memory_size change.
	if w := do(http.MethodPatch, "/api/collections/Mixed", map[string]any{
		"memory_size": "large",
		"engine":      "python",
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mixed valid-scalar/bad-engine body, got %d: %s", w.Code, w.Body.String())
	}

	got := sessions.CollectionProfileFull("Mixed")
	if got.MemorySize != "small" {
		t.Fatalf("scalar must not be persisted when the fleet block rejects: memory_size = %q, want \"small\"", got.MemorySize)
	}
	if got.Engine != "" {
		t.Fatalf("rejected engine must not be applied: %+v", got)
	}
}
