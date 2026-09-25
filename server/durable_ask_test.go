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
