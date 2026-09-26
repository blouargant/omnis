# Prompt Suggestions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a reply, show a greyed suggested next message in the web UI composer that Tab copies into the composer (never auto-sends).

**Architecture:** Pull model. The client calls `GET /api/sessions/:id/suggestion` when a turn ends in a visible pane; the server generates on demand with the `eval_model_ref` model through a one-off LLM call (`Manager.SuggestNextPrompt`), caches per session keyed on the turn count, single-flights concurrent requests. The client renders the text through the textarea's native `placeholder`, so the browser handles show/hide.

**Tech Stack:** Go (gin, ADK v2 `model.LLM`), vanilla JS web UI, JSON i18n catalogues (`make i18n`).

**Spec:** [docs/superpowers/specs/2026-09-26-prompt-suggestions-design.md](../specs/2026-09-26-prompt-suggestions-design.md)

## Global Constraints

- Model: `Manager.evalModel(ctx, inst)` (eval_model_ref → leader fallback). No new config key.
- Preference `prompt_suggestions` in `preferences.json`: absent ⇒ enabled; explicit `false` ⇒ disabled. Read server-side on every request.
- Last **3** exchanges, transcript cap **6000** runes keeping the **tail**, suggestion cap **160** runes, generation timeout **20 s** from `d.rootCtx`.
- No model call when: pref off, session archived/hidden, 0 turns, turn in flight.
- Failures are not cached; a `NONE` answer is cached as `""`.
- Tab accepts only when: no modifier, slash/`!`/`@` menu hidden, composer empty, not IME-composing, suggestion present, no turn in flight. Never sends.
- Turn-execution path (`handleMessages`, `injectTurnRouted`) is not modified.
- CLI/TUI untouched. The three assistant mini-chats are untouched.
- i18n: en/fr/es/de; do not translate `Tab`. Run `make i18n`; bump `?v=` for `app.js`, `settings.js`, `i18n/locales.js` and `css/styles.css` in `web/index.html`.
- English only in FEATURES.md and `web/docs`.

## Review Focus

1. **A suggestion that starts with `/`, `!` or `#`**: Tab would turn it into a slash command, a host shell escape or an AGENT.md write on send. Expected: such suggestions are discarded server-side (`cleanSuggestion` returns `""`). Test in Task 1.
2. **Fetch races the end of the turn**: the client asks as soon as `done` arrives, possibly before the server releases the run guard. Expected: the server answers `{busy:true}` and the client retries (max 3 attempts, 1 s apart) instead of showing nothing — and never caches a suggestion computed while the turn's persisted state is incomplete. Tests in Tasks 2 and 3.
3. **User switches session / starts a new turn while a suggestion is in flight**: expected: a late answer never shows on a busy session or overwrites a newer one (per-session sequence number; `activeSuggestion` returns `""` while busy). Covered by the Task 3 smoke step.
4. **Model output with quotes, a label, several lines, `NONE.` with punctuation, or an over-long "sentence"**: expected: cleaned to one plain line, or no suggestion. Test in Task 1.
5. **Composer holds a restored draft (not empty) when Tab is pressed**: expected: Tab keeps its default behaviour and the draft is untouched. Covered by the Task 3 smoke step.

---

### Task 1: Agent — `SuggestNextPrompt`, request builder, output cleaner

**Files:**
- Create: `agent/prompt_suggest.go`
- Test: `agent/prompt_suggest_test.go`

**Interfaces:**
- Consumes: `Exchange{User, Assistant string}` ([agent/session_reseed.go:31](../../../agent/session_reseed.go#L31)); `(*Manager).Lookup`, `(*Manager).Current`, `(*Manager).evalModel` ([agent/goal_eval.go:109](../../../agent/goal_eval.go#L109)).
- Produces: `func (m *Manager) SuggestNextPrompt(ctx context.Context, sessionID string, turns []Exchange) (string, bool)` — `ok=false` only on failure; `("", true)` = valid "no suggestion". Also `buildSuggestRequest([]Exchange) *model.LLMRequest` and `cleanSuggestion(string) string` (package-private).

- [ ] **Step 1: Write the failing tests**

```go
package agent

import (
	"context"
	"strings"
	"testing"
)

func TestCleanSuggestion(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "Can you add tests for it?", "Can you add tests for it?"},
		{"trims", "  Show me the diff \n", "Show me the diff"},
		{"double quotes", `"Apply the fix"`, "Apply the fix"},
		{"french quotes", "« Applique la correction »", "Applique la correction"},
		{"backticks", "`Run the tests`", "Run the tests"},
		{"label", "Suggestion: Explain step 2", "Explain step 2"},
		{"label user", "User: What about Windows?", "What about Windows?"},
		{"first line only", "Deploy it\nor maybe not", "Deploy it"},
		{"leading blank lines", "\n\n  Next step?\n", "Next step?"},
		{"none", "NONE", ""},
		{"none lower punct", "none.", ""},
		{"empty", "   ", ""},
		{"slash command", "/compress", ""},
		{"shell escape", "!rm -rf build", ""},
		{"memory write", "# remember this", ""},
		{"too long", strings.Repeat("word ", 40), ""},
	}
	for _, c := range cases {
		if got := cleanSuggestion(c.in); got != c.want {
			t.Errorf("%s: cleanSuggestion(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func requestText(t *testing.T, turns []Exchange) string {
	t.Helper()
	req := buildSuggestRequest(turns)
	if req.Config == nil || req.Config.SystemInstruction == nil {
		t.Fatal("missing system instruction")
	}
	if len(req.Contents) != 1 || req.Contents[0].Role != "user" {
		t.Fatalf("want one user content, got %+v", req.Contents)
	}
	return req.Contents[0].Parts[0].Text
}

func TestBuildSuggestRequestKeepsLastThreeTurns(t *testing.T) {
	turns := []Exchange{
		{User: "q1", Assistant: "a1"},
		{User: "q2", Assistant: "a2"},
		{User: "q3", Assistant: "a3"},
		{User: "q4", Assistant: "a4"},
	}
	txt := requestText(t, turns)
	if strings.Contains(txt, "q1") || strings.Contains(txt, "a1") {
		t.Errorf("oldest turn should be dropped:\n%s", txt)
	}
	for _, s := range []string{"USER: q2", "ASSISTANT: a2", "USER: q4", "ASSISTANT: a4"} {
		if !strings.Contains(txt, s) {
			t.Errorf("missing %q in:\n%s", s, txt)
		}
	}
}

func TestBuildSuggestRequestCapsKeepingTail(t *testing.T) {
	long := strings.Repeat("x", suggestTranscriptCap*2)
	txt := requestText(t, []Exchange{{User: "START", Assistant: long + "THE-END"}})
	if !strings.Contains(txt, "THE-END") {
		t.Error("tail must be kept")
	}
	if strings.Contains(txt, "START") {
		t.Error("head should be cut")
	}
	if n := len([]rune(txt)); n > suggestTranscriptCap+200 {
		t.Errorf("transcript not capped: %d runes", n)
	}
}

func TestSuggestNextPromptNilManager(t *testing.T) {
	var m *Manager
	if s, ok := m.SuggestNextPrompt(context.Background(), "x", []Exchange{{User: "a"}}); s != "" || ok {
		t.Errorf("nil manager: got (%q,%v), want (\"\",false)", s, ok)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./agent -run 'TestCleanSuggestion|TestBuildSuggestRequest|TestSuggestNextPrompt' -count=1`
Expected: FAIL — `undefined: cleanSuggestion`, `undefined: buildSuggestRequest`, `undefined: suggestTranscriptCap`.

- [ ] **Step 3: Write the implementation**

`agent/prompt_suggest.go`:

```go
// prompt_suggest.go — predict the user's next message so the web UI can offer it
// as a Tab-to-accept placeholder in the composer. One non-streamed completion on
// the eval model (eval_model_ref, else the leader) — the same isolated one-off-LLM
// pattern as EvaluateGoal / GenerateTitle: no runner, tools or event bus, so
// nothing reaches any SSE stream. The server caches the result per session.
package agent

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const (
	// suggestMaxTurns is how many trailing exchanges the model sees.
	suggestMaxTurns = 3
	// suggestTranscriptCap bounds the transcript (runes); the TAIL is kept since
	// the latest reply is what the suggestion answers.
	suggestTranscriptCap = 6000
	// suggestMaxLen drops an over-long "sentence" rather than truncating it: a
	// suggestion ending in "…" is not something the user can send.
	suggestMaxLen = 160
)

const suggestSystemPrompt = "You predict the user's next message in a chat with an AI assistant. " +
	"Write the single most likely next message the USER would send: one sentence, first person, " +
	"in the same language the user writes in, at most about 15 words. It must move the conversation " +
	"forward (a follow-up question, a next step, a request to apply or refine). " +
	"Output only the message — no quotes, no preamble. " +
	"If there is no natural follow-up, output exactly NONE."

// suggestLabelRE strips a leading label some models add despite the instruction.
var suggestLabelRE = regexp.MustCompile(`(?i)^(suggestion|suggested (message|reply)|next message|user|utilisateur)\s*[:：\-–]\s*`)

// buildSuggestRequest renders the last suggestMaxTurns exchanges as a plain
// transcript, capped at suggestTranscriptCap runes keeping the tail.
func buildSuggestRequest(turns []Exchange) *model.LLMRequest {
	if len(turns) > suggestMaxTurns {
		turns = turns[len(turns)-suggestMaxTurns:]
	}
	var b strings.Builder
	for _, t := range turns {
		if u := strings.TrimSpace(t.User); u != "" {
			b.WriteString("USER: ")
			b.WriteString(u)
			b.WriteString("\n\n")
		}
		if a := strings.TrimSpace(t.Assistant); a != "" {
			b.WriteString("ASSISTANT: ")
			b.WriteString(a)
			b.WriteString("\n\n")
		}
	}
	transcript := strings.TrimSpace(b.String())
	if r := []rune(transcript); len(r) > suggestTranscriptCap {
		transcript = "…(earlier conversation omitted)…\n" + string(r[len(r)-suggestTranscriptCap:])
	}
	return &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: suggestSystemPrompt}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "CONVERSATION:\n" + transcript}}},
		},
	}
}

// cleanSuggestion reduces the model output to one sendable line, or "" when there
// is no usable suggestion. A result starting with "/", "!" or "#" is rejected: the
// composer would treat it as a slash command, a host shell escape or an AGENT.md
// write when sent.
func cleanSuggestion(raw string) string {
	s := ""
	for _, line := range strings.Split(raw, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			s = l
			break
		}
	}
	s = suggestLabelRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.Trim(s, " \t\"'`“”«»"))
	if s == "" || strings.EqualFold(strings.TrimRight(s, ".!"), "NONE") {
		return ""
	}
	switch s[0] {
	case '/', '!', '#':
		return ""
	}
	if utf8.RuneCountInString(s) > suggestMaxLen {
		return ""
	}
	return s
}

// SuggestNextPrompt predicts the user's next message from the trailing turns.
// Returns ok=false only when the call could not be made or failed; ("", true)
// means "no natural follow-up" and is safe to cache.
func (m *Manager) SuggestNextPrompt(ctx context.Context, sessionID string, turns []Exchange) (string, bool) {
	if m == nil {
		return "", false
	}
	if len(turns) == 0 {
		return "", true
	}
	inst := m.Lookup(sessionID)
	if inst == nil {
		inst = m.Current()
	}
	if inst == nil {
		return "", false
	}
	mdl, err := m.evalModel(ctx, inst)
	if err != nil || mdl == nil {
		return "", false
	}
	var out strings.Builder
	for resp, gerr := range mdl.GenerateContent(ctx, buildSuggestRequest(turns), false) {
		if gerr != nil {
			return "", false
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			out.WriteString(p.Text)
		}
	}
	return cleanSuggestion(out.String()), true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./agent -run 'TestCleanSuggestion|TestBuildSuggestRequest|TestSuggestNextPrompt' -count=1 && go vet ./agent`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add agent/prompt_suggest.go agent/prompt_suggest_test.go
git commit -m "feat(agent): SuggestNextPrompt one-off LLM call for composer suggestions"
```

---

### Task 2: Server — preference, cache, `GET /api/sessions/:id/suggestion`

**Files:**
- Modify: `internal/sessions/sessions.go` (add `Registry.Snapshot` after `Get`, ~line 231)
- Modify: `server/preferences.go` (field on `preferences`)
- Create: `server/prompt_suggest.go`
- Modify: `server/server.go` (`serverDeps` fields; route registration next to `registerPreferencesRoutes(auth, prefStore)` ~line 929)
- Modify: `server/spawn.go` (`forgetSessionState` drops the cache entry)
- Modify: `server/main.go` (`deps := serverDeps{…}` gets `Suggest: newSuggestStore()`)
- Test: `server/prompt_suggest_test.go`, `internal/sessions/snapshot_test.go` (create)

**Interfaces:**
- Consumes: `(*toolkitagent.Manager).SuggestNextPrompt(ctx, sessionID string, turns []toolkitagent.Exchange) (string, bool)` from Task 1.
- Produces: route `GET /api/sessions/:id/suggestion` → `200 {"suggestion": string, "turns": int, "busy": bool}`, `404` unknown session. Preference JSON key `prompt_suggestions` (bool). Task 3 relies on the `busy` field for its retry.

- [ ] **Step 1: Write the failing tests**

Create `internal/sessions/snapshot_test.go`:

```go
package sessions

import "testing"

func TestRegistrySnapshotIsACopy(t *testing.T) {
	r := NewEmptyRegistry()
	r.Add(&SessionMeta{ID: "s1", Turns: 2})
	snap, ok := r.Snapshot("s1")
	if !ok || snap.Turns != 2 {
		t.Fatalf("Snapshot = %+v, %v", snap, ok)
	}
	r.SetArchived("s1", true)
	if snap.Archived {
		t.Error("snapshot must not observe later writes")
	}
	if _, ok := r.Snapshot("nope"); ok {
		t.Error("unknown id must report ok=false")
	}
}
```

Create `server/prompt_suggest_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/configedit"
	"github.com/blouargant/omnis/internal/sessions"
)

type suggestResp struct {
	Suggestion string `json:"suggestion"`
	Turns      int    `json:"turns"`
	Busy       bool   `json:"busy"`
}

// suggestFixture builds a router with one session "s1" holding `turns` persisted
// turns, and a fake generator that counts calls and returns (text, ok).
func suggestFixture(t *testing.T, turns int, text string, ok bool) (http.Handler, *sessions.Registry, *atomic.Int32, *sessionRunGuard) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "s1", Turns: turns})
	for i := 0; i < turns; i++ {
		if err := sessions.AppendConversationTurn("s1", "question", "answer"); err != nil {
			t.Fatal(err)
		}
	}
	calls := &atomic.Int32{}
	guard := newSessionRunGuard()
	d := serverDeps{
		Registry: reg,
		RunGuard: guard,
		rootCtx:  context.Background(),
		Suggest:  newSuggestStore(),
		SuggestFn: func(ctx context.Context, sid string, ex []toolkitagent.Exchange) (string, bool) {
			calls.Add(1)
			if len(ex) == 0 || ex[len(ex)-1].User != "question" {
				t.Errorf("generator got unexpected exchanges: %+v", ex)
			}
			return text, ok
		},
	}
	return newEngine(d), reg, calls, guard
}

func getSuggestion(t *testing.T, h http.Handler, id string) (int, suggestResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id+"/suggestion", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var r suggestResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, r
}

func writePrefs(t *testing.T, body string) {
	t.Helper()
	p := configedit.PreferencesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestionCachedPerTurnCount(t *testing.T) {
	h, reg, calls, _ := suggestFixture(t, 1, "Show me the diff", true)
	for i := 0; i < 2; i++ {
		code, r := getSuggestion(t, h, "s1")
		if code != 200 || r.Suggestion != "Show me the diff" || r.Turns != 1 {
			t.Fatalf("got %d %+v", code, r)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("want 1 generation for an unchanged turn count, got %d", calls.Load())
	}
	// A new turn invalidates the entry.
	_ = sessions.AppendConversationTurn("s1", "question", "answer")
	reg.SetTurns("s1", 2)
	if _, r := getSuggestion(t, h, "s1"); r.Turns != 2 {
		t.Fatalf("turns = %d, want 2", r.Turns)
	}
	if calls.Load() != 2 {
		t.Fatalf("want a new generation after a new turn, got %d calls", calls.Load())
	}
}

func TestSuggestionFailureNotCachedNoneCached(t *testing.T) {
	h, _, calls, _ := suggestFixture(t, 1, "", false)
	getSuggestion(t, h, "s1")
	getSuggestion(t, h, "s1")
	if calls.Load() != 2 {
		t.Fatalf("a failure must be retried, got %d calls", calls.Load())
	}

	h2, _, calls2, _ := suggestFixture(t, 1, "", true) // model said NONE
	getSuggestion(t, h2, "s1")
	getSuggestion(t, h2, "s1")
	if calls2.Load() != 1 {
		t.Fatalf("a NONE answer must be cached, got %d calls", calls2.Load())
	}
}

func TestSuggestionSkippedWithoutModelCall(t *testing.T) {
	t.Run("pref off", func(t *testing.T) {
		h, _, calls, _ := suggestFixture(t, 1, "x", true)
		writePrefs(t, `{"prompt_suggestions": false}`)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("pref off: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("pref explicitly on", func(t *testing.T) {
		h, _, calls, _ := suggestFixture(t, 1, "x", true)
		writePrefs(t, `{"prompt_suggestions": true}`)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "x" || calls.Load() != 1 {
			t.Fatalf("pref on: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("archived", func(t *testing.T) {
		h, reg, calls, _ := suggestFixture(t, 1, "x", true)
		reg.SetArchived("s1", true)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("archived: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("hidden", func(t *testing.T) {
		h, reg, calls, _ := suggestFixture(t, 1, "x", true)
		reg.SetHidden("s1", true)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("hidden: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("no turns", func(t *testing.T) {
		h, _, calls, _ := suggestFixture(t, 0, "x", true)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("no turns: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("turn in flight reports busy", func(t *testing.T) {
		h, _, calls, guard := suggestFixture(t, 1, "x", true)
		release := guard.acquire("s1")
		defer release()
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || !r.Busy || calls.Load() != 0 {
			t.Fatalf("busy: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("unknown session", func(t *testing.T) {
		h, _, _, _ := suggestFixture(t, 1, "x", true)
		if code, _ := getSuggestion(t, h, "nope"); code != http.StatusNotFound {
			t.Fatalf("code %d, want 404", code)
		}
	})
}

func TestSuggestionSingleFlight(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "s1", Turns: 1})
	_ = sessions.AppendConversationTurn("s1", "question", "answer")
	var calls atomic.Int32
	gate := make(chan struct{})
	d := serverDeps{
		Registry: reg, RunGuard: newSessionRunGuard(), rootCtx: context.Background(),
		Suggest: newSuggestStore(),
		SuggestFn: func(context.Context, string, []toolkitagent.Exchange) (string, bool) {
			calls.Add(1)
			<-gate
			return "same", true
		},
	}
	h := newEngine(d)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); getSuggestion(t, h, "s1") }()
	}
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("want 1 generation for concurrent requests, got %d", calls.Load())
	}
}

func TestSuggestStoreForget(t *testing.T) {
	s := newSuggestStore()
	n := 0
	gen := func() (string, bool) { n++; return "x", true }
	s.get(context.Background(), "s1", 1, gen)
	s.forget("s1")
	s.get(context.Background(), "s1", 1, gen)
	if n != 2 {
		t.Fatalf("forget must drop the entry, got %d generations", n)
	}
	var nilStore *suggestStore
	nilStore.forget("s1") // must not panic
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./server -run 'TestSuggestion|TestSuggestStore' -count=1; go test ./internal/sessions -run TestRegistrySnapshot -count=1`
Expected: FAIL — `undefined: newSuggestStore`, unknown fields `Suggest`/`SuggestFn`, `r.Snapshot undefined`.

- [ ] **Step 3: Add `Registry.Snapshot`**

In `internal/sessions/sessions.go`, directly after `Get`:

```go
// Snapshot returns a copy of a session's metadata taken under the registry lock,
// so callers can read several fields (Turns, Archived, Hidden, …) without racing
// the setters that write them through the shared pointer Get returns.
func (r *Registry) Snapshot(id string) (SessionMeta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.items[id]
	if !ok {
		return SessionMeta{}, false
	}
	return *m, true
}
```

- [ ] **Step 4: Add the preference field**

In `server/preferences.go`, inside `type preferences struct`, after `Locale`:

```go
	// PromptSuggestions records whether the composer shows a suggested next
	// message after each reply. A pointer so absent (never chosen) reads as
	// enabled — the feature defaults on — while an explicit false disables it.
	// Read server-side by GET /sessions/:id/suggestion so a stale tab cannot
	// spend model calls while the user has it off.
	PromptSuggestions *bool `json:"prompt_suggestions,omitempty"`
```

- [ ] **Step 5: Create `server/prompt_suggest.go`**

```go
package main

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

// Composer prompt suggestions: GET /sessions/:id/suggestion returns the user's
// predicted next message. The client pulls it when a turn ends in a visible pane;
// generation is on demand (Manager.SuggestNextPrompt, on the eval model) and cached
// per session keyed on the turn count, so reopening or reloading a session is free
// and a session nobody looks at never costs a model call.

const (
	suggestTurns   = 3
	suggestTimeout = 20 * time.Second
)

// suggestFunc generates a suggestion; ok=false means failure (not cached).
type suggestFunc func(ctx context.Context, sessionID string, turns []toolkitagent.Exchange) (string, bool)

type suggestEntry struct {
	turns int
	text  string
}

type suggestCall struct {
	done chan struct{}
	text string
}

// suggestStore caches one suggestion per session and single-flights concurrent
// generations for the same (session, turn count).
type suggestStore struct {
	mu      sync.Mutex
	entries map[string]suggestEntry
	calls   map[string]*suggestCall
}

func newSuggestStore() *suggestStore {
	return &suggestStore{entries: map[string]suggestEntry{}, calls: map[string]*suggestCall{}}
}

// get returns the cached suggestion for (sid, turns), or runs gen once for all
// concurrent callers. A waiter gives up when ctx ends (the generation carries on
// and still warms the cache). Only a successful generation is cached.
func (s *suggestStore) get(ctx context.Context, sid string, turns int, gen func() (string, bool)) string {
	s.mu.Lock()
	if e, ok := s.entries[sid]; ok && e.turns == turns {
		s.mu.Unlock()
		return e.text
	}
	key := sid + "#" + strconv.Itoa(turns)
	if c, ok := s.calls[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.text
		case <-ctx.Done():
			return ""
		}
	}
	c := &suggestCall{done: make(chan struct{})}
	s.calls[key] = c
	s.mu.Unlock()

	text, ok := gen()
	c.text = text
	s.mu.Lock()
	delete(s.calls, key)
	if ok {
		s.entries[sid] = suggestEntry{turns: turns, text: text}
	}
	s.mu.Unlock()
	close(c.done)
	return text
}

// forget drops a session's cached suggestion (session deleted). Nil-safe.
func (s *suggestStore) forget(sid string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.entries, sid)
	s.mu.Unlock()
}

// suggestGenerator resolves the generator: the injected one (tests), else the
// Manager's, else nil (feature inert).
func (d serverDeps) suggestGenerator() suggestFunc {
	if d.SuggestFn != nil {
		return d.SuggestFn
	}
	if d.Manager != nil {
		return d.Manager.SuggestNextPrompt
	}
	return nil
}

// suggestionsEnabled: absent preference ⇒ on; explicit false ⇒ off.
func suggestionsEnabled(store *preferencesStore) bool {
	if store == nil {
		return true
	}
	p := store.load()
	return p.PromptSuggestions == nil || *p.PromptSuggestions
}

// lastExchanges maps the trailing n persisted turns onto agent.Exchange.
func lastExchanges(turns []sessions.ConversationTurn, n int) []toolkitagent.Exchange {
	if len(turns) > n {
		turns = turns[len(turns)-n:]
	}
	out := make([]toolkitagent.Exchange, 0, len(turns))
	for _, t := range turns {
		out = append(out, toolkitagent.Exchange{User: t.UserText, Assistant: t.AssistantText})
	}
	return out
}

func handleSuggestion(d serverDeps, prefs *preferencesStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Snapshot(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		// A turn in flight: its persisted state is incomplete (Turns is bumped at
		// turn start), so generating now would cache a suggestion for the wrong
		// state under the new key. Tell the client to ask again shortly.
		if d.RunGuard != nil && d.RunGuard.busy(id) {
			c.JSON(http.StatusOK, gin.H{"suggestion": "", "turns": meta.Turns, "busy": true})
			return
		}
		empty := gin.H{"suggestion": "", "turns": meta.Turns, "busy": false}
		gen := d.suggestGenerator()
		if d.Suggest == nil || gen == nil || meta.Archived || meta.Hidden || meta.Turns == 0 || !suggestionsEnabled(prefs) {
			c.JSON(http.StatusOK, empty)
			return
		}
		root := d.rootCtx
		if root == nil {
			root = context.Background()
		}
		text := d.Suggest.get(c.Request.Context(), id, meta.Turns, func() (string, bool) {
			cf, err := sessions.LoadConversationFile(id)
			if err != nil {
				return "", false
			}
			if len(cf.Turns) == 0 {
				return "", true
			}
			// Server root context, not the request's: a client navigating away must
			// not waste a half-finished call — its result still warms the cache.
			ctx, cancel := context.WithTimeout(root, suggestTimeout)
			defer cancel()
			return gen(ctx, id, lastExchanges(cf.Turns, suggestTurns))
		})
		c.JSON(http.StatusOK, gin.H{"suggestion": text, "turns": meta.Turns, "busy": false})
	}
}
```

- [ ] **Step 6: Wire it**

`server/server.go`, in `type serverDeps struct`, after `SessionIndex`:

```go
	// Suggest caches composer prompt suggestions per session (turn-count keyed).
	// Nil ⇒ GET /sessions/:id/suggestion always answers "" (feature inert).
	Suggest *suggestStore
	// SuggestFn overrides the suggestion generator (tests). Nil ⇒
	// Manager.SuggestNextPrompt.
	SuggestFn suggestFunc
```

`server/server.go`, right after `registerPreferencesRoutes(auth, prefStore)`:

```go
	auth.GET("/sessions/:id/suggestion", handleSuggestion(d, prefStore))
```

`server/spawn.go`, at the end of `forgetSessionState`:

```go
	d.Suggest.forget(id)
```

`server/main.go`, in the `deps := serverDeps{…}` literal, after `SessionIndex: sessionIndex,`:

```go
		Suggest:             newSuggestStore(),
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./server -run 'TestSuggestion|TestSuggestStore' -count=1 -race && go test ./internal/sessions -run TestRegistrySnapshot -count=1 && go build ./... && go vet ./server ./internal/sessions`
Expected: PASS (no race reports), build and vet clean.

- [ ] **Step 8: Run the full server suite**

Run: `go test ./server ./internal/sessions ./agent -count=1`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/sessions/sessions.go internal/sessions/snapshot_test.go server/preferences.go server/prompt_suggest.go server/prompt_suggest_test.go server/server.go server/spawn.go server/main.go
git commit -m "feat(server): GET /sessions/:id/suggestion with per-session cache"
```

---

### Task 3: Web — placeholder rendering, fetching, Tab to accept

**Files:**
- Modify: `web/app.js` (new "Prompt suggestions" section near "Composer @file reference highlighting" ~line 10780; `setComposerReadOnly` ~1907; `updateEditModeBtn` ~10712; `onPromptKeydown` ~1285; `sendMessage` ~8617; send `finally` ~9132; `endRemoteBusy` ~8422; `activateTab` ~7413; `forgetSession` ~7193; `session_rewound` handler ~7947)
- Modify: `web/index.html` (hint element in `.prompt-stack` ~line 325; `?v=` bumps)
- Modify: `web/css/features/composer.css` (after `#prompt::placeholder` ~line 203)
- Modify: `web/i18n/{en,fr,es,de}.json`, regenerate `web/i18n/locales.js`

**Interfaces:**
- Consumes: `GET /api/sessions/:id/suggestion` → `{suggestion, turns, busy}` (Task 2).
- Produces for Task 4: `window.setPromptSuggestionsEnabled(on: boolean)` and the localStorage key `"agent_toolkit_prompt_suggestions"` (`"0"` = off, anything else/absent = on).

- [ ] **Step 1: Add i18n keys**

`web/i18n/en.json`, next to `"composer.placeholder"`:

```json
  "composer.placeholderCtrl": "Message the agent… (Ctrl+Enter to send)",
  "composer.archivedPlaceholder": "Session archived — unarchive to continue the conversation",
  "composer.suggestHint": "Tab ⇥",
```

`fr.json`:
```json
  "composer.placeholderCtrl": "Écrire à l'agent… (Ctrl+Entrée pour envoyer)",
  "composer.archivedPlaceholder": "Session archivée — désarchivez-la pour reprendre la conversation",
  "composer.suggestHint": "Tab ⇥",
```
`es.json`:
```json
  "composer.placeholderCtrl": "Escribe al agente… (Ctrl+Intro para enviar)",
  "composer.archivedPlaceholder": "Sesión archivada — desarchívala para continuar la conversación",
  "composer.suggestHint": "Tab ⇥",
```
`de.json`:
```json
  "composer.placeholderCtrl": "Nachricht an den Agenten… (Strg+Enter zum Senden)",
  "composer.archivedPlaceholder": "Sitzung archiviert — Archivierung aufheben, um fortzufahren",
  "composer.suggestHint": "Tab ⇥",
```

Before writing fr/es/de, read each file's existing `composer.placeholder` value and align the wording/punctuation style with it.

Run: `make i18n`
Expected: regenerates `web/i18n/locales.js`, no missing-key warnings for these keys.

- [ ] **Step 2: Add the hint element and CSS**

`web/index.html`, inside `.prompt-stack`, directly after the `<textarea id="prompt" …>`:

```html
            <span class="prompt-suggest-hint" aria-hidden="true" data-i18n="composer.suggestHint">Tab ⇥</span>
```

`web/css/features/composer.css`, after `#prompt::placeholder { color: #555558; }`:

```css
/* Prompt suggestion: the suggested next message is the textarea's placeholder
   (so the browser hides it on the first keystroke). Italic to set it apart from
   the default hint, plus a "Tab ⇥" badge shown only while the placeholder is. */
#composer-wrap.has-suggestion #prompt::placeholder { font-style: italic; }
#composer-wrap.has-suggestion #prompt:placeholder-shown { padding-right: 64px; }
.prompt-suggest-hint {
  display: none;
  position: absolute;
  right: 8px;
  top: 8px;
  padding: 1px 6px;
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 11px;
  line-height: 16px;
  color: var(--text-muted);
  pointer-events: none;
  user-select: none;
}
#composer-wrap.has-suggestion #prompt:placeholder-shown ~ .prompt-suggest-hint { display: inline-block; }
```

- [ ] **Step 3: Add the suggestion module to `web/app.js`**

Insert just before the `// ─── Composer "@file" reference highlighting` banner:

```js
// ─── Prompt suggestions ──────────────────────────────────────────────────────
// After a reply the server predicts the user's next message
// (GET /api/sessions/:id/suggestion, cached server-side per turn count). It is
// shown as the composer's placeholder — the browser hides it on the first
// keystroke — and Tab copies it into the composer. Never sent automatically.

const SUGGEST_PREF_KEY = "agent_toolkit_prompt_suggestions";
const SUGGEST_RETRIES = 3;       // attempts while the server still reports busy
const SUGGEST_RETRY_MS = 1000;
const sessionSuggestion = new Map(); // sessionId → suggested text
const suggestSeq = new Map();        // sessionId → latest request sequence

function suggestionsEnabled() {
  try { return localStorage.getItem(SUGGEST_PREF_KEY) !== "0"; } catch (_) { return true; }
}

function isSessionBusy(id) {
  return sessionSending.has(id) || remoteBusy.has(id);
}

// activeSuggestion is the suggestion a pane may show right now, or "".
function activeSuggestion(panel) {
  const id = panel && panel.sessionId;
  if (!id || !suggestionsEnabled() || archivedSessions.has(id) || isSessionBusy(id)) return "";
  return sessionSuggestion.get(id) || "";
}

function defaultComposerPlaceholder() {
  return sendOnEnter ? tr("composer.placeholder") : tr("composer.placeholderCtrl");
}

// composerPlaceholder is the single source of truth for the composer hint.
function composerPlaceholder(panel) {
  const id = panel && panel.sessionId;
  if (id && archivedSessions.has(id)) return tr("composer.archivedPlaceholder");
  return activeSuggestion(panel) || defaultComposerPlaceholder();
}

function applyComposerPlaceholder(panel) {
  if (!panel || !panel.els || !panel.els.prompt) return;
  panel.els.prompt.placeholder = composerPlaceholder(panel);
  if (panel.els.composerWrap) panel.els.composerWrap.classList.toggle("has-suggestion", !!activeSuggestion(panel));
}

function repaintSuggestion(sessionId) {
  for (const p of panelsForSession(sessionId)) applyComposerPlaceholder(p);
}

// clearSuggestion drops a session's suggestion and invalidates any in-flight fetch.
function clearSuggestion(sessionId) {
  if (!sessionId) return;
  suggestSeq.set(sessionId, (suggestSeq.get(sessionId) || 0) + 1);
  if (sessionSuggestion.delete(sessionId)) repaintSuggestion(sessionId);
}

// fetchSuggestion asks the server for the session's suggestion when some pane
// shows it. Retries while the server still holds the turn (busy); a newer request
// or a clear supersedes a late answer.
async function fetchSuggestion(sessionId) {
  if (!sessionId || !suggestionsEnabled() || archivedSessions.has(sessionId)) return;
  if (panelsForSession(sessionId).length === 0) return;
  const seq = (suggestSeq.get(sessionId) || 0) + 1;
  suggestSeq.set(sessionId, seq);
  for (let attempt = 0; attempt < SUGGEST_RETRIES; attempt++) {
    if (attempt > 0) await new Promise(r => setTimeout(r, SUGGEST_RETRY_MS));
    if (suggestSeq.get(sessionId) !== seq) return;
    let data;
    try {
      const res = await apiFetch(`/api/sessions/${sessionId}/suggestion`);
      if (!res.ok) return;
      data = await res.json();
    } catch (_) { return; }
    if (suggestSeq.get(sessionId) !== seq) return;
    if (data && data.busy) continue;
    const text = (data && typeof data.suggestion === "string") ? data.suggestion.trim() : "";
    if (text) sessionSuggestion.set(sessionId, text); else sessionSuggestion.delete(sessionId);
    repaintSuggestion(sessionId);
    return;
  }
}

// acceptSuggestion copies the suggestion into an empty composer (Tab). Returns
// true when it did, so the caller can swallow the key.
function acceptSuggestion(panel) {
  const s = activeSuggestion(panel);
  const el = panel && panel.els && panel.els.prompt;
  if (!s || !el || el.value !== "") return false;
  el.value = s;
  el.setSelectionRange(s.length, s.length);
  el.dispatchEvent(new Event("input"));
  return true;
}

// setPromptSuggestionsEnabled is called by Settings → Appearance.
window.setPromptSuggestionsEnabled = function (on) {
  if (!on) {
    for (const id of [...sessionSuggestion.keys()]) clearSuggestion(id);
    for (const p of panels) applyComposerPlaceholder(p);
    return;
  }
  for (const p of panels) if (p.sessionId) fetchSuggestion(p.sessionId);
};
```

- [ ] **Step 4: Route both placeholder sites through `applyComposerPlaceholder`**

In `setComposerReadOnly`, replace the `panel.els.prompt.placeholder = readonly ? … : …;` statement with:

```js
    applyComposerPlaceholder(panel);
```

In `updateEditModeBtn`, replace the `p.els.prompt.placeholder = archivedSessions.has(p.sessionId) ? … : …;` statement with:

```js
    applyComposerPlaceholder(p);
```

- [ ] **Step 5: Tab to accept in `onPromptKeydown`**

Right after the closing `}` of the `if (!pe.slashMenu.hasAttribute("hidden")) { … }` block and before `if (sendOnEnter) {`:

```js
  // Tab copies the suggested next message into an empty composer. The menu block
  // above keeps priority (it returns on Tab); any other case keeps Tab's default.
  if (e.key === "Tab" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && !e.isComposing) {
    if (acceptSuggestion(panel)) { e.preventDefault(); return; }
  }
```

- [ ] **Step 6: Lifecycle hooks**

`sendMessage`: right after `if (!panel.sessionId) return;` (the one following `if (!panel.sessionId) await newChat(panel);`):

```js
  clearSuggestion(panel.sessionId);
```

Send `finally`, directly after the `notifyChatReply(...)` line:

```js
    if (outcome === "done" || outcome === "reload") fetchSuggestion(sessionId);
```

`endRemoteBusy`, as its last statement (after `applySessionUI(sid);`):

```js
  fetchSuggestion(sid);
```

`activateTab`, in the session branch, directly after `restoreComposerDraft(panel, id);`:

```js
  applyComposerPlaceholder(panel);
  fetchSuggestion(id);
```

`forgetSession`, next to `composerDrafts.delete(id);`:

```js
  sessionSuggestion.delete(id);
  suggestSeq.delete(id);
```

`session_rewound` handler, as the first statement inside `else if (event === "session_rewound" && sid) {`:

```js
          clearSuggestion(sid);
          fetchSuggestion(sid);
```

- [ ] **Step 7: Bump asset versions**

In `web/index.html`: `css/styles.css?v=20` → `?v=21`, `i18n/locales.js?v=65` → `?v=66`, `app.js?v=93` → `?v=94` (use the current values +1 if they changed).

- [ ] **Step 8: Syntax check**

Run: `node --check web/app.js && node -e "for (const l of ['en','fr','es','de']) JSON.parse(require('fs').readFileSync('web/i18n/'+l+'.json','utf8'))"`
Expected: no output (success).

- [ ] **Step 9: Live smoke test (Playwright)**

Start a branch server (web assets from the repo, no token, repo config via a temp system layer as in the "omnis runs from /etc/omnis" note):

```bash
make build-server
env -u OMNIS_CONFIG_PATH OMNIS_WEB_DIR=$(pwd)/web OMNIS_SERVER_TOKEN= OMNIS_SERVER_ADDR=127.0.0.1:18090 bin/omnis-server --no-browser
```

(run it in the background). With the Playwright MCP tools, on `http://127.0.0.1:18090/`:
1. Start a chat, send `Explain in two sentences what a Go interface is.` and wait for the reply.
2. Assert the composer textarea's `placeholder` is not the default and `#composer-wrap` has class `has-suggestion` (allow up to ~20 s).
3. Focus the empty composer, press Tab → the textarea value equals the former placeholder; nothing was sent (no new user bubble).
4. Clear the composer, type `/`, press Tab → the slash menu handles it (value still starts with `/`, no suggestion inserted).
5. Clear, type `draft`, press Tab → value stays `draft`.
6. Send another message and, while it streams, assert the placeholder is the default one.
7. Take a screenshot of the composer showing the suggestion + `Tab ⇥` badge, in a light and a dark theme.

Expected: every assertion holds. Stop the server afterwards.

- [ ] **Step 10: Commit**

```bash
git add web/app.js web/index.html web/css/features/composer.css web/i18n/
git commit -m "feat(web): show the suggested next message in the composer, Tab to accept"
```

---

### Task 4: Web — Settings → Appearance toggle

**Files:**
- Modify: `web/settings.js` (constant near `NOTIFY_STORAGE_KEY` ~line 113; `savePromptSuggestions` next to `saveNotifications` ~175; `syncThemeFromServer` ~210; `renderAppearance` ~1192)
- Modify: `web/i18n/{en,fr,es,de}.json`, regenerate `locales.js`
- Modify: `web/index.html` (`settings.js` + `locales.js` `?v=` bumps)

**Interfaces:**
- Consumes: `window.setPromptSuggestionsEnabled(on)` and localStorage key `"agent_toolkit_prompt_suggestions"` (Task 3); preference key `prompt_suggestions` (Task 2).

- [ ] **Step 1: i18n keys**

`en.json`, next to the other `appearance.*` keys:
```json
  "appearance.suggestions": "Reply suggestions",
  "appearance.suggestionsLabel": "After each reply, suggest a next message in the composer (press Tab to use it).",
  "appearance.suggestionsHint": "Generated by the small evaluator model after each reply. Your choice is saved on the server.",
```
`fr.json`:
```json
  "appearance.suggestions": "Suggestions de réponse",
  "appearance.suggestionsLabel": "Après chaque réponse, suggérer un prochain message dans la zone de saisie (appuyez sur Tab pour l'utiliser).",
  "appearance.suggestionsHint": "Générées par le petit modèle d'évaluation après chaque réponse. Votre choix est enregistré sur le serveur.",
```
`es.json`:
```json
  "appearance.suggestions": "Sugerencias de respuesta",
  "appearance.suggestionsLabel": "Tras cada respuesta, sugerir un próximo mensaje en el cuadro de texto (pulsa Tab para usarlo).",
  "appearance.suggestionsHint": "Las genera el modelo evaluador pequeño tras cada respuesta. Tu elección se guarda en el servidor.",
```
`de.json`:
```json
  "appearance.suggestions": "Antwortvorschläge",
  "appearance.suggestionsLabel": "Nach jeder Antwort eine nächste Nachricht im Eingabefeld vorschlagen (mit Tab übernehmen).",
  "appearance.suggestionsHint": "Vom kleinen Evaluator-Modell nach jeder Antwort erzeugt. Deine Wahl wird auf dem Server gespeichert.",
```

Run: `make i18n`

- [ ] **Step 2: Persistence helper + server sync**

After `const NOTIFY_STORAGE_KEY = "agent_toolkit_os_notify";`:

```js
  const SUGGEST_STORAGE_KEY = "agent_toolkit_prompt_suggestions"; // shared with app.js
```

After `saveNotifications`:

```js
  // savePromptSuggestions records the composer-suggestion choice: the localStorage
  // cache app.js reads, plus the server preference (which also stops the server
  // from generating). Applies to open panes immediately.
  function savePromptSuggestions(enabled) {
    localStorage.setItem(SUGGEST_STORAGE_KEY, enabled ? "1" : "0");
    if (typeof window.setPromptSuggestionsEnabled === "function") window.setPromptSuggestionsEnabled(!!enabled);
    return fetch(BASE_PATH + "/api/preferences", {
      method: "PUT",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ prompt_suggestions: !!enabled }),
    }).catch(() => { /* offline — local cache wins */ });
  }
```

In `syncThemeFromServer`, right after the notifications seeding block:

```js
        if (prefs && typeof prefs.prompt_suggestions === "boolean") {
          localStorage.setItem(SUGGEST_STORAGE_KEY, prefs.prompt_suggestions ? "1" : "0");
        }
```

- [ ] **Step 3: The toggle in `renderAppearance`**

Next to `const osNotify = …`:

```js
    const suggestOn = localStorage.getItem(SUGGEST_STORAGE_KEY) !== "0";
```

In the `bodyEl.innerHTML` template, right after the notifications `</section>`:

```js
        <section class="form-section">
          <h3>${escHtml(tr("appearance.suggestions"))}</h3>
          <label class="settings-checkrow">
            <input type="checkbox" id="prompt-suggest-toggle" ${suggestOn ? "checked" : ""} />
            <span>${escHtml(tr("appearance.suggestionsLabel"))}</span>
          </label>
          <p class="settings-hint" style="margin:0;">
            ${escHtml(tr("appearance.suggestionsHint"))}
          </p>
        </section>
```

After the `osToggle` wiring block:

```js
    const suggestToggle = bodyEl.querySelector("#prompt-suggest-toggle");
    if (suggestToggle) {
      suggestToggle.addEventListener("change", () => savePromptSuggestions(suggestToggle.checked));
    }
```

- [ ] **Step 4: Bump versions + syntax check**

`web/index.html`: `settings.js?v=62` → `?v=63`, `locales.js` +1 again.

Run: `node --check web/settings.js`
Expected: no output.

- [ ] **Step 5: Live check**

With the branch server from Task 3 Step 9: open Settings → Appearance, untick "Reply suggestions" → the composer shows the default placeholder at once; `cat $OMNIS_HOME/preferences.json` (default `~/.omnis`) contains `"prompt_suggestions": false`; after a new reply no suggestion appears and the server log shows no suggestion model call. Tick it again → a suggestion appears for the open session.

- [ ] **Step 6: Commit**

```bash
git add web/settings.js web/index.html web/i18n/
git commit -m "feat(web): Settings toggle for composer reply suggestions"
```

---

### Task 5: Documentation

**Files:**
- Modify: `internal/features/FEATURES.md` (under `## 1.10 (in development)`)
- Modify: `web/docs/02-composer.md` (new section after "Sending a message")
- Modify: `CLAUDE.md` (new section after "Automatic session titling (Web UI)")

- [ ] **Step 1: FEATURES.md bullet**

Under `## 1.10 (in development) — …`, add:

```markdown
- **Reply suggestions** — after each answer the composer shows a suggested next message; press Tab to use it (never sent automatically). Toggle in Settings → Appearance.
```

- [ ] **Step 2: User doc**

In `web/docs/02-composer.md`, after the "Sending a message" section:

```markdown
## Reply suggestions

After the agent answers, the empty composer shows a suggested next message in
grey italics with a **Tab ⇥** badge. Press **Tab** to copy it into the composer,
edit it if you like, then send it as usual — a suggestion is never sent on its
own. Start typing and it disappears; clear the field and it comes back.

Suggestions are written by the small evaluator model (the `/goal` evaluator,
`eval_model_ref` in Settings → Models), so each one costs one cheap call, made
only for a chat that is on screen. Turn them off in **Settings → Appearance →
Reply suggestions**.
```

- [ ] **Step 3: CLAUDE.md section**

After the "Automatic session titling (Web UI)" section, add:

```markdown
### Prompt suggestions (Web UI)

After a reply, the empty composer shows a predicted next user message; **Tab**
copies it in (never sends). **Pull, not push**: the client calls
`GET /api/sessions/:id/suggestion` → `{suggestion, turns, busy}`
([server/prompt_suggest.go](server/prompt_suggest.go)) when a turn ends in a
visible pane (send `finally`, `endRemoteBusy`), on `activateTab`, and after a
`session_rewound`. The server generates on demand via
`Manager.SuggestNextPrompt` ([agent/prompt_suggest.go](agent/prompt_suggest.go)) —
the same isolated one-off-LLM pattern as `EvaluateGoal`, on `evalModel`
(`eval_model_ref` → leader), last 3 exchanges, 6000-rune tail cap — and caches it
in `suggestStore` keyed **per session on the turn count**, single-flighted per
(session, turns). Failures are not cached; `NONE` is.

- **No call** for: pref `prompt_suggestions: false` (`preferences.json`, absent ⇒
  on, read server-side each request), archived/hidden sessions, 0 turns.
- **GOTCHA — a turn in flight answers `busy:true`, never generates.** `Turns` is
  bumped at turn START but the conversation file only gains the turn at the END,
  so a generation mid-turn would cache a suggestion for the old state under the
  new key. The client's `done` can arrive before the run guard is released, so
  `fetchSuggestion` retries (3 × 1 s) on `busy`.
- **`cleanSuggestion` rejects a leading `/`, `!` or `#`** — the composer would run
  it as a slash command, a host shell escape or an AGENT.md write on send.
- **Display = the textarea's native `placeholder`** (the text itself is transparent
  for the `@file` backdrop, but `::placeholder` has its own colour), so the browser
  does hide-on-type / show-when-empty. `composerPlaceholder(panel)` is the single
  source for the composer hint (archived message > suggestion > default).
  `#composer-wrap.has-suggestion` + `:placeholder-shown` drive the italic style and
  the `Tab ⇥` badge.
- **Tab precedence**: the slash/`!`/`@` menu block in `onPromptKeydown` runs first;
  the suggestion is accepted only with no modifier, empty composer, no IME, no turn
  in flight. Otherwise Tab keeps its default.
- Settings → Appearance toggle (`savePromptSuggestions`, localStorage
  `agent_toolkit_prompt_suggestions` + `PUT /api/preferences`) calls
  `window.setPromptSuggestionsEnabled`.
- **No-op contract:** pref off or route never called ⇒ no model call, default
  placeholder, Tab unchanged. The turn path is not modified. CLI/TUI and the three
  assistant mini-chats are untouched.
```

No change to the Key packages table: it lists packages, and both new files live in existing ones (`agent/`, `server/`).

- [ ] **Step 4: Verify FEATURES.md still parses**

Run: `go test ./internal/features -count=1`
Expected: PASS.

- [ ] **Step 5: Full suite**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/features/FEATURES.md web/docs/02-composer.md CLAUDE.md
git commit -m "docs: prompt suggestions"
```
