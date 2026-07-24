package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestHandleFork_MarksFleetExperiment verifies that forking a session whose
// squad is the Fleet coordinator squad marks the new (forked) session as a
// fleet experiment, while forking a session on any other squad leaves the
// flag unset. See server/fork_rewind.go handleFork.
func TestHandleFork_MarksFleetExperiment(t *testing.T) {
	cases := []struct {
		name     string
		srcSquad string
		want     bool
	}{
		{name: "fleet squad forks as an experiment", srcSquad: "Fleet", want: true},
		{name: "fleet squad name is case-insensitive", srcSquad: "fleet", want: true},
		{name: "non-fleet squad is untouched", srcSquad: "system", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIS_HOME", t.TempDir())
			bashCwd = newBashCwdStore()

			reg := sessions.NewEmptyRegistry()
			src := reg.New(tc.srcSquad)
			if err := sessions.AppendConversationTurn(src.ID, "hello", "hi there"); err != nil {
				t.Fatalf("seed source turn: %v", err)
			}

			d := serverDeps{
				Registry: reg,
				RunGuard: newSessionRunGuard(),
				rootCtx:  context.Background(),
			}

			r := gin.New()
			r.POST("/api/sessions/:id/fork", handleFork(d))

			req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+src.ID+"/fork", strings.NewReader(`{"full": true}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Fatalf("fork: got status %d, body %s", w.Code, w.Body.String())
			}
			var resp struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode fork response: %v", err)
			}

			// In-memory registry reflects the flag right away.
			forkMeta, ok := reg.Get(resp.SessionID)
			if !ok {
				t.Fatalf("forked session %q missing from registry", resp.SessionID)
			}
			if forkMeta.FleetExperiment != tc.want {
				t.Errorf("in-memory FleetExperiment = %v, want %v", forkMeta.FleetExperiment, tc.want)
			}

			// handleFork also persists via the synchronous
			// sessions.SetConversationFleetExperiment call (mirroring the
			// bashCwd persistence pattern), so the conversation file already
			// reflects the flag by the time the handler returns.
			f, err := sessions.LoadConversationFile(resp.SessionID)
			if err != nil {
				t.Fatalf("load fork conversation: %v", err)
			}
			if f.FleetExperiment != tc.want {
				t.Errorf("persisted FleetExperiment = %v, want %v", f.FleetExperiment, tc.want)
			}
		})
	}
}
