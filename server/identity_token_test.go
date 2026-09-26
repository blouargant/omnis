package main

import (
	"net/http"
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

// TestBangEscapeRefusesOtherUsersSession is the end-to-end negative case for
// shellEnvFor: bob must never reach alice's session (ownerGuard refuses it
// with 404 before handleBash ever runs), and — belt and braces — alice's
// token must never appear in the response body.
func TestBangEscapeRefusesOtherUsersSession(t *testing.T) {
	r, d, aliceSID, _ := newCookieTestEngine(t)
	// A distinctive token (the shared "ta" is a substring of too many bodies to
	// make the leak assertion meaningful). Alice logs in with it first, so it
	// is live in the token store when bob tries her session.
	const aliceSecret = "alice-secret-7f3a9c"
	d.Cookie.validator.(fakeValidator)[aliceSecret] = "alice"
	if w := doAs(r, aliceSecret, "GET", "/api/whoami"); w.Code != http.StatusOK {
		t.Fatalf("alice login: %d %s", w.Code, w.Body)
	}
	if tok, ok := d.Cookie.tokens.Get("alice"); !ok || tok != aliceSecret {
		t.Fatalf("alice's token must be live: %q %v", tok, ok)
	}
	w := doWithBody(r, "tb", "POST", "/api/sessions/"+aliceSID+"/bash", `{"command":"echo tok=$PLAT_TOKEN"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob must not reach alice's session: got %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), aliceSecret) {
		t.Fatalf("response must not leak alice's token: %s", w.Body)
	}
}

// TestTerminalWSHonoursOwnerGuard is the fix for the review finding: the
// terminal WS route reads its target session's cwd from a `?session=` query
// param, so ownerGuard's session-query check must run in that chain too, not
// just for the /sessions/:id path pattern. A plain (non-upgrade) GET is
// enough to prove the guard fires before any WebSocket upgrade is attempted.
func TestTerminalWSHonoursOwnerGuard(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	w := doAs(r, "tb", "GET", "/api/terminal/ws?session="+aliceSID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob must not resolve alice's session via the terminal route: got %d %s", w.Code, w.Body)
	}
	w2 := doAs(r, "ta", "GET", "/api/terminal/ws?session="+aliceSID)
	if w2.Code == http.StatusNotFound {
		t.Fatalf("alice must not be refused her own session: got %d %s", w2.Code, w2.Body)
	}
}
