package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferencesArePerUser(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	if w := doWithBody(r, "ta", "PUT", "/api/preferences", `{"theme":"github-dark"}`); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if body := doAs(r, "tb", "GET", "/api/preferences").Body.String(); strings.Contains(body, "github-dark") {
		t.Fatalf("bob sees alice's theme: %s", body)
	}
	if body := doAs(r, "ta", "GET", "/api/preferences").Body.String(); !strings.Contains(body, "github-dark") {
		t.Fatalf("alice lost her theme: %s", body)
	}
}

func TestUserRoots(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if userRoot("") == userRoot("alice") {
		t.Fatal("per-user root must differ from the shared root")
	}
	if filepath.Base(userRoot("A/B")) != "a_b" {
		t.Fatalf("login must be sanitised: %s", userRoot("A/B"))
	}
	if userWorkDir("") != "" {
		t.Fatal("no work dir outside cookie mode")
	}
}

func TestNewSessionStartsInUserWorkDir(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	w := doWithBody(r, "ta", "POST", "/api/sessions", `{}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.SessionID == "" {
		t.Fatalf("could not parse session_id from create response: %v %s", err, w.Body)
	}
	// Assert on the SESSION's own cwd (bashCwd.get(sid)), not the session-less
	// global browse cwd — that one falls back to userWorkDir on its own even
	// with no start-dir seeding, so it would pass regardless of whether
	// POST /api/sessions actually seeds the new session's cwd.
	body := doAs(r, "ta", "GET", "/api/sessions/"+created.SessionID+"/folder").Body.String()
	if !strings.Contains(body, filepath.Join("users", "alice", "work")) {
		t.Fatalf("new session's own cwd for alice should be her work dir: %s", body)
	}
}

// A login and its differently-cased spelling must resolve to the SAME
// preferences store — "Alice" and "alice" share one preferences.json on disk
// (userRoot lower-cases via identity.LoginSegment), so two independent
// *preferencesStore values for them would mean two mutexes racing on one
// file, and a PUT under one casing invisible under the other.
func TestPrefStoresCaseInsensitiveLogin(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	stores := newPrefStores(configFiles{})
	if stores.forLogin("Alice") != stores.forLogin("alice") {
		t.Fatal("forLogin(\"Alice\") and forLogin(\"alice\") must return the same *preferencesStore")
	}
}

// Same requirement for the session-less browse cwd store.
func TestBashCwdStoreCaseInsensitiveLogin(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	s := newBashCwdStore()
	s.setGlobalFor("Alice", t.TempDir())
	if got := s.getGlobalFor("alice"); got != s.getGlobalFor("Alice") {
		t.Fatalf("getGlobalFor(%q) = %q, getGlobalFor(%q) = %q: must agree", "alice", got, "Alice", s.getGlobalFor("Alice"))
	}
}
