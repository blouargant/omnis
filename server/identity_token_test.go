package main

import (
	"strings"
	"testing"
	"time"
)

func TestShellEnvFor(t *testing.T) {
	d := serverDeps{Cookie: testCookieAuth()}
	d.Cookie.tokens.Put("alice", "ta", time.Time{})
	if env := d.shellEnvFor("alice"); len(env) != 1 || env[0] != "PLAT_TOKEN=ta" {
		t.Fatalf("got %q", env)
	}
}

func TestShellEnvForUnknownOwnerIsEmpty(t *testing.T) {
	d := serverDeps{Cookie: testCookieAuth()}
	d.Cookie.tokens.Put("alice", "ta", time.Time{})
	if env := d.shellEnvFor("bob"); env != nil {
		t.Fatalf("no token for bob must yield no env (never another user's): %q", env)
	}
	if env := (serverDeps{}).shellEnvFor("alice"); env != nil {
		t.Fatalf("single-user mode must never inject: %q", env)
	}
}

func TestBangEscapeSeesOwnToken(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	w := doWithBody(r, "ta", "POST", "/api/sessions/"+aliceSID+"/bash", `{"command":"echo tok=$PLAT_TOKEN"}`)
	if !strings.Contains(w.Body.String(), "tok=ta") {
		t.Fatalf("! escape did not get alice's token: %d %s", w.Code, w.Body)
	}
}
