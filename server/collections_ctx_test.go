package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestCollectionContextSizeAutoUpdateAndRevert(t *testing.T) {
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

	// PATCH size + auto-update.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"memory_size": "large", "auto_update": true}); w.Code != http.StatusOK {
		t.Fatalf("PATCH status %d: %s", w.Code, w.Body.String())
	}
	// A squad-only PATCH must not wipe them.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"squad": ""}); w.Code != http.StatusOK {
		t.Fatalf("PATCH squad status %d: %s", w.Code, w.Body.String())
	}
	// GET surfaces the fields.
	var got struct {
		MemorySize    string `json:"memory_size"`
		AutoUpdate    bool   `json:"auto_update"`
		HasPrevMemory bool   `json:"has_prev_memory"`
	}
	w := do(http.MethodGet, "/api/collections/Acme/context", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.MemorySize != "large" || !got.AutoUpdate {
		t.Fatalf("fields not persisted: %+v", got)
	}
	if got.HasPrevMemory {
		t.Fatal("unexpected prev memory")
	}
	// Invalid size rejected.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"memory_size": "huge"}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad size, got %d", w.Code)
	}

	// Simulate an auto-commit: prev snapshot + new memory + timestamp.
	collectionctx.WriteMemory("Acme", "new facts")
	collectionctx.WritePrevMemory("Acme", "old facts")
	sessions.SetCollectionMemoryUpdate("Acme", 111)
	w = do(http.MethodGet, "/api/collections/Acme/context", nil)
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.HasPrevMemory {
		t.Fatal("expected has_prev_memory true")
	}
	// Revert restores prev + consumes the snapshot.
	if w := do(http.MethodPost, "/api/collections/Acme/memory/revert", nil); w.Code != http.StatusOK {
		t.Fatalf("revert status %d: %s", w.Code, w.Body.String())
	}
	if m := collectionctx.ReadMemory("Acme"); m != "old facts" {
		t.Fatalf("revert did not restore memory: %q", m)
	}
	if collectionctx.HasPrevMemory("Acme") {
		t.Fatal("revert did not consume snapshot")
	}
	if sessions.CollectionProfileFull("Acme").LastMemoryUpdate != 0 {
		t.Fatal("revert did not clear last_memory_update")
	}
	// A manual memory PUT clears a fresh snapshot (the net covers only unreviewed auto-commits).
	collectionctx.WritePrevMemory("Acme", "snapshot2")
	sessions.SetCollectionMemoryUpdate("Acme", 222)
	if w := do(http.MethodPut, "/api/collections/Acme/context", map[string]any{"memory": "hand edited"}); w.Code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", w.Code, w.Body.String())
	}
	if collectionctx.HasPrevMemory("Acme") || sessions.CollectionProfileFull("Acme").LastMemoryUpdate != 0 {
		t.Fatal("manual PUT did not consume snapshot")
	}
}
