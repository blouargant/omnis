package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/permissions"
	"github.com/blouargant/omnis/internal/sessions"
)

type pmFixture struct {
	t      *testing.T
	reg    *sessions.Registry
	modes  *permissions.ModeStore
	bcast  *sessionPushBroadcaster
	deps   serverDeps
	engine http.Handler
}

func newPMFixture(t *testing.T) *pmFixture {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	bashCwd = newBashCwdStore()
	f := &pmFixture{t: t, reg: sessions.NewEmptyRegistry(), modes: permissions.NewModeStore(), bcast: newSessionPushBroadcaster()}
	f.deps = serverDeps{Registry: f.reg, PermModes: f.modes, PushEvents: f.bcast, RunGuard: newSessionRunGuard(), rootCtx: context.Background()}
	f.engine = newEngine(f.deps)
	return f
}

func (f *pmFixture) do(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func (f *pmFixture) seed() string {
	f.t.Helper()
	m := f.reg.New("default")
	if err := sessions.AppendConversationTurn(m.ID, "hi", "yo"); err != nil {
		f.t.Fatal(err)
	}
	return m.ID
}

func decodePM(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return m
}

func TestPermissionModeRoundTrip(t *testing.T) {
	f := newPMFixture(t)
	id := f.seed()
	base := "/api/sessions/" + id + "/permission-mode"

	if w := f.do(http.MethodPut, base, `{"mode":"plan"}`); w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	got := decodePM(t, f.do(http.MethodGet, base, ""))
	if got["mode"] != "plan" || got["effective"] != "plan" || got["config_mode"] == nil || got["bypass_disabled"] != false {
		t.Fatalf("GET payload: %v", got)
	}
	file, _ := sessions.LoadConversationFile(id)
	if file.PermissionMode != "plan" {
		t.Fatalf("file mode %q", file.PermissionMode)
	}
	// session list rows carry it
	lw := f.do(http.MethodGet, "/api/sessions", "")
	if !strings.Contains(lw.Body.String(), `"permission_mode":"plan"`) {
		t.Fatalf("list lacks permission_mode: %s", lw.Body.String())
	}

	if w := f.do(http.MethodPut, base, `{"mode":""}`); w.Code != 200 {
		t.Fatalf("clear: %d", w.Code)
	}
	got = decodePM(t, f.do(http.MethodGet, base, ""))
	if got["mode"] != "" {
		t.Fatalf("not cleared: %v", got)
	}
	file, _ = sessions.LoadConversationFile(id)
	if file.PermissionMode != "" {
		t.Fatalf("file not cleared: %q", file.PermissionMode)
	}
}

func TestPermissionModeRejects(t *testing.T) {
	f := newPMFixture(t)
	id := f.seed()
	base := "/api/sessions/" + id + "/permission-mode"
	if w := f.do(http.MethodPut, base, `{"mode":"yolo"}`); w.Code != 400 {
		t.Fatalf("yolo: %d", w.Code)
	}
	f.modes.SetBypassDisabled(true)
	if w := f.do(http.MethodPut, base, `{"mode":"bypassPermissions"}`); w.Code != 403 {
		t.Fatalf("bypass: %d", w.Code)
	}
	if w := f.do(http.MethodPut, "/api/sessions/nope/permission-mode", `{"mode":"plan"}`); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	f.reg.SetArchived(id, true)
	if w := f.do(http.MethodPut, base, `{"mode":"plan"}`); w.Code != 409 {
		t.Fatalf("archived: %d", w.Code)
	}
}

func TestPermissionModeSurvivesReload(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := sessions.SaveConversationFile("reload-1", &sessions.ConversationFile{
		PermissionMode: "auto",
		Turns:          []sessions.ConversationTurn{{UserText: "a", AssistantText: "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	var found bool
	store := permissions.NewModeStore()
	for _, meta := range sessions.LoadPersistedSessions() {
		if meta.ID != "reload-1" {
			continue
		}
		found = true
		if meta.PermissionMode != "auto" {
			t.Fatalf("meta mode %q", meta.PermissionMode)
		}
		m, ok := permissions.ParseModeStrict(meta.PermissionMode)
		if !ok {
			t.Fatal("parse")
		}
		store.Set(meta.ID, m)
	}
	if !found {
		t.Fatal("session not loaded")
	}
	if m, ok := store.Get("reload-1"); !ok || m != permissions.ModeAuto {
		t.Fatalf("store: %v %v", m, ok)
	}
}

func TestForkInheritsPermissionMode(t *testing.T) {
	f := newPMFixture(t)
	id := f.seed()
	if w := f.do(http.MethodPut, "/api/sessions/"+id+"/permission-mode", `{"mode":"plan"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := f.do(http.MethodPost, "/api/sessions/"+id+"/fork", `{"full":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("fork: %d %s", w.Code, w.Body.String())
	}
	fid := decodePM(t, w)["session_id"].(string)
	if m, ok := f.modes.Get(fid); !ok || m != permissions.ModePlan {
		t.Fatalf("fork store mode: %v %v", m, ok)
	}
	file, _ := sessions.LoadConversationFile(fid)
	if file.PermissionMode != "plan" {
		t.Fatalf("fork file mode %q", file.PermissionMode)
	}
}

func TestImportDropsPermissionMode(t *testing.T) {
	f := newPMFixture(t)
	body := `{"title":"T","squad":"default","permission_mode":"plan","turns":[{"user_text":"hi","assistant_text":"yo"}]}`
	w := f.do(http.MethodPost, "/api/import/session", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	id := decodePM(t, w)["session_id"].(string)
	file, _ := sessions.LoadConversationFile(id)
	if file.PermissionMode != "" {
		t.Fatalf("import kept mode %q", file.PermissionMode)
	}
	if _, ok := f.modes.Get(id); ok {
		t.Fatal("store has override for imported session")
	}
}

func TestCreateSessionWithPermissionMode(t *testing.T) {
	f := newPMFixture(t)
	w := f.do(http.MethodPost, "/api/sessions", `{"permission_mode":"plan"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := decodePM(t, w)["session_id"].(string)
	if m, ok := f.modes.Get(id); !ok || m != permissions.ModePlan {
		t.Fatalf("store: %v %v", m, ok)
	}
	if w := f.do(http.MethodPost, "/api/sessions", `{"permission_mode":"yolo"}`); w.Code != 400 {
		t.Fatalf("invalid: %d", w.Code)
	}
	f.modes.SetBypassDisabled(true)
	if w := f.do(http.MethodPost, "/api/sessions", `{"permission_mode":"bypassPermissions"}`); w.Code != 403 {
		t.Fatalf("bypass: %d", w.Code)
	}
}

func TestPermissionModeChangedEvent(t *testing.T) {
	f := newPMFixture(t)
	id := f.seed()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(f.engine)
	defer srv.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.bcast.mu.RLock()
		n := len(f.bcast.all)
		f.bcast.mu.RUnlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no subscriber")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if w := f.do(http.MethodPut, "/api/sessions/"+id+"/permission-mode", `{"mode":"plan"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	sc := bufio.NewScanner(resp.Body)
	saw := false
	for sc.Scan() {
		line := sc.Text()
		if line == "event: permission_mode_changed" {
			saw = true
			continue
		}
		if saw && strings.HasPrefix(line, "data: ") {
			var p map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil {
				t.Fatal(err)
			}
			if p["session_id"] != id || p["mode"] != "plan" || p["effective"] != "plan" {
				t.Fatalf("payload %v", p)
			}
			return
		}
	}
	t.Fatal("event never arrived")
}

// Back-to-back PUT plan / PUT "" must leave the file without a mode: the disk
// write happens once, synchronously, so there is no goroutine ordering race.
func TestPermissionModeRapidChangesPersistLast(t *testing.T) {
	f := newPMFixture(t)
	id := f.seed()
	base := "/api/sessions/" + id + "/permission-mode"
	for i := 0; i < 50; i++ {
		f.do(http.MethodPut, base, `{"mode":"plan"}`)
		f.do(http.MethodPut, base, `{"mode":""}`)
	}
	time.Sleep(100 * time.Millisecond)
	file, _ := sessions.LoadConversationFile(id)
	if file.PermissionMode != "" {
		t.Fatalf("file holds %q after clear", file.PermissionMode)
	}
}

func TestCreateSessionWhitespaceModeIsEmpty(t *testing.T) {
	f := newPMFixture(t)
	w := f.do(http.MethodPost, "/api/sessions", `{"permission_mode":"   "}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	id := decodePM(t, w)["session_id"].(string)
	if _, ok := f.modes.Get(id); ok {
		t.Fatal("whitespace mode installed an override")
	}
}
