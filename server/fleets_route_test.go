package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestFleetsRoutesCRUDAndAssign drives the real router (mirroring
// TestListSessionsPaginated / TestCollectionContextSizeAutoUpdateAndRevert) and
// exercises the full /api/fleets surface: create → assign a project → list with
// a derived member → rename cascades the member's fleet tag → an invalid
// default_engine PATCH is rejected without mutating → unassign returns the
// project to Ungrouped (still role:"project") → delete.
func TestFleetsRoutesCRUDAndAssign(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// Create a fleet.
	if w := do(http.MethodPost, "/api/fleets", `{"name":"Payments","color":"blue","default_engine":"omnis"}`); w.Code != http.StatusOK {
		t.Fatalf("create fleet: %d %s", w.Code, w.Body.String())
	}
	// A project collection assigned to it.
	if _, _, err := sessions.AddCollection("api"); err != nil {
		t.Fatalf("AddCollection: %v", err)
	}
	if w := do(http.MethodPost, "/api/fleets/Payments/projects", `{"collection":"api"}`); w.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", w.Code, w.Body.String())
	}

	// GET lists it with a derived member.
	w := do(http.MethodGet, "/api/fleets", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var fleets []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fleets); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	var pay map[string]any
	for _, f := range fleets {
		if f["name"] == "Payments" {
			pay = f
		}
	}
	if pay == nil {
		t.Fatalf("Payments not listed: %s", w.Body.String())
	}
	if members, _ := pay["members"].([]any); len(members) != 1 {
		t.Fatalf("Payments members = %v, want 1", pay["members"])
	}
	// assign promoted api to a project ⇒ it must carry role=project + engine.
	if p := sessions.CollectionProfileFull("api"); p.Role != "project" || p.Engine != "omnis" || p.Fleet != "Payments" {
		t.Fatalf("assigned api profile = %+v", p)
	}

	// Rename via PATCH.
	if w := do(http.MethodPatch, "/api/fleets/Payments", `{"name":"Billing"}`); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if !sessions.FleetExists("Billing") || sessions.FleetExists("Payments") {
		t.Fatalf("rename didn't take")
	}
	if sessions.CollectionProfileFull("api").Fleet != "Billing" {
		t.Fatalf("rename didn't cascade member tag")
	}

	// Invalid default engine is rejected without mutating.
	if w := do(http.MethodPatch, "/api/fleets/Billing", `{"default_engine":"gpt"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad engine should 400, got %d", w.Code)
	}

	// Unassign returns api to Ungrouped (still a project).
	if w := do(http.MethodDelete, "/api/fleets/Billing/projects/api", ""); w.Code != http.StatusOK {
		t.Fatalf("unassign: %d %s", w.Code, w.Body.String())
	}
	if p := sessions.CollectionProfileFull("api"); p.Fleet != "" || p.Role != "project" {
		t.Fatalf("after unassign api = %+v (want fleet='' role=project)", p)
	}

	// Delete the fleet.
	if w := do(http.MethodDelete, "/api/fleets/Billing", ""); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if sessions.FleetExists("Billing") {
		t.Fatalf("fleet still present after delete")
	}
}
