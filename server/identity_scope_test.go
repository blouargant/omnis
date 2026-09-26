package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/scheduler"
	"github.com/blouargant/omnis/internal/sessions"
)

// newCookieTestEngine builds the real router in cookie mode with a fake
// validator (tokens "ta"→alice, "tb"→bob) and one session per user.
func newCookieTestEngine(t *testing.T) (*gin.Engine, serverDeps, string, string) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	d := serverDeps{
		Registry:   sessions.NewEmptyRegistry(),
		WebDir:     t.TempDir(),
		Cookie:     testCookieAuth(),
		PushEvents: newSessionPushBroadcaster(),
		RunGuard:   newSessionRunGuard(),
		LiveTurns:  newLiveTurnRegistry(),
		rootCtx:    t.Context(),
	}
	a := d.Registry.NewFor("alice", "system")
	b := d.Registry.NewFor("bob", "system")
	return newEngine(d), d, a.ID, b.ID
}

func doAs(r http.Handler, tok, method, path string) *httptest.ResponseRecorder {
	return doWithBody(r, tok, method, path, "{}")
}

func doWithBody(r http.Handler, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Every route carrying a session id must refuse another user's id with 404 —
// enumerated from the router so a future route cannot slip past.
func TestOwnerGuardCoversEveryIDRoute(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	n := 0
	for _, rt := range r.Routes() {
		if !strings.Contains(rt.Path, "/sessions/:id") {
			continue
		}
		path := strings.NewReplacer(":id", aliceSID, ":qid", "q1", ":runID", "r1").Replace(rt.Path)
		w := doAs(r, "tb", rt.Method, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s as bob: got %d, want 404", rt.Method, rt.Path, w.Code)
		}
		n++
	}
	t.Logf("exercised %d /sessions/:id routes", n)
	if n < 30 {
		t.Fatalf("expected to exercise the whole /sessions/:id family, only saw %d routes", n)
	}
}

// The owner itself is not refused by the guard (sanity: the guard is not a
// blanket 404).
func TestOwnerGuardLetsOwnerThrough(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	if w := doAs(r, "ta", "GET", "/api/sessions/"+aliceSID+"/goal"); w.Code == http.StatusNotFound {
		t.Fatalf("alice refused on her own session: %d %s", w.Code, w.Body)
	}
}

// A `session` query parameter naming another user's session is refused too.
func TestOwnerGuardSessionQueryParam(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	for _, p := range []string{"/api/complete?line=ls&session=" + aliceSID, "/api/file?path=/etc/hostname&session=" + aliceSID} {
		if w := doAs(r, "tb", "GET", p); w.Code != http.StatusNotFound {
			t.Errorf("GET %s as bob: got %d, want 404", p, w.Code)
		}
	}
}

func TestSessionListsAreScoped(t *testing.T) {
	r, _, aliceSID, bobSID := newCookieTestEngine(t)
	for _, path := range []string{"/api/sessions", "/api/sessions?limit=50", "/api/session-ids"} {
		body := doAs(r, "ta", "GET", path).Body.String()
		if !strings.Contains(body, aliceSID) || strings.Contains(body, bobSID) {
			t.Errorf("%s as alice leaked or missed: %s", path, body)
		}
	}
}

// Search must not surface another user's conversation, even when both match.
func TestSessionSearchIsScoped(t *testing.T) {
	r, _, aliceSID, bobSID := newCookieTestEngine(t)
	for _, id := range []string{aliceSID, bobSID} {
		if err := sessions.AppendConversationTurn(id, "tell me about zanzibar", "zanzibar is an island"); err != nil {
			t.Fatal(err)
		}
	}
	var out struct{ Results []map[string]any }
	w := doAs(r, "ta", "GET", "/api/search/sessions?q=zanzibar")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	sawAlice := false
	for _, res := range out.Results {
		switch res["session_id"] {
		case bobSID:
			t.Errorf("search leaked bob's session to alice")
		case aliceSID:
			sawAlice = true
		}
	}
	if !sawAlice {
		t.Errorf("search missed alice's own session: %s", w.Body)
	}
}

func TestSchedulesAreScoped(t *testing.T) {
	_, d, aliceSID, _ := newCookieTestEngine(t)
	d.Scheduler = scheduler.New(filepath.Join(t.TempDir(), "s.json"))
	r := newEngine(d)
	req := `{"kind":"schedule","spec":"every 2h","prompt":"hello"}`
	wa := doWithBody(r, "ta", "POST", "/api/schedules", req)
	if wa.Code != http.StatusCreated {
		t.Fatalf("create as alice: %d %s", wa.Code, wa.Body)
	}
	var job struct {
		ID     string
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(wa.Body.Bytes(), &job)
	if job.UserID != "alice" {
		t.Errorf("schedule owner = %q, want alice", job.UserID)
	}
	if body := doAs(r, "tb", "GET", "/api/schedules").Body.String(); strings.Contains(body, job.ID) {
		t.Fatalf("bob sees alice's schedule: %s", body)
	}
	if body := doAs(r, "ta", "GET", "/api/schedules").Body.String(); !strings.Contains(body, job.ID) {
		t.Fatalf("alice misses her own schedule: %s", body)
	}
	for _, p := range []string{"/api/schedules/" + job.ID, "/api/schedules/" + job.ID + "/run", "/api/schedules/" + job.ID + "/history", "/api/schedules/" + job.ID + "/history/r1"} {
		for _, m := range []string{"PATCH", "POST", "DELETE"} {
			w := doAs(r, "tb", m, p)
			if w.Code == http.StatusOK || w.Code == http.StatusCreated || w.Code == http.StatusNoContent {
				t.Errorf("%s %s as bob: %d", m, p, w.Code)
			}
		}
	}
	// Every /schedules/:id route answers 404 to a non-owner.
	for _, rt := range r.Routes() {
		if !strings.Contains(rt.Path, "/schedules/:id") {
			continue
		}
		path := strings.NewReplacer(":id", job.ID, ":runID", "r1").Replace(rt.Path)
		if w := doAs(r, "tb", rt.Method, path); w.Code != http.StatusNotFound {
			t.Errorf("%s %s as bob: got %d, want 404", rt.Method, rt.Path, w.Code)
		}
	}
	// Bob cannot bind a loop/schedule to alice's session.
	loop := `{"kind":"loop","spec":"every 2h","prompt":"x","session_id":"` + aliceSID + `"}`
	if w := doWithBody(r, "tb", "POST", "/api/schedules", loop); w.Code != http.StatusNotFound {
		t.Errorf("bob binding a loop to alice's session: %d %s", w.Code, w.Body)
	}
}

// Collection counts only count the caller's own sessions.
func TestCollectionCountsAreScoped(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	var out struct {
		Collections []struct {
			Name  string
			Count int
		}
	}
	w := doAs(r, "ta", "GET", "/api/collections")
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Collections) == 0 {
		t.Fatalf("collections: %d %s", w.Code, w.Body)
	}
	if got := out.Collections[0].Count; got != 1 {
		t.Errorf("General count as alice = %d, want 1 (bob's session must not count)", got)
	}
}

// The collection-memory distiller only reads the caller's own chats.
func TestCollectionMaterialIsScoped(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	d := serverDeps{Registry: reg}
	for owner, text := range map[string]string{"alice": "alice-secret", "bob": "bob-secret"} {
		m := reg.NewFor(owner, "system")
		m.Collection = "Work"
		m.Turns = 1
		if err := sessions.AppendConversationTurn(m.ID, text, "ok"); err != nil {
			t.Fatal(err)
		}
	}
	mat := gatherCollectionMaterialFor(d, "Work", "alice")
	if !strings.Contains(mat, "alice-secret") || strings.Contains(mat, "bob-secret") {
		t.Fatalf("material for alice: %q", mat)
	}
}
