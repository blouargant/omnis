package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

func TestRecordProducedFileDedupsAndKeepsFirstTurn(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	id := "prod-sess"
	if err := sessions.RecordProducedFile(id, "/tmp/a.md"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendConversationTurn(id, "q", "r"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.RecordProducedFile(id, "/tmp/a.md"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.RecordProducedFile(id, "/tmp/b.pdf"); err != nil {
		t.Fatal(err)
	}
	got, err := sessions.LoadProducedFiles(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "/tmp/a.md" || got[0].Turn != 0 || got[1].Turn != 1 {
		t.Fatalf("produced = %+v", got)
	}
}

func TestTruncateAndForkKeepProducedBeforeTurn(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	id := "prod-trunc"
	_ = sessions.RecordProducedFile(id, "/tmp/early.md") // turn 0
	_ = sessions.AppendConversationTurn(id, "q1", "r1")
	_ = sessions.RecordProducedFile(id, "/tmp/late.md") // turn 1
	_ = sessions.AppendConversationTurn(id, "q2", "r2")

	if _, err := sessions.ForkConversation(id, "prod-fork", "", 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := sessions.LoadProducedFiles("prod-fork"); len(got) != 1 || got[0].Path != "/tmp/early.md" {
		t.Fatalf("fork produced = %+v", got)
	}
	if _, err := sessions.TruncateConversationTurns(id, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := sessions.LoadProducedFiles(id); len(got) != 1 || got[0].Path != "/tmp/early.md" {
		t.Fatalf("truncated produced = %+v", got)
	}
}

func TestProducedRouteListsFiles(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	m := reg.New("default")
	dir := t.TempDir()
	keep := filepath.Join(dir, "report.md")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.md")
	_ = sessions.RecordProducedFile(m.ID, keep)
	_ = sessions.RecordProducedFile(m.ID, gone)

	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/"+m.ID+"/produced", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Files []struct {
			Path   string `json:"path"`
			Name   string `json:"name"`
			Exists bool   `json:"exists"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Files) != 2 {
		t.Fatalf("files = %+v", body.Files)
	}
	byName := map[string]bool{}
	for _, f := range body.Files {
		byName[f.Name] = f.Exists
	}
	if !byName["report.md"] || byName["gone.md"] {
		t.Fatalf("exists flags wrong: %+v", body.Files)
	}
}

func TestProducedFromWriteResponse(t *testing.T) {
	if !producedByWrite("Write", map[string]any{"result": "wrote x", "created": true}) {
		t.Fatal("a Write that created the file must be recorded")
	}
	if producedByWrite("Write", map[string]any{"result": "wrote x"}) {
		t.Fatal("an overwrite must not be recorded")
	}
	if producedByWrite("Edit", map[string]any{"created": true}) {
		t.Fatal("only Write records produced files")
	}
}
