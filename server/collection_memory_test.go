package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// seedSession creates a session filed under `collection` with one conversation
// turn written so gatherCollectionMaterial can load it. Collection/Hidden/Turns
// are set directly on the in-memory meta (List/New share the live pointer) to
// avoid the Registry setters' async disk-persist goroutines, which would race
// t.Setenv cleanup.
func seedSession(t *testing.T, reg *sessions.Registry, collection, user, asst string, hidden bool) string {
	t.Helper()
	m := reg.New("default")
	m.Collection = collection
	m.Hidden = hidden
	m.Turns = 1
	if err := sessions.AppendConversationTurn(m.ID, user, asst); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	return m.ID
}

func TestGatherCollectionMaterial_FiltersAndIncludes(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	d := serverDeps{Registry: reg}

	seedSession(t, reg, "Work", "how do I deploy?", "use the pipeline", false)
	seedSession(t, reg, "Work", "secret hidden", "nope", true)             // hidden → excluded
	seedSession(t, reg, "Other", "unrelated topic", "other answer", false) // other collection → excluded

	mat := gatherCollectionMaterial(d, "Work")
	if !strings.Contains(mat, "use the pipeline") {
		t.Fatalf("in-collection session missing from material:\n%s", mat)
	}
	if strings.Contains(mat, "secret hidden") {
		t.Fatal("hidden session leaked into material")
	}
	if strings.Contains(mat, "unrelated topic") {
		t.Fatal("other-collection session leaked into material")
	}
}

func TestGatherCollectionMaterial_EmptyWhenNoSessions(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	if mat := gatherCollectionMaterial(serverDeps{Registry: reg}, "Empty"); mat != "" {
		t.Fatalf("expected empty material, got %q", mat)
	}
}

// TestDistillRoute_NoManager confirms the route degrades cleanly (503) rather
// than panicking when there is no agent manager wired (e.g. a test harness).
func TestDistillRoute_NoManager(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	if _, _, err := sessions.AddCollection("Work"); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	req := httptest.NewRequest(http.MethodPost, "/api/collections/Work/memory/distill", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no manager, got %d: %s", w.Code, w.Body.String())
	}
}
