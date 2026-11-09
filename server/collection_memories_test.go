package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/sessions"
)

func memReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestMemoryRoutesRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})

	type putResp struct {
		Memory  collmem.Memory `json:"memory"`
		Created bool           `json:"created"`
	}
	put := func(body string) putResp {
		t.Helper()
		w := memReq(t, engine, http.MethodPut, "/api/collections/General/memories/no-force-push", body)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT = %d: %s", w.Code, w.Body)
		}
		var r putResp
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := put(`{"description":"never force-push","type":"feedback","body":"Do not force-push to main."}`)
	if !r.Created || r.Memory.Name != "no-force-push" || r.Memory.Type != "feedback" {
		t.Fatalf("first PUT = %+v, want created feedback no-force-push", r)
	}
	r = put(`{"description":"never force-push","type":"feedback","body":"Never force-push to any shared branch."}`)
	if r.Created {
		t.Fatalf("second PUT created = true, want false")
	}

	// "general" in any case addresses the same memory.
	w := memReq(t, engine, http.MethodGet, "/api/collections/general/memories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body)
	}
	var list struct {
		Memories []collmem.Memory `json:"memories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Memories) != 1 || list.Memories[0].Body != "Never force-push to any shared branch." {
		t.Fatalf("GET memories = %+v, want one with the new body", list.Memories)
	}

	if w := memReq(t, engine, http.MethodPut, "/api/collections/General/memories/Bad%20Name!", `{"description":"d","type":"feedback","body":"b"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("PUT bad name = %d %s, want 400 with error", w.Code, w.Body)
	}
	if w := memReq(t, engine, http.MethodPut, "/api/collections/General/memories/other", `{"description":"d","type":"note","body":"b"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("PUT bad type = %d %s, want 400 with error", w.Code, w.Body)
	}
	if w := memReq(t, engine, http.MethodGet, "/api/collections/Nope/memories", ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET unknown collection = %d, want 404", w.Code)
	}

	if w := memReq(t, engine, http.MethodDelete, "/api/collections/General/memories/no-force-push", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("DELETE = %d %s, want 200 ok", w.Code, w.Body)
	}
	if w := memReq(t, engine, http.MethodDelete, "/api/collections/General/memories/no-force-push", ""); w.Code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", w.Code)
	}
}

// A named collection is addressed by its canonical stored name, so the route
// and the agent tools (which use the session's normalised collection) write the
// same directory.
func TestMemoryRoutesUseCanonicalCollectionName(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Proxy"); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})
	if w := memReq(t, engine, http.MethodPut, "/api/collections/proxy/memories/rate-limit", `{"description":"d","type":"project","body":"b"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	if _, ok := collmem.In(paths.ConfigWriteDir()).Get("Proxy", "rate-limit"); !ok {
		t.Fatal("memory not stored under the canonical collection name")
	}
}

// A collection that still has its legacy memory.md shows it as imported-notes
// on the first read, and a memory added before any model turn does not strand
// it (the first Save creates the memory directory, after which nothing would
// import the legacy file any more). The collection list counts it too.
func TestMemoryRoutesMigrateLegacyMemory(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Proxy"); err != nil {
		t.Fatal(err)
	}
	legacyDir := filepath.Join(paths.ConfigWriteDir(), "collections", "Proxy")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "memory.md"), []byte("The proxy repo is gitlab.example/proxy."), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})
	list := func() []collmem.Memory {
		t.Helper()
		w := memReq(t, engine, http.MethodGet, "/api/collections/Proxy/memories", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET = %d: %s", w.Code, w.Body)
		}
		var r struct {
			Memories []collmem.Memory `json:"memories"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r.Memories
	}
	if m := list(); len(m) != 1 || m[0].Name != "imported-notes" || !strings.Contains(m[0].Body, "gitlab.example/proxy") {
		t.Fatalf("first GET = %+v, want the imported notes", m)
	}
	if w := memReq(t, engine, http.MethodPut, "/api/collections/Proxy/memories/no-force-push", `{"description":"d","type":"feedback","body":"Never force-push."}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	if m := list(); len(m) != 2 {
		t.Fatalf("after PUT = %+v, want imported-notes kept beside the new memory", m)
	}
}

// The first write must import the legacy file too, not only a read.
func TestMemoryPutFirstKeepsLegacyMemory(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Proxy"); err != nil {
		t.Fatal(err)
	}
	legacyDir := filepath.Join(paths.ConfigWriteDir(), "collections", "Proxy")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "memory.md"), []byte("Old notes."), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})
	if w := memReq(t, engine, http.MethodPut, "/api/collections/Proxy/memories/rate-limit", `{"description":"d","type":"project","body":"b"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	if m, ok := collmem.In(paths.ConfigWriteDir()).Get("Proxy", "imported-notes"); !ok || m.Body != "Old notes." {
		t.Fatalf("legacy notes stranded by the first PUT: %+v %v", m, ok)
	}
	w := memReq(t, engine, http.MethodGet, "/api/collections", "")
	if !strings.Contains(w.Body.String(), `"name":"Proxy"`) || !strings.Contains(w.Body.String(), `"memories":2`) {
		t.Fatalf("collections = %s, want Proxy with 2 memories", w.Body)
	}
}

func TestMemoryRoutesAreOwnerScopedInCookieMode(t *testing.T) {
	engine, _, _, _ := newCookieTestEngine(t)
	if w := doWithBody(engine, "ta", http.MethodPut, "/api/collections/General/memories/alice-fact", `{"description":"d","type":"user","body":"Alice prefers French."}`); w.Code != http.StatusOK {
		t.Fatalf("alice PUT = %d: %s", w.Code, w.Body)
	}
	w := doAs(engine, "tb", http.MethodGet, "/api/collections/General/memories")
	if w.Code != http.StatusOK {
		t.Fatalf("bob GET = %d: %s", w.Code, w.Body)
	}
	var list struct {
		Memories []collmem.Memory `json:"memories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Memories) != 0 {
		t.Fatalf("bob sees %+v, want no memories", list.Memories)
	}
	path := filepath.Join(paths.ConfigWriteDir(), "users", "alice", "general-memory", "alice-fact.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("alice's memory not under her root: %v", err)
	}
}

func recvPush(t *testing.T, ch chan pushMsg) pushMsg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no push message")
	}
	return pushMsg{}
}

func TestMemoryChangedIsBroadcast(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	d := serverDeps{Registry: sessions.NewEmptyRegistry(), PushEvents: newSessionPushBroadcaster(), rootCtx: context.Background()}
	engine := newEngine(d)
	ch := d.PushEvents.subscribeAll()
	defer d.PushEvents.unsubscribeAll(ch)

	if w := memReq(t, engine, http.MethodPut, "/api/collections/General/memories/x", `{"description":"d","type":"project","body":"b"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	m := recvPush(t, ch)
	if m.Event != "memory_changed" || m.SID != "" {
		t.Fatalf("push = %+v, want session-less memory_changed", m)
	}
	if m.Data["collection"] != "General" || m.Data["name"] != "x" || m.Data["op"] != "created" {
		t.Fatalf("push data = %+v, want {General x created}", m.Data)
	}

	uninstall := installMemoryChangeHook(d)
	defer uninstall()
	newMemoryChangeHook(d)("sess-1", "Proxy", "y", "updated")
	m = recvPush(t, ch)
	if m.Event != "memory_changed" || m.SID != "sess-1" {
		t.Fatalf("hook push = %+v, want memory_changed for sess-1", m)
	}
	if m.Data["collection"] != "Proxy" || m.Data["name"] != "y" || m.Data["op"] != "updated" {
		t.Fatalf("hook push data = %+v, want {Proxy y updated}", m.Data)
	}
	// A tool write in General reports the collection as "General".
	newMemoryChangeHook(d)("sess-1", "", "z", "deleted")
	if m = recvPush(t, ch); m.Data["collection"] != "General" || m.Data["op"] != "deleted" {
		t.Fatalf("General hook push data = %+v", m.Data)
	}
}

func TestUnattendedTurnsAreMemoryReadOnly(t *testing.T) {
	for _, k := range []string{"mailbox", "a2a", "spawn_result"} {
		if !memoryReadOnlyFor(k) {
			t.Errorf("memoryReadOnlyFor(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"interactive", "schedule", "loop", "background", "durable_resume"} {
		if memoryReadOnlyFor(k) {
			t.Errorf("memoryReadOnlyFor(%q) = true, want false", k)
		}
	}
}
