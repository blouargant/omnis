package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Static assets must be revalidated on every load. Without a Cache-Control
// header browsers cache them heuristically, and the CSS partials pulled in by
// `@import` from styles.css carry no ?v= query — so an upgrade shipped a new
// index.html (with the "Tab ⇥" badge) against a stale composer.css (without the
// rule that hides it). no-cache keeps the 304 revalidation cheap.
func TestAssetsAreServedWithNoCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "css", "features"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "css", "features", "composer.css"), []byte("a{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(serverDeps{WebDir: dir, rootCtx: context.Background()})
	req := httptest.NewRequest(http.MethodGet, "/assets/css/features/composer.css", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}
