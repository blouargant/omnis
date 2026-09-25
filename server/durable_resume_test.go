package main

import (
	"context"
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

// Archived after the questions were restored, then answered: no resume.
func TestArchivedAfterRestoreIsNotResumed(t *testing.T) {
	areg, sreg, meta := setupTurn(t)
	l := &launched{}
	newResumeCoordinator(areg, sreg, l.fn).restore([]*sessions.SessionMeta{meta})
	sreg.SetArchived(meta.ID, true)
	_ = areg.Resolve(meta.ID, "a", askuser.Answer{Text: "prod"})
	_ = areg.Resolve(meta.ID, "b", askuser.Answer{Text: "v2"})
	if l.count() != 0 {
		t.Fatal("a session archived before its last answer must not be resumed")
	}
}

// The resume launch opts into SkipIfArchived; every other injected turn does not.
func TestSkipInjected(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sreg := sessions.NewRegistry()
	meta := sreg.New("")
	resume := injectOpts{SkipIfArchived: true}
	if skipInjected(sreg, meta.ID, resume) || skipInjected(sreg, meta.ID, injectOpts{}) {
		t.Fatal("an active session must run")
	}
	sreg.SetArchived(meta.ID, true)
	if !skipInjected(sreg, meta.ID, resume) {
		t.Fatal("a resume must not run on an archived session")
	}
	if skipInjected(sreg, meta.ID, injectOpts{}) {
		t.Fatal("callers that did not opt in keep today's behaviour")
	}
	if !skipInjected(sreg, "unknown", injectOpts{}) {
		t.Fatal("an unknown session never runs")
	}
}

// A resume that returns before running must still end the web UI's processing
// state (turn_started was already broadcast). Other injected turns stay silent.
func TestResumeEarlyExitSignalsCompletion(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sreg := sessions.NewRegistry()
	meta := sreg.New("")
	sreg.SetArchived(meta.ID, true)

	recv := func(ch chan pushMsg) (pushMsg, bool) {
		select {
		case m := <-ch:
			return m, true
		case <-time.After(200 * time.Millisecond):
			return pushMsg{}, false
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		sid  string
	}{
		{"archived", context.Background(), meta.ID},
		{"deleted", context.Background(), "gone"},
		{"shutdown", cancelled, meta.ID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bc := newSessionPushBroadcaster()
			pm := newPushManager(newSessionRunGuard(), bc, nil, nil, true)
			d := serverDeps{Registry: sreg}
			ch := bc.subscribeAll()
			defer bc.unsubscribeAll(ch)

			pm.injectTurnOpts(c.ctx, d, c.sid, "u", injectOpts{SSEEvent: "mailbox_push", SkipIfArchived: true})
			if m, ok := recv(ch); !ok || m.Event != "mailbox_push" || m.SID != c.sid {
				t.Fatalf("resume early exit must broadcast mailbox_push, got %+v ok=%v", m, ok)
			}
			if c.name == "archived" {
				return // without the opt an archived session runs: not an early exit
			}
			pm.injectTurnOpts(c.ctx, d, c.sid, "u", injectOpts{SSEEvent: "mailbox_push"})
			if m, ok := recv(ch); ok {
				t.Fatalf("a non-resume early exit must stay silent, got %+v", m)
			}
		})
	}
}
