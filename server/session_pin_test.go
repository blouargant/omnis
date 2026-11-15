package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestPinSession drives the real router: POST /api/sessions/:id/pin sets the
// flag, ?pinned=true lists only pinned sessions, ?pinned=false (a collection
// group) leaves them out, and a pinned session is not counted in its
// collection's `active` count.
func TestPinSession(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	now := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		reg.Add(&sessions.SessionMeta{ID: id, Title: id, CreatedAt: now, LastUsedAt: now})
	}
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	ids := func(query string) []string {
		t.Helper()
		w := do(http.MethodGet, "/api/sessions?"+query, "")
		var resp struct {
			Sessions []sessions.SessionMeta `json:"sessions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var out []string
		for _, s := range resp.Sessions {
			out = append(out, s.ID)
		}
		return out
	}

	if w := do(http.MethodPost, "/api/sessions/b/pin", `{"pinned":true}`); w.Code != http.StatusOK {
		t.Fatalf("pin: status %d %s", w.Code, w.Body.String())
	}
	if m, _ := reg.Get("b"); !m.Pinned {
		t.Fatal("b should be pinned")
	}
	if got := ids("archived=false&pinned=true&limit=50"); len(got) != 1 || got[0] != "b" {
		t.Fatalf("pinned=true: got %v, want [b]", got)
	}
	if got := ids("collection=General&archived=false&pinned=false&limit=50"); len(got) != 2 {
		t.Fatalf("pinned=false: got %v, want a and c", got)
	}
	if got := ids("archived=false&limit=50"); len(got) != 3 {
		t.Fatalf("no pinned filter: got %v, want all three", got)
	}

	w := do(http.MethodGet, "/api/collections", "")
	var cols struct {
		Collections []struct {
			Name   string `json:"name"`
			Active int    `json:"active"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cols); err != nil {
		t.Fatalf("collections decode: %v", err)
	}
	if len(cols.Collections) == 0 || cols.Collections[0].Active != 2 {
		t.Fatalf("General active count: %+v, want 2", cols.Collections)
	}

	if w := do(http.MethodPost, "/api/sessions/b/pin", `{"pinned":false}`); w.Code != http.StatusOK {
		t.Fatalf("unpin: status %d", w.Code)
	}
	if got := ids("archived=false&pinned=true&limit=50"); len(got) != 0 {
		t.Fatalf("after unpin: got %v, want none", got)
	}
	if w := do(http.MethodPost, "/api/sessions/nope/pin", `{"pinned":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown session: status %d, want 404", w.Code)
	}
}
