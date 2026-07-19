package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestListSessionsPaginated drives the real router and locks in the paginated
// GET /api/sessions contract: legacy full list when `limit` is absent, and
// offset/limit/collection/archived/q/sort filtering + a `total` count when it is
// present. See docs/superpowers/specs/2026-07-19-session-list-pagination-design.md.
func TestListSessionsPaginated(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	// A known collection so effective-collection folding keeps "Work" sessions in
	// Work (and a blank collection folds to General).
	if _, _, err := sessions.AddCollection("Work"); err != nil {
		t.Fatal(err)
	}

	reg := sessions.NewEmptyRegistry()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	add := func(id, title, coll string, archived bool, ageMin int) {
		reg.Add(&sessions.SessionMeta{
			ID:         id,
			Title:      title,
			Collection: coll,
			Archived:   archived,
			CreatedAt:  base.Add(time.Duration(-ageMin) * time.Minute),
			LastUsedAt: base.Add(time.Duration(-ageMin) * time.Minute),
		})
	}
	// 60 active General sessions: recent order is g00 (newest) … g59 (oldest).
	for i := 0; i < 60; i++ {
		add(fmt.Sprintf("g%02d", i), fmt.Sprintf("General chat %02d", i), "", false, i)
	}
	// Work sessions (older than every General one so they never mix into a page).
	add("w1", "Work azure upgrade", "Work", false, 100)
	add("w2", "Work planning", "Work", false, 101)
	add("wArch", "Work archived", "Work", true, 102)
	add("hid", "hidden util", "", false, 103)
	reg.SetHidden("hid", true)

	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	type pageResp struct {
		Sessions []sessions.SessionMeta `json:"sessions"`
		Total    int                    `json:"total"`
		Offset   int                    `json:"offset"`
		Limit    int                    `json:"limit"`
	}
	get := func(query string) pageResp {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/sessions?"+query, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET ?%s: status %d, body %s", query, w.Code, w.Body.String())
		}
		var p pageResp
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode ?%s: %v", query, err)
		}
		return p
	}

	// Legacy path: no `limit` → full non-hidden list (63), no total field.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		var resp struct {
			Sessions []sessions.SessionMeta `json:"sessions"`
			Total    *int                   `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("legacy decode: %v", err)
		}
		if len(resp.Sessions) != 63 { // 60 general + 2 active work + 1 archived work
			t.Fatalf("legacy list: got %d sessions, want 63", len(resp.Sessions))
		}
		if resp.Total != nil {
			t.Fatalf("legacy list must not carry total, got %d", *resp.Total)
		}
	}

	// First page: General active, recent, 50.
	p := get("collection=General&archived=false&sort=recent&offset=0&limit=50")
	if p.Total != 60 {
		t.Fatalf("General total: got %d, want 60", p.Total)
	}
	if len(p.Sessions) != 50 {
		t.Fatalf("page 1 len: got %d, want 50", len(p.Sessions))
	}
	if p.Sessions[0].ID != "g00" {
		t.Fatalf("recent order: first id %q, want g00", p.Sessions[0].ID)
	}

	// Next 5.
	p2 := get("collection=General&archived=false&sort=recent&offset=50&limit=5")
	if len(p2.Sessions) != 5 || p2.Sessions[0].ID != "g50" {
		t.Fatalf("page 2: len %d first %q, want 5 & g50", len(p2.Sessions), firstID(p2.Sessions))
	}

	// Tail: only 5 remain after offset 55 even though limit=50.
	p3 := get("collection=General&archived=false&sort=recent&offset=55&limit=50")
	if len(p3.Sessions) != 5 {
		t.Fatalf("tail page: got %d, want 5", len(p3.Sessions))
	}

	// Offset past the end → empty page, real total.
	p4 := get("collection=General&archived=false&offset=999&limit=50")
	if len(p4.Sessions) != 0 || p4.Total != 60 {
		t.Fatalf("past-end page: len %d total %d, want 0 & 60", len(p4.Sessions), p4.Total)
	}

	// Collection filter: Work active only.
	pw := get("collection=Work&archived=false&limit=50")
	if pw.Total != 2 {
		t.Fatalf("Work active total: got %d, want 2", pw.Total)
	}

	// Archived Work.
	pa := get("collection=Work&archived=true&limit=50")
	if pa.Total != 1 || firstID(pa.Sessions) != "wArch" {
		t.Fatalf("Work archived: total %d first %q, want 1 & wArch", pa.Total, firstID(pa.Sessions))
	}

	// Search q across titles.
	pq := get("collection=Work&archived=false&q=azure&limit=50")
	if pq.Total != 1 || firstID(pq.Sessions) != "w1" {
		t.Fatalf("q=azure: total %d first %q, want 1 & w1", pq.Total, firstID(pq.Sessions))
	}

	// az sort — ascending by title within General.
	paz := get("collection=General&archived=false&sort=az&offset=0&limit=3")
	if len(paz.Sessions) != 3 || paz.Sessions[0].Title != "General chat 00" {
		t.Fatalf("az sort: first title %q, want 'General chat 00'", firstTitle(paz.Sessions))
	}

	// Slim id list (/api/session-ids) for boot layout validation: every non-hidden
	// id, hidden excluded. Also proves the route registers without colliding with
	// the /api/sessions/:id wildcard (newEngine would panic otherwise).
	{
		req := httptest.NewRequest(http.MethodGet, "/api/session-ids", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("session-ids: status %d, body %s", w.Code, w.Body.String())
		}
		var resp struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("session-ids decode: %v", err)
		}
		if len(resp.IDs) != 63 {
			t.Fatalf("session-ids: got %d, want 63 (hidden excluded)", len(resp.IDs))
		}
		for _, id := range resp.IDs {
			if id == "hid" {
				t.Fatalf("session-ids leaked the hidden session")
			}
		}
	}
}

func firstID(ss []sessions.SessionMeta) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0].ID
}
func firstTitle(ss []sessions.SessionMeta) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0].Title
}
