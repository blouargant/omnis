# Collection Memory Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-collection memory **size** control (small/medium/large) and an **auto-update** toggle (auto-commit distilled memory on idle, with a one-click revert) to the web-UI collection Context editor.

**Architecture:** Two per-collection scalars ride on the existing `collectionProfile` in `collections.json`. Size bounds the distiller's word target + output cap and drives a live editor word-counter (soft — never truncates typed text). Auto-update is a server-only worker that subscribes to the existing `EventSessionIndexNow` idle rail, distils the collection's recent chats when content changed + a min-interval elapsed, snapshots the prior memory to `memory.prev.md`, and commits — with a Revert route/marker. Injection (`collectionctx.Resolve`) stays size-unaware.

**Tech Stack:** Go (server, `internal/sessions`, `internal/collectionctx`, `agent`), vanilla JS/CSS web UI, JSON i18n catalogues + `make i18n`.

## Global Constraints

- **Design doc:** `docs/superpowers/specs/2026-07-20-collection-memory-size-and-autoupdate-design.md` — the authority for behaviour.
- **Word limits (verbatim):** small **200**, medium **350** (default), large **700**. `""` ⇒ medium.
- **Enforcement:** size is a *soft target* — it bounds the distiller and shows a counter, but never truncates manually typed memory. `collectionctx.Resolve` reads `memory.md` verbatim (no size awareness).
- **Auto-update:** default **off**; enabling it in the UI first shows a `uiConfirm` warning. Auto-commit keeps a `memory.prev.md` snapshot; the snapshot is consumed on **revert** *or* on a **manual memory save** (the net covers exactly the last unreviewed auto-commit).
- **No-op contract:** size unset ⇒ medium; `auto_update` off ⇒ the worker's per-event check returns immediately; behaviour byte-identical. CLI/TUI untouched (server-only UI + worker).
- **i18n:** English base + fr/es/de at full key parity; do **not** translate product nouns (Omnis, Squad, …). After editing catalogues run `make i18n` and bump `?v=` on `app.js` + `locales.js` in `web/index.html`.
- **Env:** `OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL` (Go duration, default `30m`).
- **Commit convention:** end commit messages with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. Work on branch `feat/collection-memory-controls` (already created off `develop`).

## File Structure

- **Modify** `internal/sessions/collections.go` — extend `collectionProfile`; add `CollectionProfileData`, `CollectionProfileFull`, `SetCollectionProfileData`, `SetCollectionMemoryUpdate`, `ValidMemorySize`; make `CollectionProfile`/`SetCollectionProfile` field-preserving.
- **Create** `internal/sessions/collections_profile_test.go` — profile field round-trip.
- **Modify** `internal/collectionctx/collectionctx.go` — prev-memory helpers.
- **Create** `internal/collectionctx/prevmemory_test.go` — prev round-trip.
- **Modify** `agent/collection_memory.go` — `SizeWordLimit`; thread a word limit through `buildDistillRequest` + `DistillCollectionMemory`.
- **Create** `agent/collection_memory_size_test.go` — limit mapping + request shape.
- **Modify** `server/collection_memory.go` — distill route passes the collection's size; add `materialHash`.
- **Modify** `server/collections.go` — PATCH fields; GET fields; PUT consumes snapshot; `handleRevertCollectionMemory`.
- **Modify** `server/server.go` — register the revert route.
- **Create** `server/collection_autoupdate.go` — the auto-update worker + `shouldAutoUpdate` + `autoUpdateMinInterval`.
- **Create** `server/collection_autoupdate_test.go` — predicate + commit/skip.
- **Create** `server/collections_ctx_test.go` — route round-trip (PATCH/GET/revert/PUT-consume).
- **Modify** `server/main.go` — start the worker.
- **Modify** `web/app.js`, `web/css/features/dialogs.css`, `web/i18n/{en,fr,es,de}.json`, `web/index.html` — the editor UI.
- **Modify** `CLAUDE.md`, `internal/features/FEATURES.md` — docs.

---

### Task 1: Collection profile — size + auto-update + last-update scalars

**Files:**
- Modify: `internal/sessions/collections.go`
- Test: `internal/sessions/collections_profile_test.go`

**Interfaces:**
- Produces: `CollectionProfileData{Squad, Cwd, MemorySize string; AutoUpdate bool; LastMemoryUpdate int64}`; `CollectionProfileFull(name string) CollectionProfileData`; `SetCollectionProfileData(name string, d CollectionProfileData) error`; `SetCollectionMemoryUpdate(name string, ts int64) error`; `ValidMemorySize(s string) bool`. `CollectionProfile(name)(squad,cwd string)` and `SetCollectionProfile(name,squad,cwd string) error` keep their signatures but now preserve the other fields.

- [ ] **Step 1: Write the failing test**

Create `internal/sessions/collections_profile_test.go`:

```go
package sessions

import "testing"

func TestCollectionProfilePreservesFields(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	if err := SetCollectionProfileData("Acme", CollectionProfileData{
		Squad: "Coding", MemorySize: "large", AutoUpdate: true, LastMemoryUpdate: 123,
	}); err != nil {
		t.Fatal(err)
	}
	// A squad/cwd-only edit (the PATCH path) must NOT wipe memory_size/auto_update.
	if err := SetCollectionProfile("Acme", "Kubernetes", "/tmp"); err != nil {
		t.Fatal(err)
	}
	p := CollectionProfileFull("Acme")
	if p.Squad != "Kubernetes" || p.Cwd != "/tmp" {
		t.Fatalf("squad/cwd not updated: %+v", p)
	}
	if p.MemorySize != "large" || !p.AutoUpdate || p.LastMemoryUpdate != 123 {
		t.Fatalf("size/auto_update/last clobbered: %+v", p)
	}
	// The legacy two-value accessor still works.
	if s, c := CollectionProfile("Acme"); s != "Kubernetes" || c != "/tmp" {
		t.Fatalf("CollectionProfile = %q,%q", s, c)
	}
	// SetCollectionMemoryUpdate updates only the timestamp.
	if err := SetCollectionMemoryUpdate("Acme", 0); err != nil {
		t.Fatal(err)
	}
	if CollectionProfileFull("Acme").LastMemoryUpdate != 0 {
		t.Fatal("last_memory_update not cleared")
	}
	if !ValidMemorySize("") || !ValidMemorySize("small") || ValidMemorySize("huge") {
		t.Fatal("ValidMemorySize wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -run TestCollectionProfilePreservesFields -v`
Expected: FAIL — `CollectionProfileData` / `CollectionProfileFull` / `SetCollectionProfileData` / `SetCollectionMemoryUpdate` / `ValidMemorySize` undefined.

- [ ] **Step 3: Extend the `collectionProfile` struct**

In `internal/sessions/collections.go`, replace the struct (lines ~51-54):

```go
type collectionProfile struct {
	Squad string `json:"squad,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	// MemorySize bounds the distilled/injected memory ("" | "small" | "medium" |
	// "large"; "" ⇒ medium). It is a soft target: it caps the distiller and drives
	// the editor word-counter, but never truncates manually typed memory.
	MemorySize string `json:"memory_size,omitempty"`
	// AutoUpdate turns on the server-side idle memory distiller for this
	// collection (opt-in, default off).
	AutoUpdate bool `json:"auto_update,omitempty"`
	// LastMemoryUpdate is the unix-seconds time of the last AUTOMATIC memory
	// commit; 0 ⇒ none. Drives the min-interval gate and the "auto-updated N ago"
	// marker; cleared on revert or a manual memory save.
	LastMemoryUpdate int64 `json:"last_memory_update,omitempty"`
}
```

- [ ] **Step 4: Add the struct-based accessors + validator**

In `internal/sessions/collections.go`, replace `CollectionProfile` and `SetCollectionProfile` (lines ~56-105) with:

```go
// CollectionProfileData is a collection's full per-collection scalar bag.
type CollectionProfileData struct {
	Squad            string
	Cwd              string
	MemorySize       string
	AutoUpdate       bool
	LastMemoryUpdate int64
}

// CollectionProfileFull returns the full stored per-collection scalars. A missing
// collection or no recorded profile yields the zero value.
func CollectionProfileFull(name string) CollectionProfileData {
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return CollectionProfileData{}
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return CollectionProfileData{}
	}
	p := f.Profiles[f.Collections[i]]
	return CollectionProfileData{
		Squad: p.Squad, Cwd: p.Cwd, MemorySize: p.MemorySize,
		AutoUpdate: p.AutoUpdate, LastMemoryUpdate: p.LastMemoryUpdate,
	}
}

// SetCollectionProfileData writes a collection's full scalar bag (all zero ⇒ the
// profile entry is dropped). An unknown collection is an error.
func SetCollectionProfileData(name string, d CollectionProfileData) error {
	name = strings.TrimSpace(name)
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return err
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	canon := f.Collections[i]
	p := collectionProfile{
		Squad:            strings.TrimSpace(d.Squad),
		Cwd:              strings.TrimSpace(d.Cwd),
		MemorySize:       strings.TrimSpace(d.MemorySize),
		AutoUpdate:       d.AutoUpdate,
		LastMemoryUpdate: d.LastMemoryUpdate,
	}
	if p == (collectionProfile{}) {
		if f.Profiles != nil {
			delete(f.Profiles, canon)
		}
	} else {
		if f.Profiles == nil {
			f.Profiles = map[string]collectionProfile{}
		}
		f.Profiles[canon] = p
	}
	return saveFileLocked(f)
}

// CollectionProfile returns the stored (squad, cwd) defaults. Kept for callers
// that only need the seed scalars.
func CollectionProfile(name string) (squad, cwd string) {
	p := CollectionProfileFull(name)
	return p.Squad, p.Cwd
}

// SetCollectionProfile sets squad/cwd while PRESERVING memory_size/auto_update/
// last_memory_update (a squad/cwd-only PATCH must not clobber the memory fields).
func SetCollectionProfile(name, squad, cwd string) error {
	cur := CollectionProfileFull(name)
	cur.Squad = squad
	cur.Cwd = cwd
	return SetCollectionProfileData(name, cur)
}

// SetCollectionMemoryUpdate updates only the last-automatic-memory-commit
// timestamp (0 clears it), preserving the other scalars.
func SetCollectionMemoryUpdate(name string, ts int64) error {
	cur := CollectionProfileFull(name)
	cur.LastMemoryUpdate = ts
	return SetCollectionProfileData(name, cur)
}

// ValidMemorySize reports whether s is an accepted memory-size token.
func ValidMemorySize(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "small", "medium", "large":
		return true
	}
	return false
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sessions/ -run 'TestCollectionProfile|TestCollection' -v`
Expected: PASS. Also run `go build ./...` to confirm no caller broke (`CollectionProfile`/`SetCollectionProfile` signatures unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/sessions/collections.go internal/sessions/collections_profile_test.go
git commit -m "feat(collections): memory_size + auto_update + last_memory_update on the collection profile

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: `collectionctx` previous-memory snapshot helpers

**Files:**
- Modify: `internal/collectionctx/collectionctx.go`
- Test: `internal/collectionctx/prevmemory_test.go`

**Interfaces:**
- Produces: `PrevMemoryPath(name) string`, `ReadPrevMemory(name) string`, `WritePrevMemory(name, text string) error`, `HasPrevMemory(name) bool`, `RemovePrevMemory(name) error`. `memory.prev.md` lives beside `memory.md`, so the existing `RenameDir`/`RemoveDir` (whole-dir) already migrate/drop it.

- [ ] **Step 1: Write the failing test**

Create `internal/collectionctx/prevmemory_test.go`:

```go
package collectionctx

import "testing"

func TestPrevMemoryRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if HasPrevMemory("Acme") {
		t.Fatal("expected no prev initially")
	}
	if err := WritePrevMemory("Acme", "old facts"); err != nil {
		t.Fatal(err)
	}
	if !HasPrevMemory("Acme") {
		t.Fatal("expected prev after write")
	}
	if got := ReadPrevMemory("Acme"); got != "old facts" {
		t.Fatalf("ReadPrevMemory = %q", got)
	}
	if err := RemovePrevMemory("Acme"); err != nil {
		t.Fatal(err)
	}
	if HasPrevMemory("Acme") {
		t.Fatal("expected prev removed")
	}
	// Removing a missing snapshot is a no-op.
	if err := RemovePrevMemory("Acme"); err != nil {
		t.Fatalf("RemovePrevMemory on missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collectionctx/ -run TestPrevMemoryRoundTrip -v`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the prev-memory helpers**

In `internal/collectionctx/collectionctx.go`, add the file constant beside `memoryFile` (line ~33):

```go
const (
	instructionsFile = "instructions.md"
	memoryFile       = "memory.md"
	prevMemoryFile   = "memory.prev.md"
)
```

Then add, after `WriteMemory` / `writeFile` (around line 105):

```go
// PrevMemoryPath returns the on-disk path of a collection's previous-memory
// snapshot (used by auto-update's revert net), or "" for an unusable name.
func PrevMemoryPath(name string) string { return filePath(name, prevMemoryFile) }

// ReadPrevMemory returns the previous-memory snapshot text, or "" when absent.
func ReadPrevMemory(name string) string { return readFile(PrevMemoryPath(name)) }

// WritePrevMemory replaces the snapshot (an empty body removes it).
func WritePrevMemory(name, text string) error { return writeFile(PrevMemoryPath(name), text) }

// HasPrevMemory reports whether a non-empty snapshot exists (so the UI only
// offers Revert when there is prior memory to restore).
func HasPrevMemory(name string) bool { return strings.TrimSpace(ReadPrevMemory(name)) != "" }

// RemovePrevMemory drops the snapshot; a missing file is a no-op.
func RemovePrevMemory(name string) error {
	p := PrevMemoryPath(name)
	if p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/collectionctx/ -run TestPrevMemoryRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collectionctx/collectionctx.go internal/collectionctx/prevmemory_test.go
git commit -m "feat(collectionctx): previous-memory snapshot helpers for auto-update revert

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Distiller — size-driven word limit

**Files:**
- Modify: `agent/collection_memory.go`
- Test: `agent/collection_memory_size_test.go`

**Interfaces:**
- Produces: `SizeWordLimit(size string) int` (200/350/700, default 350); `DistillCollectionMemory(ctx, currentMemory, material string, wordLimit int) (string, error)` (new trailing `wordLimit` arg); `buildDistillRequest(currentMemory, material string, wordLimit int) *model.LLMRequest`.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing test**

Create `agent/collection_memory_size_test.go`:

```go
package agent

import (
	"strings"
	"testing"
)

func TestSizeWordLimit(t *testing.T) {
	cases := map[string]int{"small": 200, "medium": 350, "large": 700, "": 350, "bogus": 350, "LARGE": 700}
	for in, want := range cases {
		if got := SizeWordLimit(in); got != want {
			t.Errorf("SizeWordLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBuildDistillRequestInjectsWordTarget(t *testing.T) {
	req := buildDistillRequest("prior facts", "## Session: s\nUser: hi\nAssistant: yo\n", 200)
	if req == nil || len(req.Contents) == 0 || len(req.Contents[0].Parts) == 0 {
		t.Fatal("nil/empty request")
	}
	body := req.Contents[0].Parts[0].Text
	if !strings.Contains(body, "200 words") {
		t.Fatalf("word target missing from body:\n%s", body)
	}
	if !strings.Contains(body, "prior facts") {
		t.Fatal("current memory missing from body")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/ -run 'TestSizeWordLimit|TestBuildDistillRequestInjectsWordTarget' -v`
Expected: FAIL — `SizeWordLimit` undefined and `buildDistillRequest` arity mismatch.

- [ ] **Step 3: Add `SizeWordLimit` + char cap, drop the const**

In `agent/collection_memory.go`, replace the `collectionMemoryOutputCap` const (line ~26) with:

```go
// SizeWordLimit maps a collection memory-size token to its word budget. "" or an
// unknown token ⇒ medium. Exported so the server can size the distiller from the
// collection's stored MemorySize.
func SizeWordLimit(size string) int {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "small":
		return 200
	case "large":
		return 700
	default: // "medium", "", unknown
		return 350
	}
}

// memoryCharCap is the backstop output cap in characters for a given word limit
// (the prompt is the primary control; this trims a model that overshoots badly).
func memoryCharCap(wordLimit int) int { return wordLimit * 8 }
```

- [ ] **Step 4: Thread the word limit through the distiller**

In `agent/collection_memory.go`, change `DistillCollectionMemory` (line ~50) signature and body:

```go
func (m *Manager) DistillCollectionMemory(ctx context.Context, currentMemory, material string, wordLimit int) (string, error) {
```

Inside it, replace the `buildDistillRequest(currentMemory, material)` call with `buildDistillRequest(currentMemory, material, wordLimit)`, and replace the output-cap block at the end:

```go
	res := strings.TrimSpace(out.String())
	cap := memoryCharCap(wordLimit)
	if r := []rune(res); len(r) > cap {
		res = strings.TrimSpace(string(r[:cap]))
	}
	return res, nil
```

Then change `buildDistillRequest` (line ~92) to take the limit and inject it:

```go
func buildDistillRequest(currentMemory, material string, wordLimit int) *model.LLMRequest {
	material = strings.TrimSpace(material)
	if r := []rune(material); len(r) > collectionMaterialCap {
		material = string(r[:collectionMaterialCap]) + "\n…(older sessions omitted)…"
	}
	currentMemory = strings.TrimSpace(currentMemory)

	var user strings.Builder
	user.WriteString("CURRENT MEMORY:\n")
	if currentMemory == "" {
		user.WriteString("(empty — this collection has no memory yet)\n")
	} else {
		user.WriteString(currentMemory)
		user.WriteString("\n")
	}
	user.WriteString("\nRECENT SESSIONS (most recent first):\n")
	user.WriteString(material)
	fmt.Fprintf(&user, "\n\nIMPORTANT: keep the updated memory concise — under about %d words.", wordLimit)

	return &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: collectionMemorySystemPrompt}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: user.String()}}},
		},
	}
}
```

(`collectionMaterialCap` remains as-is; only `collectionMemoryOutputCap` was removed.)

- [ ] **Step 5: Fix the existing caller so the package compiles**

The only current caller is `server/collection_memory.go`. Temporarily keep it compiling by passing medium — Task 4 replaces it with the real size:

Add the agent import to `server/collection_memory.go`'s import block (it currently has none). Use the **`toolkitagent`** alias — the same alias `server/a2a_server.go` uses and the alias Task 5's worker uses, so every server call site to `SizeWordLimit` is identical:

```go
	toolkitagent "github.com/blouargant/omnis/agent"
```

Then in `handleDistillCollectionMemory`, change the distill call to:

```go
		proposed, err := d.Manager.DistillCollectionMemory(c.Request.Context(), current, material, toolkitagent.SizeWordLimit(""))
```

- [ ] **Step 6: Run tests + build**

Run: `go test ./agent/ -run 'TestSizeWordLimit|TestBuildDistillRequestInjectsWordTarget' -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add agent/collection_memory.go agent/collection_memory_size_test.go server/collection_memory.go
git commit -m "feat(agent): size-driven word limit for the collection memory distiller

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Collection context routes — size/auto-update fields, snapshot consume, revert

**Files:**
- Modify: `server/collections.go`, `server/collection_memory.go`, `server/server.go`
- Test: `server/collections_ctx_test.go`

**Interfaces:**
- Consumes: `sessions.CollectionProfileFull`, `SetCollectionProfileData`, `SetCollectionMemoryUpdate`, `ValidMemorySize` (Task 1); `collectionctx.HasPrevMemory`, `ReadPrevMemory`, `WriteMemory`, `RemovePrevMemory` (Task 2); `agent.SizeWordLimit` (Task 3).
- Produces: `POST /api/collections/:name/memory/revert` → `{name, memory}`; PATCH accepts `memory_size`/`auto_update`; GET `/context` returns `memory_size`/`auto_update`/`last_memory_update`/`has_prev_memory`.

- [ ] **Step 1: Write the failing route test**

Create `server/collections_ctx_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestCollectionContextSizeAutoUpdateAndRevert(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}

	// PATCH size + auto-update.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"memory_size": "large", "auto_update": true}); w.Code != http.StatusOK {
		t.Fatalf("PATCH status %d: %s", w.Code, w.Body.String())
	}
	// A squad-only PATCH must not wipe them.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"squad": ""}); w.Code != http.StatusOK {
		t.Fatalf("PATCH squad status %d: %s", w.Code, w.Body.String())
	}
	// GET surfaces the fields.
	var got struct {
		MemorySize     string `json:"memory_size"`
		AutoUpdate     bool   `json:"auto_update"`
		HasPrevMemory  bool   `json:"has_prev_memory"`
	}
	w := do(http.MethodGet, "/api/collections/Acme/context", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.MemorySize != "large" || !got.AutoUpdate {
		t.Fatalf("fields not persisted: %+v", got)
	}
	if got.HasPrevMemory {
		t.Fatal("unexpected prev memory")
	}
	// Invalid size rejected.
	if w := do(http.MethodPatch, "/api/collections/Acme", map[string]any{"memory_size": "huge"}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad size, got %d", w.Code)
	}

	// Simulate an auto-commit: prev snapshot + new memory + timestamp.
	collectionctx.WriteMemory("Acme", "new facts")
	collectionctx.WritePrevMemory("Acme", "old facts")
	sessions.SetCollectionMemoryUpdate("Acme", 111)
	w = do(http.MethodGet, "/api/collections/Acme/context", nil)
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.HasPrevMemory {
		t.Fatal("expected has_prev_memory true")
	}
	// Revert restores prev + consumes the snapshot.
	if w := do(http.MethodPost, "/api/collections/Acme/memory/revert", nil); w.Code != http.StatusOK {
		t.Fatalf("revert status %d: %s", w.Code, w.Body.String())
	}
	if m := collectionctx.ReadMemory("Acme"); m != "old facts" {
		t.Fatalf("revert did not restore memory: %q", m)
	}
	if collectionctx.HasPrevMemory("Acme") {
		t.Fatal("revert did not consume snapshot")
	}
	if sessions.CollectionProfileFull("Acme").LastMemoryUpdate != 0 {
		t.Fatal("revert did not clear last_memory_update")
	}
	// A manual memory PUT clears a fresh snapshot (the net covers only unreviewed auto-commits).
	collectionctx.WritePrevMemory("Acme", "snapshot2")
	sessions.SetCollectionMemoryUpdate("Acme", 222)
	if w := do(http.MethodPut, "/api/collections/Acme/context", map[string]any{"memory": "hand edited"}); w.Code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", w.Code, w.Body.String())
	}
	if collectionctx.HasPrevMemory("Acme") || sessions.CollectionProfileFull("Acme").LastMemoryUpdate != 0 {
		t.Fatal("manual PUT did not consume snapshot")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run TestCollectionContextSizeAutoUpdateAndRevert -v`
Expected: FAIL — PATCH ignores the new fields / no revert route (404).

- [ ] **Step 3: Extend the PATCH handler**

In `server/collections.go` `handleUpdateCollection`, extend the body struct (line ~143):

```go
		var body struct {
			Name       string  `json:"name"`
			Color      *string `json:"color"`
			Squad      *string `json:"squad"`
			Cwd        *string `json:"cwd"`
			MemorySize *string `json:"memory_size"`
			AutoUpdate *bool   `json:"auto_update"`
		}
```

Replace the whole `if body.Squad != nil || body.Cwd != nil { … }` block (lines ~187-209) with a unified profile-merge block:

```go
		// Per-collection scalars (squad / cwd / memory_size / auto_update). Merge
		// with the stored profile so a PATCH touching one field never clears the
		// others (and preserves last_memory_update). Empty string clears a field.
		if body.Squad != nil || body.Cwd != nil || body.MemorySize != nil || body.AutoUpdate != nil {
			prof := sessions.CollectionProfileFull(current)
			if body.Squad != nil {
				prof.Squad = strings.TrimSpace(*body.Squad)
			}
			if body.Cwd != nil {
				prof.Cwd = strings.TrimSpace(*body.Cwd)
			}
			if body.MemorySize != nil {
				ms := strings.TrimSpace(*body.MemorySize)
				if !sessions.ValidMemorySize(ms) {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid memory size"})
					return
				}
				prof.MemorySize = ms
			}
			if body.AutoUpdate != nil {
				prof.AutoUpdate = *body.AutoUpdate
			}
			if prof.Squad != "" && d.Manager != nil && !d.Manager.HasSquad(strings.ToLower(prof.Squad)) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown squad " + prof.Squad})
				return
			}
			if prof.Cwd != "" {
				if info, err := os.Stat(prof.Cwd); err != nil || !info.IsDir() {
					c.JSON(http.StatusBadRequest, gin.H{"error": "default folder is not a directory"})
					return
				}
			}
			if err := sessions.SetCollectionProfileData(current, prof); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
```

- [ ] **Step 4: Extend the GET context handler**

In `server/collections.go` `handleGetCollectionContext`, replace the body (lines ~285-294) with:

```go
		prof := sessions.CollectionProfileFull(name)
		colors, _ := sessions.CollectionColors()
		c.JSON(http.StatusOK, gin.H{
			"name":               name,
			"instructions":       collectionctx.ReadInstructions(name),
			"memory":             collectionctx.ReadMemory(name),
			"squad":              prof.Squad,
			"cwd":                prof.Cwd,
			"color":              colors[name],
			"memory_size":        prof.MemorySize,
			"auto_update":        prof.AutoUpdate,
			"last_memory_update": prof.LastMemoryUpdate,
			"has_prev_memory":    collectionctx.HasPrevMemory(name),
		})
```

- [ ] **Step 5: Make a manual memory PUT consume the snapshot**

In `server/collections.go` `handleSetCollectionContext`, inside the `if body.Memory != nil {` block, after the successful `WriteMemory`, add the consume:

```go
		if body.Memory != nil {
			if err := collectionctx.WriteMemory(name, *body.Memory); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			// A manual memory edit supersedes any unreviewed auto-commit: consume
			// the revert snapshot + clear the auto-update marker.
			_ = collectionctx.RemovePrevMemory(name)
			_ = sessions.SetCollectionMemoryUpdate(name, 0)
		}
```

- [ ] **Step 6: Add the revert handler**

In `server/collections.go`, add after `handleSetCollectionContext`:

```go
// handleRevertCollectionMemory restores a collection's previous-memory snapshot
// (written by an auto-commit) and consumes it — undoing the last automatic
// memory update. 404 when there is no snapshot to restore.
func handleRevertCollectionMemory(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, ok := resolveKnownCollection(c)
		if !ok {
			return
		}
		prev := collectionctx.ReadPrevMemory(name)
		if strings.TrimSpace(prev) == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "no previous memory to revert to"})
			return
		}
		if err := collectionctx.WriteMemory(name, prev); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		_ = collectionctx.RemovePrevMemory(name)
		_ = sessions.SetCollectionMemoryUpdate(name, 0)
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"name": name, "memory": collectionctx.ReadMemory(name)})
	}
}
```

- [ ] **Step 7: Register the revert route**

In `server/server.go`, after line 711 (`…/memory/distill`), add:

```go
	auth.POST("/collections/:name/memory/revert", handleRevertCollectionMemory(d))
```

- [ ] **Step 8: Make the distill route honour the collection's size**

In `server/collection_memory.go` `handleDistillCollectionMemory`, replace the `toolkitagent.SizeWordLimit("")` call from Task 3 Step 5 with the real size (the `toolkitagent` import is already present from Task 3):

```go
		size := sessions.CollectionProfileFull(name).MemorySize
		proposed, err := d.Manager.DistillCollectionMemory(c.Request.Context(), current, material, toolkitagent.SizeWordLimit(size))
```

- [ ] **Step 9: Run tests + build**

Run: `go test ./server/ -run TestCollectionContextSizeAutoUpdateAndRevert -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 10: Commit**

```bash
git add server/collections.go server/collection_memory.go server/server.go server/collections_ctx_test.go
git commit -m "feat(server): collection context size/auto-update fields + memory revert route

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Auto-update worker (idle-triggered distill + commit)

**Files:**
- Create: `server/collection_autoupdate.go`
- Test: `server/collection_autoupdate_test.go`
- Modify: `server/collection_memory.go` (add `materialHash`), `server/main.go` (start the worker)

**Interfaces:**
- Consumes: `events.EventSessionIndexNow`, `d.EventBus`, `d.Registry`, `d.Manager`, `d.PushEvents`; `gatherCollectionMaterial` (existing); `sessions.CollectionProfileFull`/`SetCollectionMemoryUpdate`; `collectionctx.ReadMemory`/`WriteMemory`/`WritePrevMemory`; `agent.SizeWordLimit`.
- Produces: `startCollectionAutoUpdate(ctx, d, minInterval)`; `shouldAutoUpdate(autoUpdate, changed bool, sinceLast, minInterval time.Duration) bool`; `autoUpdateMinInterval() time.Duration`; `materialHash(string) string`; the `autoUpdater` struct with `runCollection(ctx, collection)`.

- [ ] **Step 1: Write the failing test**

Create `server/collection_autoupdate_test.go`:

```go
package main

import (
	"context"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestShouldAutoUpdate(t *testing.T) {
	if !shouldAutoUpdate(true, true, 40*time.Minute, 30*time.Minute) {
		t.Fatal("all conditions met ⇒ should fire")
	}
	if shouldAutoUpdate(false, true, time.Hour, time.Minute) {
		t.Fatal("auto_update off ⇒ no")
	}
	if shouldAutoUpdate(true, false, time.Hour, time.Minute) {
		t.Fatal("content unchanged ⇒ no")
	}
	if shouldAutoUpdate(true, true, time.Minute, 30*time.Minute) {
		t.Fatal("within min-interval ⇒ no")
	}
}

func TestAutoUpdaterCommitsThenSkipsUnchanged(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sessions.AddCollection("Acme")
	sessions.SetCollectionProfileData("Acme", sessions.CollectionProfileData{AutoUpdate: true, MemorySize: "small"})
	collectionctx.WriteMemory("Acme", "old")

	calls := 0
	au := &autoUpdater{
		minInterval: 0, // isolate the content-hash gate for the second run
		gather:      func(string) string { return "## Session: s\nUser: hi\nAssistant: yo\n" },
		distill: func(_ context.Context, cur, mat string, wl int) (string, error) {
			calls++
			if wl != 200 {
				t.Fatalf("expected small=200 word limit, got %d", wl)
			}
			return "new facts", nil
		},
		inflight: map[string]bool{},
		lastHash: map[string]string{},
	}
	au.runCollection(context.Background(), "Acme")
	if got := collectionctx.ReadMemory("Acme"); got != "new facts" {
		t.Fatalf("memory not committed: %q", got)
	}
	if collectionctx.ReadPrevMemory("Acme") != "old" {
		t.Fatal("prev snapshot missing")
	}
	if sessions.CollectionProfileFull("Acme").LastMemoryUpdate == 0 {
		t.Fatal("last_memory_update not set")
	}
	au.runCollection(context.Background(), "Acme") // same material ⇒ hash gate skips
	if calls != 1 {
		t.Fatalf("expected 1 distill call, got %d", calls)
	}
}

func TestAutoUpdaterOffIsNoop(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sessions.AddCollection("Acme") // no profile ⇒ auto_update false
	collectionctx.WriteMemory("Acme", "old")
	au := &autoUpdater{
		minInterval: 0,
		gather:      func(string) string { return "material" },
		distill:     func(_ context.Context, _, _ string, _ int) (string, error) { t.Fatal("distill must not run"); return "", nil },
		inflight:    map[string]bool{},
		lastHash:    map[string]string{},
	}
	au.runCollection(context.Background(), "Acme")
	if collectionctx.ReadMemory("Acme") != "old" {
		t.Fatal("memory changed while auto_update off")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run 'TestShouldAutoUpdate|TestAutoUpdater' -v`
Expected: FAIL — `shouldAutoUpdate`/`autoUpdater` undefined.

- [ ] **Step 3: Add `materialHash` to `server/collection_memory.go`**

Add to `server/collection_memory.go` (and add `"crypto/sha256"` + `"encoding/hex"` to its imports):

```go
// materialHash is a stable content key for gathered material, so the auto-update
// worker can skip re-distilling a collection whose recent chats have not changed.
func materialHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Write the worker**

Create `server/collection_autoupdate.go`:

```go
// collection_autoupdate.go — the per-collection auto-update worker. When a
// collection has auto_update on, it re-distils the collection's recent chats into
// its memory and COMMITS the result automatically, keeping a memory.prev.md
// snapshot so the change can be reverted (see server/collections.go revert route).
//
// It rides the EXISTING idle rail (EventSessionIndexNow — fired for a session
// idle ≥5 min and on archive), so "after some idle time" needs no new timer. Two
// further gates keep it cheap and safe: a content hash (skip when the recent
// chats have not changed since the last distill) and a min-interval (a busy
// collection cannot churn the model). Server-only; a collection with auto_update
// off makes the per-event handler return immediately.
package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
)

// shouldAutoUpdate is the pure gate predicate: fire only when the collection opts
// in, its recent chats changed, and the min-interval since the last auto-commit
// has elapsed.
func shouldAutoUpdate(autoUpdate, changed bool, sinceLast, minInterval time.Duration) bool {
	return autoUpdate && changed && sinceLast >= minInterval
}

// autoUpdateMinInterval reads OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL (default 30m).
func autoUpdateMinInterval() time.Duration {
	const def = 30 * time.Minute
	if v := strings.TrimSpace(os.Getenv("OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// autoUpdater holds the worker's dependencies. gather/distill are injectable so
// the commit logic is testable without a live model.
type autoUpdater struct {
	deps        serverDeps
	minInterval time.Duration
	gather      func(collection string) string
	distill     func(ctx context.Context, current, material string, wordLimit int) (string, error)

	mu       sync.Mutex
	inflight map[string]bool
	lastHash map[string]string
}

// runCollection applies the gates and, if they pass, distils + commits the
// collection's memory. Safe to call concurrently for different collections; a
// per-collection in-flight guard prevents overlapping runs of the same one.
func (au *autoUpdater) runCollection(ctx context.Context, collection string) {
	if collection == "" {
		return
	}
	prof := sessions.CollectionProfileFull(collection)
	if !prof.AutoUpdate {
		return
	}
	material := au.gather(collection)
	if strings.TrimSpace(material) == "" {
		return
	}
	h := materialHash(material)
	sinceLast := time.Since(time.Unix(prof.LastMemoryUpdate, 0))

	au.mu.Lock()
	changed := au.lastHash[collection] != h
	if !shouldAutoUpdate(prof.AutoUpdate, changed, sinceLast, au.minInterval) || au.inflight[collection] {
		au.mu.Unlock()
		return
	}
	au.inflight[collection] = true
	au.mu.Unlock()
	defer func() {
		au.mu.Lock()
		delete(au.inflight, collection)
		au.mu.Unlock()
	}()

	cur := collectionctx.ReadMemory(collection)
	proposed, err := au.distill(ctx, cur, material, toolkitagent.SizeWordLimit(prof.MemorySize))
	if err != nil {
		log.Printf("collection auto-update: distill %q: %v", collection, err)
		return
	}
	proposed = strings.TrimSpace(proposed)
	// Record the hash even on a no-op so we don't re-distill identical material.
	au.mu.Lock()
	au.lastHash[collection] = h
	au.mu.Unlock()
	if proposed == "" || proposed == strings.TrimSpace(cur) {
		return // nothing changed — no write, no snapshot
	}
	if strings.TrimSpace(cur) != "" {
		_ = collectionctx.WritePrevMemory(collection, cur)
	}
	if err := collectionctx.WriteMemory(collection, proposed); err != nil {
		log.Printf("collection auto-update: write %q: %v", collection, err)
		return
	}
	_ = sessions.SetCollectionMemoryUpdate(collection, time.Now().Unix())
	if au.deps.PushEvents != nil {
		au.deps.PushEvents.broadcast("collections_changed", "")
	}
	log.Printf("collection auto-update: committed memory for %q", collection)
}

// startCollectionAutoUpdate subscribes to the idle rail and drives runCollection
// off the main thread (Emit is synchronous; distillation is a slow LLM call).
func startCollectionAutoUpdate(ctx context.Context, d serverDeps, minInterval time.Duration) {
	if d.EventBus == nil || d.Manager == nil || d.Registry == nil {
		return
	}
	au := &autoUpdater{
		deps:        d,
		minInterval: minInterval,
		gather:      func(c string) string { return gatherCollectionMaterial(d, c) },
		distill: func(ctx context.Context, cur, material string, wl int) (string, error) {
			return d.Manager.DistillCollectionMemory(ctx, cur, material, wl)
		},
		inflight: map[string]bool{},
		lastHash: map[string]string{},
	}
	log.Printf("collection auto-update: enabled (min_interval=%s)", minInterval)
	d.EventBus.Subscribe(events.EventSessionIndexNow, func(_ string, payload map[string]any) {
		sid, _ := payload["session_id"].(string)
		if sid == "" {
			return
		}
		meta, ok := d.Registry.Get(sid)
		if !ok {
			return
		}
		collection := sessions.NormalizeCollectionName(meta.Collection)
		if collection == "" {
			return // General has no context
		}
		go au.runCollection(ctx, collection)
	})
}
```

- [ ] **Step 5: Run tests + build**

Run: `go test ./server/ -run 'TestShouldAutoUpdate|TestAutoUpdater' -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Wire the worker into `server/main.go`**

In `server/main.go`, right after the `deps` value is fully populated (the `deps := serverDeps{…}` block around line 409), add:

```go
	startCollectionAutoUpdate(rootCtx, deps, autoUpdateMinInterval())
```

Verify `deps` has `EventBus`, `Manager`, `Registry`, `PushEvents` set before this line (grep the `deps := serverDeps{` block); if `deps` is defined lower than the `startIdleIndexer` call, place this new call directly under the `deps :=` block.

- [ ] **Step 7: Build + full server test pass**

Run: `go build ./... && go test ./server/ -run 'Collection' -v`
Expected: clean build + PASS.

- [ ] **Step 8: Commit**

```bash
git add server/collection_autoupdate.go server/collection_autoupdate_test.go server/collection_memory.go server/main.go
git commit -m "feat(server): idle-triggered collection memory auto-update worker

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Web UI — size radios, word counter, auto-update toggle, revert marker

**Files:**
- Modify: `web/app.js` (`collectionContextDialog`, `editCollectionContext`)
- Modify: `web/css/features/dialogs.css`
- Modify: `web/i18n/en.json`, `web/i18n/fr.json`, `web/i18n/es.json`, `web/i18n/de.json`
- Modify: `web/index.html` (bump `app.js` + `locales.js` `?v=`)

**Interfaces:**
- Consumes: `GET /api/collections/:name/context` now returns `memory_size`/`auto_update`/`last_memory_update`/`has_prev_memory`; `PATCH /api/collections/:name` accepts `memory_size`/`auto_update`; `POST /api/collections/:name/memory/revert`.
- Produces (JS): `collectionContextDialog` resolves `{squad, cwd, instructions, memory, memory_size, auto_update}`; a module-level `_ccModalBusy` flag guarding the dialog's Escape handler.

- [ ] **Step 1: Add the i18n keys (English)**

In `web/i18n/en.json`, after the `collections.memoryHint` line, add:

```json
  "collections.memorySizeLabel": "Size",
  "collections.memorySizeSmall": "Small",
  "collections.memorySizeMedium": "Medium",
  "collections.memorySizeLarge": "Large",
  "collections.memorySizeSmallTip": "Under 200 words — Essentials only, a few key facts",
  "collections.memorySizeMediumTip": "Under 350 words — A solid working brief",
  "collections.memorySizeLargeTip": "Under 700 words — A detailed dossier for complex workstreams",
  "collections.memoryWords": "{n} / {limit} words",
  "collections.autoUpdate": "Auto-update",
  "collections.autoUpdateTip": "Rewrite this memory automatically from recent chats after the collection goes idle",
  "collections.autoUpdateWarnTitle": "Enable automatic memory updates?",
  "collections.autoUpdateWarnMsg": "The memory will be rewritten automatically from recent chats, without asking you to review each change. The previous version is always kept so you can revert. Enable auto-update?",
  "collections.autoUpdateConfirm": "Enable",
  "collections.memoryAutoUpdated": "Auto-updated {ago} ago",
  "collections.memoryRevert": "Revert",
  "collections.memoryReverted": "Memory reverted.",
  "collections.memoryRevertFailed": "Could not revert the memory.",
```

- [ ] **Step 2: Add the same keys to fr / es / de**

`web/i18n/fr.json` (after `collections.memoryHint`):

```json
  "collections.memorySizeLabel": "Taille",
  "collections.memorySizeSmall": "Petite",
  "collections.memorySizeMedium": "Moyenne",
  "collections.memorySizeLarge": "Grande",
  "collections.memorySizeSmallTip": "Moins de 200 mots — L'essentiel seulement, quelques faits clés",
  "collections.memorySizeMediumTip": "Moins de 350 mots — Une synthèse de travail solide",
  "collections.memorySizeLargeTip": "Moins de 700 mots — Un dossier détaillé pour les sujets complexes",
  "collections.memoryWords": "{n} / {limit} mots",
  "collections.autoUpdate": "Mise à jour auto",
  "collections.autoUpdateTip": "Réécrire cette mémoire automatiquement à partir des chats récents après une période d'inactivité",
  "collections.autoUpdateWarnTitle": "Activer les mises à jour automatiques de la mémoire ?",
  "collections.autoUpdateWarnMsg": "La mémoire sera réécrite automatiquement à partir des chats récents, sans vous demander de valider chaque changement. La version précédente est toujours conservée pour permettre un retour en arrière. Activer la mise à jour automatique ?",
  "collections.autoUpdateConfirm": "Activer",
  "collections.memoryAutoUpdated": "Mise à jour auto il y a {ago}",
  "collections.memoryRevert": "Rétablir",
  "collections.memoryReverted": "Mémoire rétablie.",
  "collections.memoryRevertFailed": "Impossible de rétablir la mémoire.",
```

`web/i18n/es.json`:

```json
  "collections.memorySizeLabel": "Tamaño",
  "collections.memorySizeSmall": "Pequeña",
  "collections.memorySizeMedium": "Media",
  "collections.memorySizeLarge": "Grande",
  "collections.memorySizeSmallTip": "Menos de 200 palabras — Solo lo esencial, unos pocos datos clave",
  "collections.memorySizeMediumTip": "Menos de 350 palabras — Un resumen de trabajo sólido",
  "collections.memorySizeLargeTip": "Menos de 700 palabras — Un dosier detallado para temas complejos",
  "collections.memoryWords": "{n} / {limit} palabras",
  "collections.autoUpdate": "Actualización automática",
  "collections.autoUpdateTip": "Reescribir esta memoria automáticamente a partir de los chats recientes tras un periodo de inactividad",
  "collections.autoUpdateWarnTitle": "¿Activar las actualizaciones automáticas de la memoria?",
  "collections.autoUpdateWarnMsg": "La memoria se reescribirá automáticamente a partir de los chats recientes, sin pedirte que revises cada cambio. La versión anterior siempre se conserva para poder revertir. ¿Activar la actualización automática?",
  "collections.autoUpdateConfirm": "Activar",
  "collections.memoryAutoUpdated": "Actualizada automáticamente hace {ago}",
  "collections.memoryRevert": "Revertir",
  "collections.memoryReverted": "Memoria revertida.",
  "collections.memoryRevertFailed": "No se pudo revertir la memoria.",
```

`web/i18n/de.json`:

```json
  "collections.memorySizeLabel": "Größe",
  "collections.memorySizeSmall": "Klein",
  "collections.memorySizeMedium": "Mittel",
  "collections.memorySizeLarge": "Groß",
  "collections.memorySizeSmallTip": "Unter 200 Wörter — Nur das Wesentliche, ein paar Kernfakten",
  "collections.memorySizeMediumTip": "Unter 350 Wörter — Ein solider Arbeitsüberblick",
  "collections.memorySizeLargeTip": "Unter 700 Wörter — Ein detailliertes Dossier für komplexe Themen",
  "collections.memoryWords": "{n} / {limit} Wörter",
  "collections.autoUpdate": "Auto-Aktualisierung",
  "collections.autoUpdateTip": "Dieses Gedächtnis nach einer Leerlaufzeit automatisch aus den letzten Chats neu schreiben",
  "collections.autoUpdateWarnTitle": "Automatische Gedächtnis-Aktualisierung aktivieren?",
  "collections.autoUpdateWarnMsg": "Das Gedächtnis wird automatisch aus den letzten Chats neu geschrieben, ohne dass du jede Änderung prüfst. Die vorherige Version wird immer aufbewahrt, sodass du sie zurücksetzen kannst. Auto-Aktualisierung aktivieren?",
  "collections.autoUpdateConfirm": "Aktivieren",
  "collections.memoryAutoUpdated": "Automatisch aktualisiert vor {ago}",
  "collections.memoryRevert": "Zurücksetzen",
  "collections.memoryReverted": "Gedächtnis zurückgesetzt.",
  "collections.memoryRevertFailed": "Gedächtnis konnte nicht zurückgesetzt werden.",
```

- [ ] **Step 3: Expand the Memory section markup**

In `web/app.js` `collectionContextDialog`, replace the `<div class="user-cmd-field cc-grow-mem">…</div>` block (the memory field) with:

```js
      <div class="user-cmd-field cc-grow-mem">
        <div class="cc-mem-head">
          <span class="user-cmd-field-label cc-label-row cc-mem-label">${escHtml(tr("collections.memory"))}</span>
          <button type="button" class="cc-mem-gen">${escHtml(tr("collections.memoryGenerate"))}</button>
        </div>
        <div class="cc-mem-controls">
          <div class="cc-size-row" role="radiogroup" aria-label="${escHtml(tr("collections.memorySizeLabel"))}">
            <span class="cc-size-caption">${escHtml(tr("collections.memorySizeLabel"))}</span>
            <label class="cc-size-radio" data-tip="${escHtml(tr("collections.memorySizeSmallTip"))}"><input type="radio" name="cc-size" value="small"> ${escHtml(tr("collections.memorySizeSmall"))}</label>
            <label class="cc-size-radio" data-tip="${escHtml(tr("collections.memorySizeMediumTip"))}"><input type="radio" name="cc-size" value="medium"> ${escHtml(tr("collections.memorySizeMedium"))}</label>
            <label class="cc-size-radio" data-tip="${escHtml(tr("collections.memorySizeLargeTip"))}"><input type="radio" name="cc-size" value="large"> ${escHtml(tr("collections.memorySizeLarge"))}</label>
          </div>
          <label class="cc-autoupdate"><input type="checkbox" class="cc-autoupdate-cb"> <span data-tip="${escHtml(tr("collections.autoUpdateTip"))}">${escHtml(tr("collections.autoUpdate"))}</span></label>
        </div>
        <textarea class="cc-mem" spellcheck="false" placeholder="${escHtml(tr("collections.memoryPlaceholder"))}"></textarea>
        <div class="cc-mem-foot">
          <span class="cc-word-count"></span>
          <span class="cc-revert-marker" hidden><span class="cc-revert-text"></span> <button type="button" class="cc-revert-btn">${escHtml(tr("collections.memoryRevert"))}</button></span>
        </div>
        <span class="user-cmd-field-hint">${escHtml(tr("collections.memoryHint"))}</span>
      </div>`;
```

(Keep the leading part of the template string — `<label class="user-cmd-field cc-grow-instr">…</label>` — unchanged; only the memory `<div>` is replaced. The closing backtick + `;` shown above is the end of the existing `body.innerHTML = \`…\`` assignment.)

- [ ] **Step 4: Wire the size radios + word counter + auto-update + revert**

In `web/app.js` `collectionContextDialog`, after the existing help-button prepend lines (`body.querySelector(".cc-mem-label").prepend(...)`), add:

```js
    // ── Memory size (soft target) + live word counter ──
    const CC_SIZE_LIMITS = { small: 200, medium: 350, large: 700 };
    const memEl = body.querySelector(".cc-mem");
    const sizeInputs = Array.from(body.querySelectorAll('input[name="cc-size"]'));
    const initialSize = CC_SIZE_LIMITS[snap.memory_size] ? snap.memory_size : "medium";
    for (const r of sizeInputs) r.checked = r.value === initialSize;
    const wordCountEl = body.querySelector(".cc-word-count");
    const currentSize = () => (sizeInputs.find((r) => r.checked) || {}).value || "medium";
    const updateWordCount = () => {
      const n = (memEl.value.trim().match(/\S+/g) || []).length;
      const limit = CC_SIZE_LIMITS[currentSize()];
      wordCountEl.textContent = tr("collections.memoryWords", { n, limit });
      wordCountEl.classList.toggle("over", n > limit);
    };
    memEl.addEventListener("input", updateWordCount);
    for (const r of sizeInputs) r.addEventListener("change", updateWordCount);
    updateWordCount();

    // Refresh the counter when the "Generate" button fills the textarea: in the
    // EXISTING generate handler (the `genBtn.addEventListener("click", …)` block
    // above, where it does `mem.value = b.proposed || "";`), add one line right
    // after that assignment so the counter recomputes:
    //   mem.dispatchEvent(new Event("input"));
    // (`mem` there and `memEl` here are the same `.cc-mem` element; the input
    // listener is attached at build time, so it exists by the time Generate runs.)

    // ── Auto-update toggle (+ enable warning) ──
    const autoCb = body.querySelector(".cc-autoupdate-cb");
    autoCb.checked = !!snap.auto_update;
    autoCb.addEventListener("change", async () => {
      if (!autoCb.checked) return; // turning OFF never warns
      _ccModalBusy = true;
      const ok = await uiConfirm({
        title: tr("collections.autoUpdateWarnTitle"),
        message: tr("collections.autoUpdateWarnMsg"),
        confirmText: tr("collections.autoUpdateConfirm"),
        cancelText: tr("common.cancel"),
      });
      _ccModalBusy = false;
      if (!ok) autoCb.checked = false; // declined ⇒ leave it off
    });

    // ── Revert marker (only when an unreviewed auto-commit snapshot exists) ──
    const revertMarker = body.querySelector(".cc-revert-marker");
    const revertText = body.querySelector(".cc-revert-text");
    const relAgo = (sec) => {
      const s = Math.max(0, Math.floor(Date.now() / 1000) - (sec || 0));
      if (s < 60) return `${s}s`;
      const m = Math.floor(s / 60);
      if (m < 60) return `${m}m`;
      const h = Math.floor(m / 60);
      if (h < 24) return `${h}h`;
      return `${Math.floor(h / 24)}d`;
    };
    if (snap.has_prev_memory) {
      revertText.textContent = tr("collections.memoryAutoUpdated", { ago: relAgo(snap.last_memory_update) });
      revertMarker.hidden = false;
    }
    body.querySelector(".cc-revert-btn").addEventListener("click", async () => {
      try {
        const res = await apiFetch(`/api/collections/${encodeURIComponent(name)}/memory/revert`, { method: "POST" });
        const b = await res.json().catch(() => ({}));
        if (!res.ok) { showToast(b.error || tr("collections.memoryRevertFailed"), "err"); return; }
        memEl.value = b.memory || "";
        updateWordCount();
        revertMarker.hidden = true;
        showToast(tr("collections.memoryReverted"), "ok");
      } catch (e) {
        console.error(e);
        showToast(tr("collections.memoryRevertFailed"), "err");
      }
    });
```

- [ ] **Step 5: Return the new fields + guard the Escape handler**

In `web/app.js`, add the module-level flag near `_ccHelpPopup` (top of the "Collection field help popup" section):

```js
let _ccModalBusy = false; // true while a child modal (e.g. the auto-update warning) is open
```

In `collectionContextDialog`, update the `onKey` handler to yield to a child modal:

```js
    const onKey = (e) => {
      // Enter inside a textarea inserts a newline; only Escape closes. Yield to a
      // child modal (auto-update warning) and to an open help popup first.
      if (e.key === "Escape") {
        if (_ccModalBusy) return; // the child uiConfirm handles this Escape
        e.preventDefault();
        if (_ccHelpPopup) { closeCcHelpPopup(); return; }
        close(null);
      }
    };
```

Update the `submit` object to include the new scalars:

```js
    const submit = () => close({
      squad: body.querySelector(".cc-squad").value,
      cwd: body.querySelector(".cc-cwd").value.trim(),
      instructions: body.querySelector(".cc-instr").value,
      memory: body.querySelector(".cc-mem").value,
      memory_size: currentSize(),
      auto_update: autoCb.checked,
    });
```

- [ ] **Step 6: Send the scalars on save**

In `web/app.js` `editCollectionContext`, extend the PATCH body:

```js
    const p = await apiFetch(`/api/collections/${encodeURIComponent(c.name)}`, {
      method: "PATCH",
      body: JSON.stringify({ squad: chosen.squad, cwd: chosen.cwd, memory_size: chosen.memory_size, auto_update: chosen.auto_update }),
    });
```

- [ ] **Step 7: Add the CSS**

In `web/css/features/dialogs.css`, after the `.cc-mem-gen:disabled` rule (~line 344), add:

```css
/* Memory-size radios + auto-update toggle row, and the word-count / revert foot. */
.cc-mem-controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 12px;
}
.cc-size-row { display: inline-flex; align-items: center; gap: 10px; }
.cc-size-caption { color: var(--text-muted); }
.cc-size-radio { display: inline-flex; align-items: center; gap: 4px; cursor: pointer; }
.cc-autoupdate { display: inline-flex; align-items: center; gap: 5px; cursor: pointer; }
.cc-autoupdate span { cursor: help; }
.cc-mem-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 11px;
  min-height: 16px;
}
.cc-word-count { color: var(--text-muted); }
.cc-word-count.over { color: var(--warn-fg, #c9821f); font-weight: 600; }
.cc-revert-marker { display: inline-flex; align-items: center; gap: 6px; color: var(--text-muted); }
.cc-revert-btn {
  font-size: 11px;
  padding: 1px 7px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-raised);
  color: var(--text);
  cursor: pointer;
}
.cc-revert-btn:hover { border-color: var(--accent, #0e639c); }
```

- [ ] **Step 8: Regenerate i18n + bump cache-busters**

Run: `make i18n`
Expected: `build_i18n: wrote web/i18n/locales.js (4 locales, …)` with no missing-key warnings.

In `web/index.html`, bump both versions (currently `locales.js?v=58` and `app.js?v=83`):

```html
  <script src="assets/i18n/locales.js?v=59" defer></script>
  ...
  <script src="assets/app.js?v=84" defer></script>
```

- [ ] **Step 9: Static checks**

Run: `node --check web/app.js && for f in en fr es de; do node -e "JSON.parse(require('fs').readFileSync('web/i18n/'+process.argv[1]+'.json','utf8'))" $f && echo "$f ok"; done`
Expected: no output from `node --check` (success) + `en ok / fr ok / es ok / de ok`.

- [ ] **Step 10: Live smoke test (Playwright)**

Build + run the server against the repo web assets (no token), then drive the editor:

```bash
make build-server
OMNIS_WEB_DIR="$(pwd)/web" OMNIS_SERVER_TOKEN="" OMNIS_SERVER_ADDR=":8199" OMNIS_UPDATE_CHECK=false ./bin/omnis-server &
sleep 3; curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8199/
```

In a browser (Playwright MCP): navigate to `http://127.0.0.1:8199/`, then evaluate to open the dialog and assert the controls render:

```js
() => {
  window.collectionContextDialog("Smoke", { instructions: "", memory: "one two three", memory_size: "small", auto_update: false, has_prev_memory: false });
  const radios = document.querySelectorAll('.collection-ctx-modal input[name="cc-size"]');
  const smallChecked = document.querySelector('.collection-ctx-modal input[value="small"]').checked;
  const wc = document.querySelector('.collection-ctx-modal .cc-word-count').textContent;
  const hasToggle = !!document.querySelector('.collection-ctx-modal .cc-autoupdate-cb');
  return { radioCount: radios.length, smallChecked, wc, hasToggle };
}
```

Expected: `radioCount: 3`, `smallChecked: true`, `wc` = "3 / 200 words", `hasToggle: true`. Then click the `large` radio and re-read `.cc-word-count` → "3 / 700 words". Then click the auto-update checkbox and confirm the warning `uiConfirm` (`.ui-modal`) appears. Stop the server (`kill %1`) and remove `.playwright-mcp/` when done.

- [ ] **Step 11: Commit**

```bash
git add web/app.js web/css/features/dialogs.css web/i18n/ web/index.html
git commit -m "feat(web): collection memory size radios, word counter, auto-update toggle + revert

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: Documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `internal/features/FEATURES.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Update the CLAUDE.md "Collection context" section**

In `CLAUDE.md`, in the **Collection context (per-collection instructions + memory)** section, add a bullet documenting:
- the per-collection `memory_size` scalar (small 200 / medium 350 / large 700 words, default medium), a soft target that bounds the distiller (`agent.SizeWordLimit`) + drives the editor word-counter but never truncates typed memory (injection stays size-unaware);
- the **auto-update** worker ([server/collection_autoupdate.go](server/collection_autoupdate.go)): opt-in per collection (`auto_update` on the profile, default off, enable-warning in the UI), rides `EventSessionIndexNow`, gated by content-hash + `OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL` (default 30m), auto-commits with a `memory.prev.md` snapshot, and the `POST /api/collections/:name/memory/revert` net (snapshot consumed on revert or manual memory save). Note this **wires the previously-"unwired by design" Phase 3** with the safety net.

- [ ] **Step 2: Add the env var to the CLAUDE.md environment-variable table**

Add a row:

```markdown
| `OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL` | Minimum time between automatic memory distillations for one collection (Go duration, default `30m`). Gates the per-collection auto-update worker (see "Collection context") so a busy collection can't churn the model |
```

- [ ] **Step 3: Add FEATURES.md bullets**

In `internal/features/FEATURES.md`, under the in-development minor section (the `## A.B (in development)` block whose version is above the latest release tag — create it if absent, per the FEATURES.md rules), add:

```markdown
- **Collection memory size** — choose a Small / Medium / Large memory budget per collection; a live word counter shows how close you are.
- **Automatic memory updates** — opt a collection into keeping its memory current from recent chats, with one-click revert.
```

- [ ] **Step 4: Verify + commit**

Run: `go test ./internal/features/ -v` (guards that FEATURES.md still parses to ≥1 section).
Expected: PASS.

```bash
git add CLAUDE.md internal/features/FEATURES.md
git commit -m "docs: collection memory size + auto-update (CLAUDE.md, FEATURES.md, env var)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Final verification

- [ ] `make test` — full unit suite passes.
- [ ] `make build` — both binaries build.
- [ ] Manual: with a collection set to auto-update and `OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL=1m`, run a chat in it, wait for the 5-min idle event (or archive it to fire immediately), confirm the memory is rewritten and the editor shows the "Auto-updated N ago — Revert" marker, and Revert restores the prior text.
