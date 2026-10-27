package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// The sidebar's collection tree shows, on each group header, how many chats the
// group lists — and the group lists only non-archived chats. `count` includes
// archived ones, so it would read "14" above 4 rows; `active` must not.
func TestListCollectionsActiveExcludesArchived(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	reg := sessions.NewEmptyRegistry()
	// Fields are set directly: the registry setters persist from a goroutine,
	// which would race the temp dir's cleanup.
	add := func(collection string, archived, hidden bool) {
		m := reg.New("")
		m.Collection = collection
		m.Archived = archived
		m.Hidden = hidden
	}
	add("Acme", false, false)
	add("Acme", true, false)
	add("", false, false)
	add("", true, false)
	add("Gone", false, false) // unknown collection folds into General
	add("", false, true)      // hidden sessions are never counted

	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/collections", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/collections status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Collections []struct {
			Name   string `json:"name"`
			Count  int    `json:"count"`
			Active int    `json:"active"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int{}
	for _, c := range body.Collections {
		got[c.Name] = [2]int{c.Count, c.Active}
	}
	if g := got[sessions.GeneralCollection]; g != [2]int{3, 2} {
		t.Fatalf("General = count %d active %d, want count 3 active 2", g[0], g[1])
	}
	if a := got["Acme"]; a != [2]int{2, 1} {
		t.Fatalf("Acme = count %d active %d, want count 2 active 1", a[0], a[1])
	}
}
