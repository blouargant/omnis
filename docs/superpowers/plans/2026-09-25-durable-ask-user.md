# Durable agent questions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A question asked with `AskUserQuestion` survives a server restart;
answering it after the restart starts a turn that continues the task.

**Architecture:** The in-memory `askuser.Registry` gets an optional
`Persister` hook used only for durable questions. The server implements it by
storing pending questions in the session's `conversation_<id>.json`. At boot,
stored questions are re-registered as orphans; their answers are recorded on
disk, and once every question of an interrupted turn is answered, a resume
turn is injected through the existing `injectTurnRouted` rail.

**Tech Stack:** Go 1.26.6, ADK Go v2.4.0, gin, vanilla JS web UI.

**Spec:** [docs/superpowers/specs/2026-09-25-durable-ask-user-design.md](../specs/2026-09-25-durable-ask-user-design.md)

## Global Constraints

- Only `AskUserQuestion` questions are durable. Permission, hook, budget,
  settings, dependency and MCP-input cards keep today's behaviour.
- CLI and TUI install no persister and must behave exactly as today.
- A registry with no persister set behaves exactly as today.
- Removing a pending entry must never recreate a deleted conversation file.
- Model-facing and persisted Go strings are English; UI chrome goes through
  i18n (en/fr/es/de).
- Run `go test ./...` from the worktree root; `make i18n` needs node.

## Review Focus

1. **Graceful shutdown must keep the question.** SIGTERM cancels the run
   context, which resolves the question as cancelled. The persister must not
   delete the entry when the server root context is done. Tested in Task 4.
2. **The first turn of a brand-new session is interrupted.** The conversation
   file has zero turns; without the loader fix the session is not restored and
   the GC deletes the file. Tested in Task 3.
3. **Restart between two answers of the same turn.** The first answer must be
   on disk, not only in memory. Tested in Task 5.
4. **Deleting a session with a pending question.** Cancelling the question
   after the file is gone must not recreate the file, and orphans must not
   trigger a resume. Tested in Tasks 3 and 5.
5. **Stop must release a pending question.** Today it does not
   (`context.Background()`); after the fix the turn ends and the entry is
   removed. Tested in Task 2.

---

### Task 1: `askuser` registry — durable questions, persister, orphans

**Files:**
- Modify: `internal/askuser/askuser.go`
- Test: `internal/askuser/durable_test.go` (create)

**Interfaces:**
- Produces:
  - `Question.Durable bool` (`json:"durable,omitempty"`), `Question.Agent string`
    (`json:"agent,omitempty"`), `Question.Resumed bool` (`json:"resumed,omitempty"`)
  - `type Persister interface { Save(q Question); Remove(q Question, answered bool) }`
  - `func (r *Registry) SetPersister(p Persister)`
  - `func (r *Registry) Restore(q Question, onAnswer func(Question, Answer))`
  - `func (r *Registry) CancelSession(sessionID string)`
  - `QuestionToPayload` emits `"resumed": true` when set.

- [ ] **Step 1: Write the failing tests**

Create `internal/askuser/durable_test.go`:

```go
package askuser_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

type recPersister struct {
	mu      sync.Mutex
	saved   []string
	removed map[string]bool // id -> answered
}

func newRec() *recPersister { return &recPersister{removed: map[string]bool{}} }

func (p *recPersister) Save(q askuser.Question) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saved = append(p.saved, q.ID)
}

func (p *recPersister) Remove(q askuser.Question, answered bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removed[q.ID] = answered
}

func (p *recPersister) snapshot() ([]string, map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]bool{}
	for k, v := range p.removed {
		out[k] = v
	}
	return append([]string(nil), p.saved...), out
}

// waitPending polls until sessionID has n pending questions.
func waitPending(t *testing.T, r *askuser.Registry, sessionID string, n int) []askuser.Question {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if qs := r.Pending(sessionID); len(qs) == n {
			return qs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d pending questions for %s, got %d", n, sessionID, len(r.Pending(sessionID)))
	return nil
}

func TestPersisterSeesOnlyDurableQuestions(t *testing.T) {
	r := askuser.NewRegistry()
	p := newRec()
	r.SetPersister(p)

	done := make(chan struct{})
	go func() {
		_, _ = r.Ask(context.Background(), "s1", askuser.Question{ID: "plain", Kind: askuser.KindText, Prompt: "p"})
		_, _ = r.Ask(context.Background(), "s1", askuser.Question{ID: "dur", Kind: askuser.KindText, Prompt: "p", Durable: true})
		close(done)
	}()
	waitPending(t, r, "s1", 1)
	_ = r.Resolve("s1", "plain", askuser.Answer{Text: "x"})
	waitPending(t, r, "s1", 1)
	_ = r.Resolve("s1", "dur", askuser.Answer{Text: "y"})
	<-done

	saved, removed := p.snapshot()
	if len(saved) != 1 || saved[0] != "dur" {
		t.Fatalf("persister must only see the durable question, saved=%v", saved)
	}
	if answered, ok := removed["dur"]; !ok || !answered {
		t.Fatalf("durable question must be removed as answered, removed=%v", removed)
	}
	if _, ok := removed["plain"]; ok {
		t.Fatal("non-durable question must never reach the persister")
	}
}

func TestPersisterRemoveReportsCancellation(t *testing.T) {
	r := askuser.NewRegistry()
	p := newRec()
	r.SetPersister(p)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan askuser.Answer, 1)
	go func() {
		ans, _ := r.Ask(ctx, "s1", askuser.Question{ID: "q", Kind: askuser.KindText, Prompt: "p", Durable: true})
		done <- ans
	}()
	waitPending(t, r, "s1", 1)
	cancel()
	if ans := <-done; !ans.Cancelled {
		t.Fatal("cancelled context must yield a cancelled answer")
	}
	_, removed := p.snapshot()
	if answered, ok := removed["q"]; !ok || answered {
		t.Fatalf("a cancellation must be removed with answered=false, removed=%v", removed)
	}
}

func TestRestoredOrphanCallsOnAnswerNotRemove(t *testing.T) {
	r := askuser.NewRegistry()
	p := newRec()
	r.SetPersister(p)

	got := make(chan askuser.Answer, 1)
	r.Restore(askuser.Question{ID: "o1", SessionID: "s1", Kind: askuser.KindText, Prompt: "p", Durable: true},
		func(q askuser.Question, a askuser.Answer) { got <- a })

	qs := r.Pending("s1")
	if len(qs) != 1 || !qs[0].Resumed {
		t.Fatalf("restored question must be pending and marked Resumed, got %+v", qs)
	}
	if err := r.Resolve("s1", "o1", askuser.Answer{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-got:
		if a.Text != "hello" {
			t.Fatalf("onAnswer got %+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("onAnswer was not called")
	}
	if _, removed := p.snapshot(); len(removed) != 0 {
		t.Fatalf("an orphan's storage is owned by onAnswer, persister.Remove must not run: %v", removed)
	}
	if len(r.Pending("s1")) != 0 {
		t.Fatal("answered orphan must leave Pending")
	}
}

func TestCancelSessionDropsOrphansAndCancelsLiveQuestions(t *testing.T) {
	r := askuser.NewRegistry()
	var cancelled []string
	var mu sync.Mutex
	r.SetCancel(func(q askuser.Question) { mu.Lock(); cancelled = append(cancelled, q.ID); mu.Unlock() })

	called := false
	r.Restore(askuser.Question{ID: "orphan", SessionID: "s1", Kind: askuser.KindText, Prompt: "p", Durable: true},
		func(askuser.Question, askuser.Answer) { called = true })
	done := make(chan askuser.Answer, 1)
	go func() {
		ans, _ := r.Ask(context.Background(), "s1", askuser.Question{ID: "live", Kind: askuser.KindText, Prompt: "p"})
		done <- ans
	}()
	go func() { _, _ = r.Ask(context.Background(), "s2", askuser.Question{ID: "other", Kind: askuser.KindText, Prompt: "p"}) }()
	waitPending(t, r, "s1", 2)
	waitPending(t, r, "s2", 1)

	r.CancelSession("s1")

	if ans := <-done; !ans.Cancelled {
		t.Fatal("a live question must be released as cancelled")
	}
	if called {
		t.Fatal("CancelSession must drop an orphan without calling onAnswer")
	}
	if len(r.Pending("s1")) != 0 {
		t.Fatal("s1 must have no pending questions left")
	}
	if len(r.Pending("s2")) != 1 {
		t.Fatal("another session's question must be untouched")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cancelled) != 2 {
		t.Fatalf("both s1 questions must be dismissed in the UI, got %v", cancelled)
	}
}

func TestPayloadCarriesResumed(t *testing.T) {
	p := askuser.QuestionToPayload(askuser.Question{ID: "x", Kind: askuser.KindText, Prompt: "p", Resumed: true})
	if p["resumed"] != true {
		t.Fatalf("payload must carry resumed=true, got %v", p)
	}
	if _, ok := askuser.QuestionToPayload(askuser.Question{ID: "y"})["resumed"]; ok {
		t.Fatal("resumed must be omitted when false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/askuser/ -run 'Persister|Orphan|CancelSession|Resumed'`
Expected: build failure (`Durable`, `SetPersister`, `Restore`, `CancelSession`, `Resumed` undefined).

- [ ] **Step 3: Implement**

In `internal/askuser/askuser.go`:

1. Add to `Question`, after `Item`:

```go
	// Durable marks a question that must survive a server restart (see
	// Persister). Only AskUserQuestion sets it; permission and other technical
	// cards gate one tool call that does not exist after a restart.
	Durable bool `json:"durable,omitempty"`
	// Agent is the agent that asked the question (used to resume after a restart).
	Agent string `json:"agent,omitempty"`
	// Resumed is set by Restore: this question was asked before a server
	// restart and its answer resumes the task in a new turn.
	Resumed bool `json:"resumed,omitempty"`
```

2. Add the interface after `ErrAlreadyResolved`:

```go
// Persister stores durable questions so they survive a restart. Only
// questions with Durable=true are passed to it. Implementations must be safe
// for concurrent use and must not block for long.
type Persister interface {
	// Save is called once a durable question is pending.
	Save(q Question)
	// Remove is called when a live durable question is resolved: answered is
	// true for a real answer, false for a cancellation or timeout.
	Remove(q Question, answered bool)
}
```

3. Extend `pending` and `Registry`:

```go
type pending struct {
	q    Question
	ch   chan Answer // buffer 1; closed on resolution
	once sync.Once   // ensures ch is closed exactly once
	// orphan marks a question restored after a restart: no tool call waits on
	// ch, and its storage is owned by the caller of Restore, so the persister
	// is never called for it. onAnswer (read/written under Registry.mu) is its
	// completion callback; CancelSession clears it so an ending session is
	// never resumed.
	orphan   bool
	onAnswer func(Question, Answer)
}
```

Add a field `persister Persister` to `Registry`, and:

```go
// SetPersister installs the durable-question store. Thread-safe.
func (r *Registry) SetPersister(p Persister) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persister = p
}

func (r *Registry) getPersister() Persister {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persister
}

// Restore re-registers a question persisted before a restart. No tool call
// waits for it: when it is answered, onAnswer runs instead, and the persister
// is not called (the caller owns the stored entry). The question is marked
// Resumed and announced through notifyFn like a new question.
func (r *Registry) Restore(q Question, onAnswer func(Question, Answer)) {
	q.Resumed = true
	p := &pending{q: q, ch: make(chan Answer, 1), orphan: true, onAnswer: onAnswer}
	r.mu.Lock()
	if r.sessions[q.SessionID] == nil {
		r.sessions[q.SessionID] = map[string]*pending{}
	}
	r.sessions[q.SessionID][q.ID] = p
	notify := r.notifyFn
	r.mu.Unlock()
	if notify != nil {
		notify(q)
	}
}

// CancelSession ends every pending question of a session (archive, delete).
// A live question is resolved as cancelled; a restored orphan is dropped
// without calling its onAnswer, since the session is going away.
func (r *Registry) CancelSession(sessionID string) {
	r.mu.Lock()
	var ps []*pending
	for _, p := range r.sessions[sessionID] {
		p.onAnswer = nil // never resume a session that is ending
		ps = append(ps, p)
	}
	r.mu.Unlock()
	for _, p := range ps {
		_ = r.resolveInternal(sessionID, p.q.ID, Answer{Cancelled: true}, p)
	}
}
```

`p.onAnswer` is written and read only under `r.mu` (here, in `Restore` before
the pending is published, and in `resolveInternal` below), so a concurrent
`Resolve` cannot race `CancelSession` under `-race`.

4. In `Ask`, right after `r.notifyFn(q)`:

```go
	if q.Durable {
		if pr := r.getPersister(); pr != nil {
			pr.Save(q)
		}
	}
```

5. Replace `resolveInternal` with:

```go
func (r *Registry) resolveInternal(sessionID, questionID string, ans Answer, p *pending) error {
	resolved := false
	p.once.Do(func() {
		resolved = true
		p.ch <- ans
		close(p.ch)
		r.mu.Lock()
		if r.sessions[sessionID] != nil {
			delete(r.sessions[sessionID], questionID)
		}
		cancelFn := r.cancelFn
		persister := r.persister
		onAnswer := p.onAnswer
		r.mu.Unlock()
		if cancelFn != nil {
			cancelFn(p.q)
		}
		switch {
		case p.orphan:
			if onAnswer != nil {
				onAnswer(p.q, ans)
			}
		case p.q.Durable && persister != nil:
			persister.Remove(p.q, !ans.Cancelled)
		}
	})
	if !resolved {
		return ErrAlreadyResolved
	}
	return nil
}
```

The previous version never set its `alreadyDone` flag, so a double resolve
silently returned nil; it now returns `ErrAlreadyResolved`, as its doc comment
always said. `TestDoubleResolveIsIdempotent` ignores that return value, so it
still passes.

6. In `QuestionToPayload`, next to `password`:

```go
	if q.Resumed {
		p["resumed"] = true
	}
```

- [ ] **Step 4: Run the package tests**

Run: `go test -race ./internal/askuser/`
Expected: PASS (new tests and the existing ones).

- [ ] **Step 5: Commit**

```bash
git add internal/askuser/
git commit -m "feat(askuser): durable questions, persister hook, restored orphans"
```

---

### Task 2: `AskUserQuestion` — mark durable, wait on the run context

**Files:**
- Modify: `core/tools/ask_user.go`
- Test: `core/tools/ask_user_test.go` (create)

**Interfaces:**
- Consumes: `askuser.Question.Durable`, `askuser.Question.Agent` (Task 1).
- Produces: `func askUserHandler(reg *askuser.Registry) func(adk.ToolContext, askUserIn) (askUserOut, error)` (unexported; `NewAskUserTool` wraps it).

- [ ] **Step 1: Write the failing test**

Create `core/tools/ask_user_test.go`:

```go
package tools

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"

	"github.com/blouargant/omnis/internal/askuser"
)

// fakeToolCtx is a minimal adk.ToolContext: Done/Err come from Ctx, the
// methods the handler uses are overridden, everything else panics.
type fakeToolCtx struct {
	agent.StrictContextMock
	sid, agentName string
}

func (f *fakeToolCtx) SessionID() string { return f.sid }
func (f *fakeToolCtx) AgentName() string { return f.agentName }

func TestAskUserQuestionIsDurableAndNamesTheAgent(t *testing.T) {
	reg := askuser.NewRegistry()
	h := askUserHandler(reg)
	tc := &fakeToolCtx{StrictContextMock: agent.StrictContextMock{Ctx: context.Background()}, sid: "s1", agentName: "leader"}

	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if qs := reg.Pending("s1"); len(qs) == 1 {
				if !qs[0].Durable || qs[0].Agent != "leader" {
					t.Errorf("question must be durable and name the agent, got %+v", qs[0])
				}
				_ = reg.Resolve("s1", qs[0].ID, askuser.Answer{Text: "ok"})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	out, err := h(tc, askUserIn{Kind: "text", Prompt: "p"})
	if err != nil || out.Text != "ok" {
		t.Fatalf("got %+v, %v", out, err)
	}
}

// Regression: the tool used to wait on context.Background(), so Stop (which
// cancels the run context) left the turn blocked until the user answered.
func TestAskUserQuestionReleasedByRunContextCancel(t *testing.T) {
	reg := askuser.NewRegistry()
	h := askUserHandler(reg)
	ctx, cancel := context.WithCancel(context.Background())
	tc := &fakeToolCtx{StrictContextMock: agent.StrictContextMock{Ctx: ctx}, sid: "s1"}

	done := make(chan askUserOut, 1)
	go func() {
		out, _ := h(tc, askUserIn{Kind: "text", Prompt: "p"})
		done <- out
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case out := <-done:
		if !out.Cancelled {
			t.Fatalf("cancel must return Cancelled, got %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskUserQuestion is still blocked after the run context was cancelled")
	}
}
```

If `agent.StrictContextMock`'s context methods are named differently from the
README (`Ctx` field), open
`$GOMODCACHE/google.golang.org/adk/v2@v2.4.0/agent/context_mock.go` and adapt
the field name; do not change the assertions.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./core/tools/ -run AskUserQuestion`
Expected: build failure (`askUserHandler` undefined).

- [ ] **Step 3: Implement**

In `core/tools/ask_user.go`, move the handler body into `askUserHandler` and
make `NewAskUserTool` call `mustTool("AskUserQuestion", <unchanged description>, askUserHandler(reg))`:

```go
// askUserHandler is AskUserQuestion's body. The question is Durable: it
// survives a server restart and resumes the task in a new turn. It waits on
// the tool context (the run context), so Stop, session end and shutdown
// release it; a client disconnect does not cancel the run context, so the
// question still waits for the user to come back.
func askUserHandler(reg *askuser.Registry) func(adk.ToolContext, askUserIn) (askUserOut, error) {
	return func(tc adk.ToolContext, in askUserIn) (askUserOut, error) {
		if err := validateAskUserIn(in); err != nil {
			return askUserOut{}, err
		}
		q := askuser.Question{
			Kind:        askuser.Kind(in.Kind),
			Prompt:      in.Prompt,
			Choices:     in.Choices,
			AllowText:   in.AllowText,
			Default:     in.Default,
			TimeoutSecs: in.TimeoutSeconds,
			Durable:     true,
			Agent:       tc.AgentName(),
		}
		ans, err := reg.Ask(tc, tc.SessionID(), q)
		if err != nil {
			return askUserOut{}, fmt.Errorf("ask_user: %w", err)
		}
		return askUserOut{Selected: ans.Selected, Text: ans.Text, Cancelled: ans.Cancelled}, nil
	}
}
```

Drop the now-unused `"context"` import.

- [ ] **Step 4: Run the tests**

Run: `go test ./core/tools/ && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/tools/ask_user.go core/tools/ask_user_test.go
git commit -m "fix(ask_user): durable questions; Stop releases a pending question"
```

---

### Task 3: `internal/sessions` — pending questions in the conversation file

**Files:**
- Create: `internal/sessions/pending_questions.go`
- Modify: `internal/sessions/history.go` (`ConversationFile`, `LoadPersistedSessions`)
- Modify: `internal/sessions/sessions.go` (`SessionMeta`)
- Modify: `server/export_import.go` (strip on export)
- Test: `internal/sessions/pending_questions_test.go` (create), `server/export_import_test.go` (extend)

**Interfaces:**
- Consumes: `askuser.Question`, `askuser.Answer` (Task 1).
- Produces:
  - `type PendingQuestion struct { Question askuser.Question; TurnID, Prompt, Squad string; TurnCount int; AskedAt time.Time; Answer *askuser.Answer }` with JSON tags `question`, `turn_id`, `prompt`, `squad`, `turn_count`, `asked_at`, `answer,omitempty`
  - `ConversationFile.PendingQuestions []PendingQuestion` (`json:"pending_questions,omitempty"`)
  - `SessionMeta.PendingQuestions []PendingQuestion` (not serialised in the session list: `json:"-"`)
  - `func AddPendingQuestion(sessionID string, pq PendingQuestion) error`
  - `func RemovePendingQuestion(sessionID, questionID string) error` — no-op when the file does not exist
  - `func SetPendingAnswer(sessionID, questionID string, ans askuser.Answer) error` — no-op when the file does not exist
  - `func RemovePendingTurn(sessionID, turnID string) error` — no-op when the file does not exist
  - `func ClearPendingQuestions(sessionID string) error` — no-op when the file does not exist
  - `func LoadPendingQuestions(sessionID string) ([]PendingQuestion, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/sessions/pending_questions_test.go`:

```go
package sessions

import (
	"os"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

func pq(id, turn string) PendingQuestion {
	return PendingQuestion{
		Question: askuser.Question{ID: id, SessionID: "s1", Kind: askuser.KindText, Prompt: "Which env?", Durable: true},
		TurnID:   turn, Prompt: "deploy it", Squad: "system", AskedAt: time.Now(),
	}
}

func TestPendingQuestionsRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := AddPendingQuestion("s1", pq("q1", "t1")); err != nil {
		t.Fatal(err)
	}
	if err := AddPendingQuestion("s1", pq("q2", "t1")); err != nil {
		t.Fatal(err)
	}
	if err := SetPendingAnswer("s1", "q1", askuser.Answer{Text: "prod"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPendingQuestions("s1")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0].Answer == nil || got[0].Answer.Text != "prod" || got[1].Answer != nil {
		t.Fatalf("answer must be recorded on q1 only: %+v", got)
	}
	if err := RemovePendingQuestion("s1", "q2"); err != nil {
		t.Fatal(err)
	}
	if err := RemovePendingTurn("s1", "t1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadPendingQuestions("s1"); len(got) != 0 {
		t.Fatalf("expected none left, got %+v", got)
	}
}

// A session whose first turn was interrupted has a conversation file with no
// turns. It must still be restored, or the GC deletes it with its question.
func TestLoadPersistedSessionsKeepsTurnlessSessionWithPendingQuestion(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := AddPendingQuestion("fresh", pq("q1", "t1")); err != nil {
		t.Fatal(err)
	}
	var found *SessionMeta
	for _, m := range LoadPersistedSessions() {
		if m.ID == "fresh" {
			found = m
		}
	}
	if found == nil {
		t.Fatal("turnless session with a pending question was not loaded")
	}
	if len(found.PendingQuestions) != 1 || found.CreatedAt.IsZero() {
		t.Fatalf("meta must carry the question and a created time: %+v", found)
	}
}

// Removing an entry after the session was deleted must not recreate the file.
func TestRemoveDoesNotRecreateDeletedConversation(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_ = AddPendingQuestion("gone", pq("q1", "t1"))
	DeleteConversationFile("gone")
	for _, fn := range []func() error{
		func() error { return RemovePendingQuestion("gone", "q1") },
		func() error { return SetPendingAnswer("gone", "q1", askuser.Answer{Text: "x"}) },
		func() error { return RemovePendingTurn("gone", "t1") },
		func() error { return ClearPendingQuestions("gone") },
	} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(ConversationPath("gone")); !os.IsNotExist(err) {
			t.Fatal("a deleted conversation file was recreated")
		}
	}
}
```

Extend `server/export_import_test.go`: in the existing round-trip test (or a
new test next to it), add a pending question with
`sessions.AddPendingQuestion(srcID, …)` before exporting and assert the
exported JSON does not contain `"pending_questions"`, and that the imported
session has no pending questions (`sessions.LoadPendingQuestions(newID)` is
empty).

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/sessions/ -run Pending && go test ./internal/sessions/ -run Turnless && go test ./internal/sessions/ -run Recreate`
Expected: build failure (`PendingQuestion` undefined).

- [ ] **Step 3: Implement**

`internal/sessions/pending_questions.go`:

```go
package sessions

import (
	"os"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

// PendingQuestion is a durable AskUserQuestion waiting for an answer, stored in
// the session's conversation file so it survives a server restart.
type PendingQuestion struct {
	Question askuser.Question `json:"question"`
	// TurnID groups the questions of one interrupted turn; the resume turn
	// starts once every question of that turn has an answer.
	TurnID string `json:"turn_id"`
	// Prompt is the user request the interrupted turn was answering (empty for
	// an injected turn). A turn is persisted only when it ends, so this is the
	// only record of it.
	Prompt string `json:"prompt,omitempty"`
	Squad  string `json:"squad,omitempty"`
	// TurnCount is how many turns were persisted when the question was asked,
	// so the resume can tell whether the interrupted turn was saved since.
	TurnCount int       `json:"turn_count"`
	AskedAt   time.Time `json:"asked_at"`
	// Answer is set once a restored question is answered, so a restart
	// between two answers of the same turn loses nothing.
	Answer *askuser.Answer `json:"answer,omitempty"`
}

// mutateExisting applies fn only when the conversation file exists, so a
// removal racing a session delete never recreates the deleted file.
func mutateExisting(sessionID string, fn func(*ConversationFile)) error {
	if _, err := os.Stat(ConversationPath(sessionID)); os.IsNotExist(err) {
		return nil
	}
	return mutateConversation(sessionID, fn)
}

// AddPendingQuestion stores a durable question, replacing one with the same id.
func AddPendingQuestion(sessionID string, pq PendingQuestion) error {
	return mutateConversation(sessionID, func(f *ConversationFile) {
		f.PendingQuestions = append(dropQuestion(f.PendingQuestions, pq.Question.ID), pq)
	})
}

// RemovePendingQuestion drops one stored question.
func RemovePendingQuestion(sessionID, questionID string) error {
	return mutateExisting(sessionID, func(f *ConversationFile) {
		f.PendingQuestions = dropQuestion(f.PendingQuestions, questionID)
	})
}

// SetPendingAnswer records the answer on a stored question.
func SetPendingAnswer(sessionID, questionID string, ans askuser.Answer) error {
	return mutateExisting(sessionID, func(f *ConversationFile) {
		for i := range f.PendingQuestions {
			if f.PendingQuestions[i].Question.ID == questionID {
				a := ans
				f.PendingQuestions[i].Answer = &a
			}
		}
	})
}

// RemovePendingTurn drops every stored question of one interrupted turn.
func RemovePendingTurn(sessionID, turnID string) error {
	return mutateExisting(sessionID, func(f *ConversationFile) {
		kept := f.PendingQuestions[:0]
		for _, p := range f.PendingQuestions {
			if p.TurnID != turnID {
				kept = append(kept, p)
			}
		}
		f.PendingQuestions = kept
	})
}

// ClearPendingQuestions drops every stored question of a session.
func ClearPendingQuestions(sessionID string) error {
	return mutateExisting(sessionID, func(f *ConversationFile) { f.PendingQuestions = nil })
}

// LoadPendingQuestions returns the stored questions of a session.
func LoadPendingQuestions(sessionID string) ([]PendingQuestion, error) {
	f, err := LoadConversationFile(sessionID)
	if err != nil || f == nil {
		return nil, err
	}
	return f.PendingQuestions, nil
}

func dropQuestion(in []PendingQuestion, id string) []PendingQuestion {
	out := in[:0]
	for _, p := range in {
		if p.Question.ID != id {
			out = append(out, p)
		}
	}
	return out
}
```

In `history.go`, add to `ConversationFile` (before `Turns`):

```go
	// PendingQuestions are durable AskUserQuestion prompts still waiting for an
	// answer (see pending_questions.go). Never exported, forked or imported.
	PendingQuestions []PendingQuestion `json:"pending_questions,omitempty"`
```

In `LoadPersistedSessions`, replace the skip and the time fields:

```go
		if err != nil || f == nil || (len(f.Turns) == 0 && len(f.PendingQuestions) == 0) {
			continue
		}
		created, lastUsed := sessionTimes(f)
```

…use `CreatedAt: created, LastUsedAt: lastUsed, PendingQuestions: f.PendingQuestions,`
in the `SessionMeta` literal, and add:

```go
// sessionTimes derives a session's created/last-used times from its turns, or
// from its oldest pending question when the first turn was interrupted.
func sessionTimes(f *ConversationFile) (created, lastUsed time.Time) {
	if len(f.Turns) > 0 {
		return f.Turns[0].At, f.Turns[len(f.Turns)-1].At
	}
	for _, p := range f.PendingQuestions {
		if created.IsZero() || p.AskedAt.Before(created) {
			created = p.AskedAt
		}
	}
	return created, created
}
```

In `sessions.go`, add to `SessionMeta`:

```go
	// PendingQuestions are durable questions loaded from disk at boot, restored
	// into the ask-user registry by the server. Not part of the session list.
	PendingQuestions []PendingQuestion `json:"-"`
```

In `server/export_import.go`, in the export handler right after the
`if f == nil { … }` block:

```go
		// A pending question belongs to this instance's run; never export it.
		f.PendingQuestions = nil
```

(`ForkConversation` and the import handler already build a fresh
`ConversationFile` field by field, so they drop the field with no change.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/sessions/ ./server/ -run 'Pending|Turnless|Recreate|Export'`
Expected: PASS. Then `go test ./internal/sessions/ ./server/` — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sessions/ server/export_import.go server/export_import_test.go
git commit -m "feat(sessions): store pending durable questions in the conversation file"
```

---

### Task 4: Server persister — save with turn context, keep on shutdown

**Files:**
- Modify: `server/live_turn.go` (turn id)
- Create: `server/durable_ask.go`
- Test: `server/durable_ask_test.go` (create)

**Interfaces:**
- Consumes: `sessions.AddPendingQuestion`, `sessions.RemovePendingQuestion`, `sessions.LoadConversationTurns` (Task 3); `askuser.Persister` (Task 1).
- Produces:
  - `func (lt *liveTurn) turnID() string`
  - `type askPersister struct { root context.Context; live *liveTurnRegistry; reg *sessions.Registry }`
  - `func newAskPersister(root context.Context, live *liveTurnRegistry, reg *sessions.Registry) *askPersister` (implements `askuser.Persister`)

- [ ] **Step 1: Write the failing tests**

Create `server/durable_ask_test.go`:

```go
package main

import (
	"context"
	"testing"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestAskPersisterSavesTurnContext(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewRegistry()
	meta := reg.New("system")
	_ = sessions.AppendConversationTurn(meta.ID, "earlier", "reply")
	live := newLiveTurnRegistry()
	lt := live.start(meta.ID, func() {}, "deploy the app")

	p := newAskPersister(context.Background(), live, reg)
	p.Save(askuser.Question{ID: "q1", SessionID: meta.ID, Prompt: "Which env?", Durable: true})

	got, _ := sessions.LoadPendingQuestions(meta.ID)
	if len(got) != 1 {
		t.Fatalf("expected one stored question, got %+v", got)
	}
	g := got[0]
	if g.Prompt != "deploy the app" || g.TurnID != lt.turnID() || g.Squad != "system" || g.TurnCount != 1 {
		t.Fatalf("turn context not captured: %+v", g)
	}
}

func TestAskPersisterFallsBackWithoutLiveTurn(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewRegistry()
	meta := reg.New("")
	p := newAskPersister(context.Background(), newLiveTurnRegistry(), reg)
	p.Save(askuser.Question{ID: "q1", SessionID: meta.ID, Prompt: "?", Durable: true})
	got, _ := sessions.LoadPendingQuestions(meta.ID)
	if len(got) != 1 || got[0].TurnID != "q1" || got[0].Prompt != "" {
		t.Fatalf("injected-turn question must resume alone, got %+v", got)
	}
}

func TestAskPersisterKeepsEntryOnShutdown(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewRegistry()
	meta := reg.New("")
	root, stop := context.WithCancel(context.Background())
	p := newAskPersister(root, newLiveTurnRegistry(), reg)
	q := askuser.Question{ID: "q1", SessionID: meta.ID, Prompt: "?", Durable: true}
	p.Save(q)

	stop() // server shutting down
	p.Remove(q, false)
	if got, _ := sessions.LoadPendingQuestions(meta.ID); len(got) != 1 {
		t.Fatal("a cancellation during shutdown must keep the stored question")
	}
}

func TestAskPersisterRemovesOnAnswerOrStop(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewRegistry()
	meta := reg.New("")
	p := newAskPersister(context.Background(), newLiveTurnRegistry(), reg)
	for _, answered := range []bool{true, false} {
		q := askuser.Question{ID: "q", SessionID: meta.ID, Prompt: "?", Durable: true}
		p.Save(q)
		p.Remove(q, answered)
		if got, _ := sessions.LoadPendingQuestions(meta.ID); len(got) != 0 {
			t.Fatalf("answered=%v with the server running must remove the entry", answered)
		}
	}
}
```

`sessions.NewRegistry()` scans the (empty, temp) `OMNIS_HOME`, and
`Registry.New(squad string)` creates a session pinned to that squad. Check
the live-turn registry constructor name in `server/live_turn.go`
(`newLiveTurnRegistry` is assumed) and adjust the call, not the assertions.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./server/ -run AskPersister`
Expected: build failure (`newAskPersister`, `turnID` undefined).

- [ ] **Step 3: Implement**

In `server/live_turn.go`, add a field `id string` to `liveTurn` (next to
`prompt`, with the comment "id identifies this turn for durable questions
asked during it; immutable after start."), set `id: uuid.NewString()` in
`newLiveTurn` (import `github.com/google/uuid`), and add:

```go
// turnID identifies the turn; immutable after start, so no lock is needed.
func (lt *liveTurn) turnID() string { return lt.id }
```

Create `server/durable_ask.go`:

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/sessions"
)

// askPersister stores durable AskUserQuestion prompts in the session's
// conversation file so they survive a restart (askuser.Persister).
type askPersister struct {
	root context.Context
	live *liveTurnRegistry
	reg  *sessions.Registry
}

func newAskPersister(root context.Context, live *liveTurnRegistry, reg *sessions.Registry) *askPersister {
	return &askPersister{root: root, live: live, reg: reg}
}

// Save records the question with what a resume needs: the request of the
// running turn (the only record of it until the turn ends), the squad, and how
// many turns were already persisted. A question asked in an injected turn has
// no live turn, so it forms a turn of its own.
func (p *askPersister) Save(q askuser.Question) {
	pq := sessions.PendingQuestion{Question: q, TurnID: q.ID, AskedAt: time.Now()}
	if lt := p.live.get(q.SessionID); lt != nil {
		if running, prompt := lt.active(); running {
			pq.TurnID, pq.Prompt = lt.turnID(), prompt
		}
	}
	if meta, ok := p.reg.Get(q.SessionID); ok {
		pq.Squad = meta.Squad
	}
	if turns, err := sessions.LoadConversationTurns(q.SessionID); err == nil {
		pq.TurnCount = len(turns)
	}
	if err := sessions.AddPendingQuestion(q.SessionID, pq); err != nil {
		log.Printf("durable ask: save %s/%s: %v", q.SessionID, q.ID, err)
	}
}

// Remove drops the stored question, except for a cancellation while the
// server is shutting down: that is exactly the question that must survive.
func (p *askPersister) Remove(q askuser.Question, answered bool) {
	if !answered && p.root.Err() != nil {
		return
	}
	if err := sessions.RemovePendingQuestion(q.SessionID, q.ID); err != nil {
		log.Printf("durable ask: remove %s/%s: %v", q.SessionID, q.ID, err)
	}
}
```

Use `meta.Squad`, not `meta.Turns`: the in-memory turn counter is incremented
at turn start, before the turn is persisted, so it would be off by one.

- [ ] **Step 4: Run the tests**

Run: `go test ./server/ -run 'AskPersister|LiveTurn'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/live_turn.go server/durable_ask.go server/durable_ask_test.go
git commit -m "feat(server): persist durable questions, keep them across shutdown"
```

---

### Task 5: Resume — coordinator, injected-turn reseed, boot restore, archive/delete cleanup

**Files:**
- Create: `server/durable_resume.go`
- Create: `server/reseed.go`
- Modify: `server/mailbox_push.go` (`injectTurnRouted` → options struct + reseed)
- Modify: `server/sse.go` (use the shared reseed helper)
- Modify: `server/main.go` (install persister, restore at boot)
- Modify: `server/spawn.go` (`forgetSessionState` — called by both the delete path and the archive handler)
- Test: `server/durable_resume_test.go` (create), `server/reseed_test.go` (create)

**Interfaces:**
- Consumes: Tasks 1, 3, 4.
- Produces:
  - `type injectOpts struct { AnswerPrompt, RouterPrompt, PersistPrompt, SSEEvent, ReplyTo string }`
  - `func (pm *pushManager) injectTurnOpts(ctx context.Context, d serverDeps, sessionID, userID string, o injectOpts) string` — `injectTurnRouted` becomes a wrapper that sets `PersistPrompt = routerPrompt`.
  - `type contextReseeder interface { HasSessionContext(ctx context.Context, userID, sessionID, squad string) bool; ReseedSessionContext(ctx context.Context, userID, sessionID, squad string, ex []toolkitagent.Exchange) error }`
  - `func reseedIfCold(ctx context.Context, m contextReseeder, userID, sessionID, squad string)`
  - `func buildResumePrompt(entries []sessions.PendingQuestion) string`
  - `func buildResumeDisplay(entries []sessions.PendingQuestion, persistedTurns int) string`
  - `type resumeCoordinator struct { … }`, `func newResumeCoordinator(reg *askuser.Registry, sreg *sessions.Registry, launch func(sessionID, userID, modelPrompt, display string)) *resumeCoordinator`
  - `func (c *resumeCoordinator) restore(metas []*sessions.SessionMeta)`

- [ ] **Step 1: Write the failing tests**

Create `server/durable_resume_test.go`:

```go
package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/sessions"
)

func entry(id, turn, prompt, question string, turnCount int, ans *askuser.Answer) sessions.PendingQuestion {
	return sessions.PendingQuestion{
		Question: askuser.Question{ID: id, Kind: askuser.KindText, Prompt: question, Durable: true},
		TurnID:   turn, Prompt: prompt, TurnCount: turnCount, AskedAt: time.Now(), Answer: ans,
	}
}

func TestBuildResumePrompt(t *testing.T) {
	got := buildResumePrompt([]sessions.PendingQuestion{
		entry("a", "t", "deploy the app", "Which env?", 0, &askuser.Answer{Selected: []string{"prod"}}),
		entry("b", "t", "deploy the app", "Tag?", 0, &askuser.Answer{Cancelled: true}),
	})
	for _, want := range []string{"[Resumed after a server restart]", "Original request:\ndeploy the app",
		"You asked: Which env?", "The user answered: prod", "You asked: Tag?",
		"The user dismissed this question", "re-check"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resume prompt missing %q:\n%s", want, got)
		}
	}
	noPrompt := buildResumePrompt([]sessions.PendingQuestion{entry("a", "a", "", "Q?", 0, &askuser.Answer{Text: "x"})})
	if strings.Contains(noPrompt, "Original request") {
		t.Fatal("an injected-turn question has no original request to show")
	}
}

func TestBuildResumeDisplay(t *testing.T) {
	es := []sessions.PendingQuestion{entry("a", "t", "deploy the app", "Which env?", 2, &askuser.Answer{Text: "prod"})}
	if got := buildResumeDisplay(es, 3); strings.Contains(got, "deploy the app") || !strings.Contains(got, `↪ Answer to "Which env?": prod`) {
		t.Fatalf("interrupted turn already persisted: show only the answer, got %q", got)
	}
	if got := buildResumeDisplay(es, 2); !strings.HasPrefix(got, "deploy the app") {
		t.Fatalf("interrupted turn lost (crash): show the request first, got %q", got)
	}
}

type launched struct {
	mu    sync.Mutex
	calls []string // model prompts
}

func (l *launched) fn(sid, uid, model, display string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, model)
}

func (l *launched) count() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.calls) }

func setupTurn(t *testing.T) (*askuser.Registry, *sessions.Registry, *sessions.SessionMeta) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	sreg := sessions.NewRegistry()
	meta := sreg.New("")
	_ = sessions.AddPendingQuestion(meta.ID, entry("a", "t", "deploy", "Env?", 0, nil))
	_ = sessions.AddPendingQuestion(meta.ID, entry("b", "t", "deploy", "Tag?", 0, nil))
	meta.PendingQuestions, _ = sessions.LoadPendingQuestions(meta.ID)
	return askuser.NewRegistry(), sreg, meta
}

func TestResumeWaitsForEveryQuestionOfTheTurn(t *testing.T) {
	areg, sreg, meta := setupTurn(t)
	l := &launched{}
	c := newResumeCoordinator(areg, sreg, l.fn)
	c.restore([]*sessions.SessionMeta{meta})
	if n := len(areg.Pending(meta.ID)); n != 2 {
		t.Fatalf("both questions must be restored, got %d", n)
	}

	_ = areg.Resolve(meta.ID, "a", askuser.Answer{Text: "prod"})
	if l.count() != 0 {
		t.Fatal("resume must wait for the second question")
	}
	// The first answer is on disk: a restart now would not lose it.
	stored, _ := sessions.LoadPendingQuestions(meta.ID)
	if len(stored) != 2 || stored[0].Answer == nil || stored[0].Answer.Text != "prod" {
		t.Fatalf("first answer must be persisted, got %+v", stored)
	}

	_ = areg.Resolve(meta.ID, "b", askuser.Answer{Text: "v2"})
	if l.count() != 1 {
		t.Fatalf("one resume expected once all are answered, got %d", l.count())
	}
	if stored, _ := sessions.LoadPendingQuestions(meta.ID); len(stored) != 0 {
		t.Fatal("entries must be removed when the resume starts")
	}
}

func TestAllDismissedStartsNothing(t *testing.T) {
	areg, sreg, meta := setupTurn(t)
	l := &launched{}
	newResumeCoordinator(areg, sreg, l.fn).restore([]*sessions.SessionMeta{meta})
	_ = areg.Resolve(meta.ID, "a", askuser.Answer{Cancelled: true})
	_ = areg.Resolve(meta.ID, "b", askuser.Answer{Cancelled: true})
	if l.count() != 0 {
		t.Fatal("every question dismissed: nothing to resume")
	}
	if stored, _ := sessions.LoadPendingQuestions(meta.ID); len(stored) != 0 {
		t.Fatal("dismissed entries must be removed")
	}
}

// The server died after the last answer but before the resume ran.
func TestBootResumesAFullyAnsweredTurn(t *testing.T) {
	areg, sreg, meta := setupTurn(t)
	_ = sessions.SetPendingAnswer(meta.ID, "a", askuser.Answer{Text: "prod"})
	_ = sessions.SetPendingAnswer(meta.ID, "b", askuser.Answer{Text: "v2"})
	meta.PendingQuestions, _ = sessions.LoadPendingQuestions(meta.ID)
	l := &launched{}
	newResumeCoordinator(areg, sreg, l.fn).restore([]*sessions.SessionMeta{meta})
	if l.count() != 1 || len(areg.Pending(meta.ID)) != 0 {
		t.Fatalf("expected an immediate resume and no restored question, launches=%d", l.count())
	}
}

func TestArchivedSessionIsNotResumed(t *testing.T) {
	areg, sreg, meta := setupTurn(t)
	sreg.SetArchived(meta.ID, true)
	l := &launched{}
	newResumeCoordinator(areg, sreg, l.fn).restore([]*sessions.SessionMeta{meta})
	if len(areg.Pending(meta.ID)) != 0 || l.count() != 0 {
		t.Fatal("an archived session's questions must not be restored")
	}
}
```

Create `server/reseed_test.go`:

```go
package main

import (
	"context"
	"testing"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

type fakeReseeder struct {
	has     bool
	reseeds int
}

func (f *fakeReseeder) HasSessionContext(context.Context, string, string, string) bool { return f.has }
func (f *fakeReseeder) ReseedSessionContext(context.Context, string, string, string, []toolkitagent.Exchange) error {
	f.reseeds++
	return nil
}

func TestReseedIfCold(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_ = sessions.AppendConversationTurn("s1", "hi", "hello")

	cold := &fakeReseeder{has: false}
	reseedIfCold(context.Background(), cold, "u", "s1", "system")
	if cold.reseeds != 1 {
		t.Fatal("a session with turns but no in-memory context must be reseeded")
	}
	warm := &fakeReseeder{has: true}
	reseedIfCold(context.Background(), warm, "u", "s1", "system")
	if warm.reseeds != 0 {
		t.Fatal("a warm session must not be reseeded")
	}
	empty := &fakeReseeder{has: false}
	reseedIfCold(context.Background(), empty, "u", "no-turns", "system")
	if empty.reseeds != 0 {
		t.Fatal("a session with no persisted turns has nothing to reseed")
	}
}
```

Check the real import alias of the `agent` package in `server/` (`grep -n '"github.com/blouargant/omnis/agent"' server/*.go`) and the
`ReseedSessionContext` signature in `agent/session_reseed.go`; adapt the fake,
not the assertions.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./server/ -run 'Resume|Dismissed|Archived|Reseed'`
Expected: build failure.

- [ ] **Step 3: Implement the reseed helper**

`server/reseed.go`:

```go
package main

import (
	"context"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

// contextReseeder is the part of *agent.Manager the reseed needs.
type contextReseeder interface {
	HasSessionContext(ctx context.Context, userID, sessionID, squad string) bool
	ReseedSessionContext(ctx context.Context, userID, sessionID, squad string, ex []toolkitagent.Exchange) error
}

// reseedIfCold rebuilds a squad's in-memory model context from the persisted
// transcript when the session has turns but the squad holds none — the first
// turn after a restart. Best-effort; a failure is logged by the Manager.
func reseedIfCold(ctx context.Context, m contextReseeder, userID, sessionID, squad string) {
	if m.HasSessionContext(ctx, userID, sessionID, squad) {
		return
	}
	f, err := sessions.LoadConversationFile(sessionID)
	if err != nil || f == nil || len(f.Turns) == 0 {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, reseedTimeout)
	defer cancel()
	_ = m.ReseedSessionContext(rctx, userID, sessionID, squad, toExchanges(f.Turns))
}
```

In `server/sse.go`,
replace the reseed block (the `if meta.Turns > 0 && !d.Manager.HasSessionContext(…) { … }`)
with:

```go
			if meta.Turns > 0 {
				reseedIfCold(d.rootCtx, d.Manager, meta.UserID, meta.ID, startSquad)
			}
```

Keep the explanatory comment above it. `reseedTimeout` and `toExchanges`
already exist in `server/`.

- [ ] **Step 4: Implement the injection options and reseed in injected turns**

In `server/mailbox_push.go`:

```go
// injectOpts describes one injected turn. PersistPrompt is the user text saved
// in the transcript; it defaults to RouterPrompt.
type injectOpts struct {
	AnswerPrompt, RouterPrompt, PersistPrompt, SSEEvent, ReplyTo string
}

func (pm *pushManager) injectTurnRouted(ctx context.Context, d serverDeps, sessionID, userID, answerPrompt, routerPrompt, sseEvent, replyTo string) string {
	return pm.injectTurnOpts(ctx, d, sessionID, userID, injectOpts{
		AnswerPrompt: answerPrompt, RouterPrompt: routerPrompt, SSEEvent: sseEvent, ReplyTo: replyTo,
	})
}
```

Rename the existing body to `injectTurnOpts(ctx, d, sessionID, userID string, o injectOpts) (reply string)`,
replacing `answerPrompt`/`routerPrompt`/`sseEvent`/`replyTo` with the `o.` fields.
At the top of the body add `if o.PersistPrompt == "" { o.PersistPrompt = o.RouterPrompt }`,
persist `o.PersistPrompt` in the `AppendConversationTurnFull` call, and right
after the `LookupSquad` check add:

```go
	// First injected turn after a restart: rebuild the model's context from
	// the transcript (mailbox, schedules, spawn and durable-question resumes).
	if meta.Turns > 0 {
		reseedIfCold(ctx, d.Manager, userID, sessionID, meta.Squad)
	}
```

- [ ] **Step 5: Implement the coordinator**

`server/durable_resume.go`:

```go
package main

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/sessions"
)

// resumeCoordinator restores durable questions at boot and, once every question
// of an interrupted turn is answered, launches one resume turn.
type resumeCoordinator struct {
	areg   *askuser.Registry
	sreg   *sessions.Registry
	launch func(sessionID, userID, modelPrompt, display string)
	mu     sync.Mutex // serialises answer bookkeeping per process
}

func newResumeCoordinator(areg *askuser.Registry, sreg *sessions.Registry,
	launch func(sessionID, userID, modelPrompt, display string)) *resumeCoordinator {
	return &resumeCoordinator{areg: areg, sreg: sreg, launch: launch}
}

// restore re-registers every unanswered stored question. A turn whose
// questions are all already answered (the server died before its resume ran)
// is resumed right away. Archived sessions are cleaned up instead.
func (c *resumeCoordinator) restore(metas []*sessions.SessionMeta) {
	for _, m := range metas {
		if len(m.PendingQuestions) == 0 {
			continue
		}
		if m.Archived {
			_ = sessions.ClearPendingQuestions(m.ID)
			continue
		}
		for turnID, entries := range groupByTurn(m.PendingQuestions) {
			if allAnswered(entries) {
				c.finish(m.ID, turnID, entries)
				continue
			}
			for _, e := range entries {
				if e.Answer != nil {
					continue
				}
				q := e.Question
				q.SessionID = m.ID
				c.areg.Restore(q, c.onAnswer)
			}
		}
	}
}

// onAnswer records the answer on disk, then resumes when the turn is complete.
func (c *resumeCoordinator) onAnswer(q askuser.Question, ans askuser.Answer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if meta, ok := c.sreg.Get(q.SessionID); !ok || meta.Archived {
		return
	}
	if err := sessions.SetPendingAnswer(q.SessionID, q.ID, ans); err != nil {
		log.Printf("durable ask: record answer %s/%s: %v", q.SessionID, q.ID, err)
		return
	}
	all, err := sessions.LoadPendingQuestions(q.SessionID)
	if err != nil {
		return
	}
	var turnID string
	for _, e := range all {
		if e.Question.ID == q.ID {
			turnID = e.TurnID
		}
	}
	entries := groupByTurn(all)[turnID]
	if turnID != "" && allAnswered(entries) {
		c.finish(q.SessionID, turnID, entries)
	}
}

// finish removes a completed turn's entries (at most once) and launches its
// resume, unless every question was dismissed.
func (c *resumeCoordinator) finish(sessionID, turnID string, entries []sessions.PendingQuestion) {
	if err := sessions.RemovePendingTurn(sessionID, turnID); err != nil {
		log.Printf("durable ask: clear turn %s/%s: %v", sessionID, turnID, err)
		return
	}
	if allDismissed(entries) {
		return
	}
	meta, ok := c.sreg.Get(sessionID)
	if !ok {
		return
	}
	turns, _ := sessions.LoadConversationTurns(sessionID)
	c.launch(sessionID, meta.UserID, buildResumePrompt(entries), buildResumeDisplay(entries, len(turns)))
}

func groupByTurn(in []sessions.PendingQuestion) map[string][]sessions.PendingQuestion {
	out := map[string][]sessions.PendingQuestion{}
	for _, e := range in {
		out[e.TurnID] = append(out[e.TurnID], e)
	}
	return out
}

func allAnswered(es []sessions.PendingQuestion) bool {
	for _, e := range es {
		if e.Answer == nil {
			return false
		}
	}
	return len(es) > 0
}

func allDismissed(es []sessions.PendingQuestion) bool {
	for _, e := range es {
		if e.Answer == nil || !e.Answer.Cancelled {
			return false
		}
	}
	return true
}

// answerText renders an answer the way the user gave it.
func answerText(a *askuser.Answer) string {
	parts := append([]string(nil), a.Selected...)
	if t := strings.TrimSpace(a.Text); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, ", ")
}

// buildResumePrompt is what the model receives to continue an interrupted turn.
func buildResumePrompt(entries []sessions.PendingQuestion) string {
	var b strings.Builder
	b.WriteString("[Resumed after a server restart] Your previous turn was interrupted while you were waiting for the user's answer.\n\n")
	if len(entries) > 0 && strings.TrimSpace(entries[0].Prompt) != "" {
		fmt.Fprintf(&b, "Original request:\n%s\n\n", strings.TrimSpace(entries[0].Prompt))
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "You asked: %s\n", strings.TrimSpace(e.Question.Prompt))
		if e.Answer.Cancelled {
			b.WriteString("The user dismissed this question without answering.\n\n")
		} else {
			fmt.Fprintf(&b, "The user answered: %s\n\n", answerText(e.Answer))
		}
	}
	b.WriteString("Continue the task from where you left off. Work done during the interrupted turn may be lost, so re-check anything you relied on before continuing.")
	return b.String()
}

// buildResumeDisplay is the user text saved in the transcript. When the
// interrupted turn was persisted since the question (graceful shutdown), it is
// already in the history, so only the answers are shown.
func buildResumeDisplay(entries []sessions.PendingQuestion, persistedTurns int) string {
	var lines []string
	if len(entries) > 0 && persistedTurns <= entries[0].TurnCount && strings.TrimSpace(entries[0].Prompt) != "" {
		lines = append(lines, strings.TrimSpace(entries[0].Prompt), "")
	}
	for _, e := range entries {
		ans := answerText(e.Answer)
		if e.Answer.Cancelled {
			ans = "(dismissed)"
		}
		lines = append(lines, fmt.Sprintf("↪ Answer to %q: %s", strings.TrimSpace(e.Question.Prompt), ans))
	}
	return strings.Join(lines, "\n")
}
```

Note `c.launch` is called with `c.mu` held; the server's launch must return
immediately (it starts a goroutine), which Step 6 guarantees.

- [ ] **Step 6: Wire it into the server**

In `server/main.go`, after `deps` is built (the `serverDeps` literal that has
`rootCtx: rootCtx`) and before `startCollectionAutoUpdate`:

```go
	// Durable agent questions: store AskUserQuestion prompts in the conversation
	// file so they survive a restart, and restore the ones a previous run left.
	if infra.AskUserRegistry != nil {
		infra.AskUserRegistry.SetPersister(newAskPersister(rootCtx, deps.LiveTurns, registry))
		resume := newResumeCoordinator(infra.AskUserRegistry, registry,
			func(sessionID, userID, modelPrompt, display string) {
				go func() {
					if deps.PushEvents != nil {
						deps.PushEvents.broadcastWithText("turn_started", sessionID, display)
					}
					deps.PushMgr.injectTurnOpts(rootCtx, deps, sessionID, userID, injectOpts{
						AnswerPrompt: modelPrompt, RouterPrompt: modelPrompt,
						PersistPrompt: display, SSEEvent: "mailbox_push",
					})
				}()
			})
		resume.restore(registry.List())
	}
```

`sessions.NewRegistry` stores the `*SessionMeta` values returned by
`LoadPersistedSessions` as-is, so `registry.List()` carries `PendingQuestions`
at boot. `deps.PushMgr`, `deps.LiveTurns` and `deps.PushEvents` are existing
`serverDeps` fields.

In `server/spawn.go` `forgetSessionState`, add at the end:

```go
	if d.AskUserRegistry != nil {
		d.AskUserRegistry.CancelSession(id)
	}
	_ = sessions.ClearPendingQuestions(id) // no-op once the file is deleted
```

`forgetSessionState` runs on both delete (after the conversation file is gone)
and archive.

- [ ] **Step 7: Run the tests**

Run: `go test -race ./server/ ./internal/... ./core/...`
Expected: PASS. Then `go build ./... && go vet ./...`.

- [ ] **Step 8: Commit**

```bash
git add server/
git commit -m "feat(server): resume a task when a question from before a restart is answered

Also reseeds the model context on the first injected turn after a restart
(mailbox, schedules, spawn), closing a known gap."
```

---

### Task 6: Web UI label, docs, live end-to-end check

**Files:**
- Modify: `web/app.js` (`renderSingleStepBody`)
- Modify: `web/css/features/dialogs.css`
- Modify: `web/i18n/{en,fr,es,de}.json`, regenerate `web/i18n/locales.js`
- Modify: `web/index.html` (`?v=` bumps)
- Modify: `CLAUDE.md`, `internal/features/FEATURES.md`

- [ ] **Step 1: UI label**

In `web/app.js` `renderSingleStepBody`, before `card.appendChild(promptEl);`:

```js
  if (q.resumed) {
    const note = document.createElement("div");
    note.className = "ask-user-resumed";
    note.textContent = tr("app.askwizard.resumed");
    card.appendChild(note);
  }
```

In `web/css/features/dialogs.css`, next to `.ask-user-prompt`:

```css
.ask-user-resumed {
  font-size: 0.85em;
  color: var(--text-muted, inherit);
  margin-bottom: 6px;
}
```

(Use the muted-text token the neighbouring rules use; check the file.)

Add `"app.askwizard.resumed"` after `"app.askwizard.question"` in each catalogue:
- en: `"Asked before a server restart — your answer will resume the task."`
- fr: `"Question posée avant un redémarrage du serveur — votre réponse relancera la tâche."`
- es: `"Pregunta hecha antes de reiniciar el servidor: tu respuesta reanudará la tarea."`
- de: `"Vor einem Serverneustart gestellt – deine Antwort setzt die Aufgabe fort."`

Run `make i18n`, then in `web/index.html` bump `locales.js?v=63` → `64`,
`i18n.js?v=2` → `3` only if `i18n.js` changed (it does not — leave it), and
`app.js?v=92` → `93`.

- [ ] **Step 2: Docs**

CLAUDE.md, in "No ask-user / permission timeout" section, append a paragraph:

```markdown
**Durable agent questions (survive a restart).** `AskUserQuestion` marks its
question `Durable`; the server installs an `askuser.Persister`
([server/durable_ask.go](server/durable_ask.go)) that stores it in the
session's `conversation_<id>.json` (`pending_questions`) with the running
turn's request, squad and persisted-turn count. A cancellation while the root
context is done (shutdown) keeps the entry; Stop, an answer, archive or delete
remove it. At boot, [server/durable_resume.go](server/durable_resume.go)
re-registers them as orphans (`Registry.Restore`, shown with a "resumed" label);
each answer is recorded on disk, and once every question of an interrupted turn
is answered one resume turn is injected (`injectTurnOpts`) with the original
request and the Q/A pairs. Only `AskUserQuestion` is durable: permission, hook,
budget, settings, dependency and MCP-input cards gate one tool call that does
not exist after a restart. `AskUserQuestion` now waits on the run context, so
Stop releases it (it used to wait on `context.Background()`). CLI/TUI install no
persister. `LoadPersistedSessions` also loads a turnless conversation that has
pending questions (an interrupted first turn), or the GC would delete it.
```

In "Context restore after a restart", replace the sentence about the A2A
known gap's scope so it says injected turns (`injectTurnOpts`) now reseed too;
only the A2A path remains un-reseeded.

FEATURES.md: the latest tag is `v1.9.1`, so `1.9` is released. If no
`## 1.10 (in development)` section exists, add one at the top:

```markdown
## 1.10 (in development) — Durable agent questions

- **Questions survive a restart** — a question an agent asked you is kept across a server restart or crash; answering it afterwards resumes the task.
```

Otherwise add the bullet to the existing in-development section. Run
`go test ./internal/features/`.

- [ ] **Step 3: Full verification**

Run: `go build ./... && go vet ./... && go test ./... && go test -race ./agent/... ./server/... ./internal/...`
Expected: all PASS.

- [ ] **Step 4: Live end-to-end**

Use the smoke launcher (isolated `OMNIS_HOME`, port 8848, credentials from the
repo `.env`; rebuild the server binary from this branch first). Then:

1. Create a session and send: "Before doing anything, ask me with AskUserQuestion which language I want the answer in (French or English), then tell me a one-line fun fact in that language."
2. Wait for the `ask_user` event; confirm `pending_questions` appears in `$OMNIS_HOME/logs/conversation_<id>.json`.
3. Stop the server with SIGTERM; confirm the entry is still in the file.
4. Restart; `GET /api/events` (or the per-session stream) replays the question with `"resumed": true`.
5. Answer via `POST /api/sessions/<id>/ask-user/<qid>` `{"selected":["French"]}`.
6. Confirm a new persisted turn appears whose assistant text is a fun fact in French, and `pending_questions` is gone.
7. Repeat steps 1–6 killing the server with `kill -9` instead of SIGTERM.
8. Also check Stop: ask again, `POST /api/sessions/<id>/cancel`, confirm the turn ends and the entry is removed.

- [ ] **Step 5: Commit**

```bash
git add web/ CLAUDE.md internal/features/FEATURES.md
git commit -m "feat(web): label questions resumed after a restart; document durable ask_user"
```
