package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/lessons"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/sessions"
)

func saveTestLesson(t *testing.T, root, agent string) lessons.Lesson {
	t.Helper()
	l := lessons.Lesson{
		Agent: agent, Tool: "Edit", Signature: lessons.Signature("Edit", "old_string not found"),
		Cue: "old_string not found", Text: "Read the file again before editing.", Worked: "Read then Edit",
		State: lessons.StatePrompt, Seen: 4, Hinted: 3, Recovered: 2, Repeated: 1,
		Created: time.Unix(1700000000, 0).UTC(), LastSeen: time.Unix(1700000100, 0).UTC(),
	}
	if err := lessons.In(root).Save(l); err != nil {
		t.Fatal(err)
	}
	l.ID = l.Signature[:12]
	return l
}

type lessonList struct {
	Lessons []map[string]any `json:"lessons"`
}

func TestLessonsRoutesListAndDelete(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})
	l := saveTestLesson(t, paths.ConfigWriteDir(), "coder")

	w := memReq(t, engine, http.MethodGet, "/api/agents/coder/lessons", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body)
	}
	var list lessonList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Lessons) != 1 {
		t.Fatalf("lessons = %+v, want 1", list.Lessons)
	}
	g := list.Lessons[0]
	if g["id"] != l.ID || g["tool"] != "Edit" || g["cue"] != "old_string not found" ||
		g["text"] != l.Text || g["worked"] != "Read then Edit" || g["state"] != "prompt" ||
		g["seen"] != float64(4) || g["hinted"] != float64(3) || g["recovered"] != float64(2) ||
		g["repeated"] != float64(1) || g["created"] != "2023-11-14T22:13:20Z" || g["last_seen"] != "2023-11-14T22:15:00Z" {
		t.Fatalf("lesson = %+v", g)
	}

	if w := memReq(t, engine, http.MethodDelete, "/api/agents/coder/lessons/"+l.ID, ""); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body)
	}
	if w := memReq(t, engine, http.MethodDelete, "/api/agents/coder/lessons/"+l.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", w.Code)
	}
	w = memReq(t, engine, http.MethodGet, "/api/agents/coder/lessons", "")
	if w.Code != http.StatusOK || w.Body.String() != `{"lessons":[]}` {
		t.Fatalf("after delete: %d %s", w.Code, w.Body)
	}
}

func TestLessonsRoutesEmptyAndInvalid(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})
	if w := memReq(t, engine, http.MethodGet, "/api/agents/nobody/lessons", ""); w.Code != http.StatusOK || w.Body.String() != `{"lessons":[]}` {
		t.Fatalf("unknown agent: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/api/agents/Bad-Name/lessons", "/api/agents/..%2Fx/lessons"} {
		if w := memReq(t, engine, http.MethodGet, p, ""); w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", p, w.Code)
		}
	}
	if w := memReq(t, engine, http.MethodGet, "/api/agents/Bad-Name/lessons", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad agent = %d, want 400", w.Code)
	}
	if w := memReq(t, engine, http.MethodDelete, "/api/agents/coder/lessons/xyz", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d, want 400", w.Code)
	}
}

func TestLessonsRoutesAreOwnerScopedInCookieMode(t *testing.T) {
	engine, _, _, _ := newCookieTestEngine(t)
	l := saveTestLesson(t, userRoot("alice"), "coder")

	var list lessonList
	w := doAs(engine, "ta", http.MethodGet, "/api/agents/coder/lessons")
	if w.Code != http.StatusOK {
		t.Fatalf("alice GET = %d: %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Lessons) != 1 {
		t.Fatalf("alice sees %+v, want 1", list.Lessons)
	}
	list = lessonList{}
	w = doAs(engine, "tb", http.MethodGet, "/api/agents/coder/lessons")
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list.Lessons) != 0 {
		t.Fatalf("bob sees %d %+v, want none", w.Code, list.Lessons)
	}
	if w := doAs(engine, "tb", http.MethodDelete, "/api/agents/coder/lessons/"+l.ID); w.Code != http.StatusNotFound {
		t.Fatalf("bob DELETE = %d, want 404", w.Code)
	}
	if w := doAs(engine, "ta", http.MethodDelete, "/api/agents/coder/lessons/"+l.ID); w.Code != http.StatusOK {
		t.Fatalf("alice DELETE = %d", w.Code)
	}
}
