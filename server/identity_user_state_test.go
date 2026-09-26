package main

import (
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
	body := doAs(r, "ta", "GET", "/api/folder").Body.String()
	if !strings.Contains(body, filepath.Join("users", "alice", "work")) {
		t.Fatalf("global folder for alice should be her work dir: %s", body)
	}
}
