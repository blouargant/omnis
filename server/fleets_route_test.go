package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestFleetRouteCaseOnlyRename guards the HTTP layer against silently dropping a
// case-only re-case ("payments" → "Payments"). The data layer's RenameFleet
// supports it (exact-string compare, with TestRenameFleetCaseOnlyPreservesMetadata),
// but handleUpdateFleet used EqualFold, so the PATCH skipped the rename and the
// stored key/metadata stayed under the old casing — reachable code the UI could
// never trigger. This drives the real route and asserts the re-case migrates the
// fleet key, its metadata, and the member tag (mirroring handleUpdateCollection).
func TestFleetRouteCaseOnlyRename(t *testing.T) {
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

	if w := do(http.MethodPost, "/api/fleets", `{"name":"payments","color":"blue","default_engine":"claude"}`); w.Code != http.StatusOK {
		t.Fatalf("create fleet: %d %s", w.Code, w.Body.String())
	}
	if _, _, err := sessions.AddCollection("api"); err != nil {
		t.Fatalf("AddCollection: %v", err)
	}
	if w := do(http.MethodPost, "/api/fleets/payments/projects", `{"collection":"api"}`); w.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", w.Code, w.Body.String())
	}

	// Case-only rename via PATCH must migrate everything.
	if w := do(http.MethodPatch, "/api/fleets/payments", `{"name":"Payments"}`); w.Code != http.StatusOK {
		t.Fatalf("case-only rename: %d %s", w.Code, w.Body.String())
	}
	if !sessions.FleetExists("Payments") {
		t.Fatalf("case-only rename didn't move the fleet key to 'Payments'")
	}
	if m := sessions.FleetMetaFor("Payments"); m.Color != "blue" || m.DefaultEngine != "claude" {
		t.Fatalf("case-only rename lost metadata: %+v", m)
	}
	if got := sessions.CollectionProfileFull("api").Fleet; got != "Payments" {
		t.Fatalf("case-only rename didn't re-case the member tag: %q", got)
	}
}

// TestFleetCreateProjectRoute drives the create mode of
// POST /api/fleets/:name/projects — the web UI's "New project…", which makes a
// purpose-built project instead of re-tagging one of the user's topic folders. It
// pins the parts that matter: the workspace directory (and engine/dependencies) are
// stored at birth, the mandatory-directory + name-collision + bad-engine refusals,
// the non-git advisory, and that a refused create leaves NO stray collection behind.
func TestFleetCreateProjectRoute(t *testing.T) {
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
	has := func(name string) bool {
		names, _ := sessions.ListCollections()
		for _, n := range names {
			if strings.EqualFold(n, name) {
				return true
			}
		}
		return false
	}

	if w := do(http.MethodPost, "/api/fleets", `{"name":"Payments","color":"blue","default_engine":"omnis"}`); w.Code != http.StatusOK {
		t.Fatalf("create fleet: %d %s", w.Code, w.Body.String())
	}

	// A real git repo → created with no advisory.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := do(http.MethodPost, "/api/fleets/Payments/projects",
		`{"name":"api","dir":`+jsonStr(repo)+`,"color":"green","engine":"claude","depends_on":["contracts","api"],"claude_allowed_tools":["Read","Edit"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Name    string `json:"name"`
		Warning string `json:"warning"`
		Members []struct {
			Name   string `json:"name"`
			Engine string `json:"engine"`
		} `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Warning != "" {
		t.Fatalf("git repo should raise no warning, got %q", created.Warning)
	}
	if len(created.Members) != 1 || created.Members[0].Name != "api" || created.Members[0].Engine != "claude" {
		t.Fatalf("member not reported: %+v", created.Members)
	}
	p := sessions.CollectionProfileFull("api")
	if p.Cwd != repo {
		t.Fatalf("workspace directory not stored: %q want %q", p.Cwd, repo)
	}
	if p.Role != "project" || p.Fleet != "Payments" || p.Engine != "claude" {
		t.Fatalf("project profile wrong: %+v", p)
	}
	// The self-reference in depends_on is dropped; the real dependency is kept.
	if len(p.DependsOn) != 1 || p.DependsOn[0] != "contracts" {
		t.Fatalf("depends_on wrong: %v", p.DependsOn)
	}
	if len(p.ClaudeAllowedTools) != 2 {
		t.Fatalf("allowlist wrong: %v", p.ClaudeAllowedTools)
	}
	colors, err := sessions.CollectionColors()
	if err != nil {
		t.Fatalf("CollectionColors: %v", err)
	}
	if colors["api"] != "green" {
		t.Fatalf("colour wrong: %q", colors["api"])
	}

	// A plain (non-git) directory is allowed but advertised — worktree isolation
	// for forked experiments needs a repo.
	plain := t.TempDir()
	w = do(http.MethodPost, "/api/fleets/Payments/projects", `{"name":"docs","dir":`+jsonStr(plain)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create non-git project: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not_a_git_repo") {
		t.Fatalf("expected a not-a-git-repo warning, got %s", w.Body.String())
	}
	// The fleet default seeds the engine when the caller names none.
	if got := sessions.CollectionProfileFull("docs").Engine; got != "omnis" {
		t.Fatalf("engine not seeded from the fleet default: %q", got)
	}

	// Refusals — each must leave no collection behind.
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"no directory", `{"name":"web"}`, http.StatusBadRequest},
		{"missing directory", `{"name":"web","dir":"/definitely/not/here"}`, http.StatusBadRequest},
		{"bad engine", `{"name":"web","dir":` + jsonStr(repo) + `,"engine":"gpt"}`, http.StatusBadRequest},
		{"name taken", `{"name":"api","dir":` + jsonStr(repo) + `}`, http.StatusConflict},
		{"unknown fleet", `{"name":"web","dir":` + jsonStr(repo) + `}`, http.StatusNotFound},
	} {
		path := "/api/fleets/Payments/projects"
		if tc.name == "unknown fleet" {
			path = "/api/fleets/Nope/projects"
		}
		if w := do(http.MethodPost, path, tc.body); w.Code != tc.want {
			t.Fatalf("%s: got %d (want %d) %s", tc.name, w.Code, tc.want, w.Body.String())
		}
		if has("web") {
			t.Fatalf("%s: left a stray collection behind", tc.name)
		}
	}
}

// jsonStr quotes s as a JSON string (temp dirs can contain characters that must be
// escaped, so never interpolate one raw into a literal body).
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
