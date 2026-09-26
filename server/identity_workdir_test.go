package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// An imported session in cookie mode starts in its owner's own working
// directory, exactly like POST /sessions — never the shared process root.
func TestImportStartsInOwnersWorkDir(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	bashCwd = newBashCwdStore()
	body := `{"title":"x","turns":[{"user_text":"hi","assistant_text":"yo"}]}`
	w := doWithBody(r, "ta", "POST", "/api/import/session", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if got, want := bashCwd.get(out.SessionID), userWorkDir("alice"); got != want {
		t.Fatalf("imported cwd = %q, want alice's work dir %q", got, want)
	}
}

// A fresh scheduled-run session starts in its owner's working directory in
// cookie mode, and at the shared root otherwise (single-user unchanged).
func TestScheduledSessionStartsInOwnersWorkDir(t *testing.T) {
	_, d, _, _ := newCookieTestEngine(t)
	bashCwd = newBashCwdStore()
	sid := createScheduledSession(d, "alice", "", "do it")
	if got, want := bashCwd.get(sid), userWorkDir("alice"); got != want {
		t.Fatalf("scheduled cwd = %q, want %q", got, want)
	}
	d.Cookie = nil
	sid2 := createScheduledSession(d, "web-user", "", "do it")
	if got := bashCwd.get(sid2); got != bashCwd.root {
		t.Fatalf("single-user scheduled cwd = %q, want the shared root %q", got, bashCwd.root)
	}
}
