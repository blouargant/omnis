package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/bg"
	"github.com/blouargant/omnis/internal/sessions"
)

// TestSessionTaskRoutes drives the real router: a session's background tasks
// are listed, stopped and read through the queue keyed by (its user, its id) —
// the same key the agent's own bash_background / monitor tools use.
func TestSessionTaskRoutes(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	m := reg.New("default")

	queues := bg.NewSessionQueues(8)
	prev := sessionBgQueue
	sessionBgQueue = func(_ serverDeps, userID, sessionID string) *bg.Queue {
		return queues.For(userID, sessionID)
	}
	t.Cleanup(func() { sessionBgQueue = prev })

	q := queues.For(m.UserID, m.ID)
	long := q.Start("sleeper", "sleep 30", time.Minute)
	quick := q.Start("hello", "echo hello", time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, info, _ := q.Output(quick); info.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the quick task never completed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})
	call := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	base := "/api/sessions/" + m.ID + "/tasks"

	rec := call(http.MethodGet, base)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Tasks []bg.TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, ti := range list.Tasks {
		status[ti.ID] = ti.Status
	}
	if len(list.Tasks) != 2 || status[long] != "running" || status[quick] != "completed" {
		t.Fatalf("tasks = %+v", list.Tasks)
	}

	rec = call(http.MethodGet, base+"/"+quick+"/output")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("output: status %d: %s", rec.Code, rec.Body.String())
	}

	if rec = call(http.MethodPost, base+"/"+long+"/cancel"); rec.Code != http.StatusOK {
		t.Fatalf("cancel: status %d: %s", rec.Code, rec.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, info, _ := q.Output(long); info.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stopped task never reported cancelled")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if rec = call(http.MethodPost, base+"/nope/cancel"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: status %d, want 404", rec.Code)
	}
	if rec = call(http.MethodGet, "/api/sessions/missing/tasks"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown session: status %d, want 404", rec.Code)
	}
}
