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
	go func() {
		_, _ = r.Ask(context.Background(), "s2", askuser.Question{ID: "other", Kind: askuser.KindText, Prompt: "p"})
	}()
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
