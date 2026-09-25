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
