package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/sessions"
)

// TestCollectionContextHasNoMemoryText pins the removal of the single collection
// Memory text: the context routes carry instructions only, the distiller and
// revert routes are gone, and an older client still sending memory /
// memory_size / auto_update is not refused (the extra fields are ignored).
func TestCollectionContextHasNoMemoryText(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Acme"); err != nil {
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

	// PUT: instructions written, the legacy memory field ignored (no memory.md).
	if w := do(http.MethodPut, "/api/collections/Acme/context", map[string]any{"instructions": "x", "memory": "y"}); w.Code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", w.Code, w.Body.String())
	}
	if got := collectionctx.ReadInstructions("Acme"); got != "x" {
		t.Fatalf("instructions = %q, want x", got)
	}
	if _, err := os.Stat(filepath.Join(collectionctx.Dir("Acme"), "memory.md")); !os.IsNotExist(err) {
		t.Fatalf("PUT must not create memory.md (stat err %v)", err)
	}

	// GET: instructions + scalars, no memory keys.
	w := do(http.MethodGet, "/api/collections/Acme/context", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", w.Code, w.Body.String())
	}
	var snap map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["instructions"] != "x" {
		t.Fatalf("GET instructions = %v", snap["instructions"])
	}
	for _, gone := range []string{"memory", "memory_size", "auto_update", "last_memory_update", "has_prev_memory"} {
		if _, ok := snap[gone]; ok {
			t.Errorf("GET context must not carry %q: %s", gone, w.Body.String())
		}
	}
	for _, kept := range []string{"squad", "cwd", "color"} {
		if _, ok := snap[kept]; !ok {
			t.Errorf("GET context must carry %q: %s", kept, w.Body.String())
		}
	}

	// PATCH from an older client: the removed scalars are ignored, not a 400.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"memory_size": "huge", "auto_update": true}); w.Code != http.StatusOK {
		t.Fatalf("PATCH with legacy fields status %d: %s", w.Code, w.Body.String())
	}
	raw, _ := os.ReadFile(sessions.CollectionsPath())
	if bytes.Contains(raw, []byte("memory_size")) || bytes.Contains(raw, []byte("auto_update")) {
		t.Fatalf("legacy scalars persisted: %s", raw)
	}

	// The distiller and revert routes are gone.
	for _, p := range []string{"/api/collections/Acme/memory/distill", "/api/collections/Acme/memory/revert"} {
		if w := do(http.MethodPost, p, nil); w.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, w.Code)
		}
	}

	// GET /api/collections reports each collection's memory count (General too).
	store := collmem.In(paths.ConfigWriteDir())
	for _, col := range []string{"", "Acme"} {
		if _, _, err := store.Save(col, collmem.Memory{Name: "fact", Type: "project", Body: "A fact."}); err != nil {
			t.Fatal(err)
		}
	}
	w = do(http.MethodGet, "/api/collections", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", w.Code, w.Body.String())
	}
	var list struct {
		Collections []struct {
			Name       string `json:"name"`
			Memories   int    `json:"memories"`
			HasContext bool   `json:"has_context"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, c := range list.Collections {
		got[c.Name] = c.Memories
		if c.Name == "Acme" && !c.HasContext {
			t.Errorf("Acme has instructions, has_context must be true")
		}
	}
	if got[sessions.GeneralCollection] != 1 || got["Acme"] != 1 {
		t.Fatalf("memories = %v, want General:1 Acme:1", got)
	}
}
