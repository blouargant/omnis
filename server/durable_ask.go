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

// Remove drops the stored question, except while the server is shutting
// down: the waiting run is already cancelled, so the question must survive. A
// cancellation keeps the entry as is; a real answer given during the shutdown
// drain is recorded on it, so the boot "all answered → resume" path resumes
// the task after the restart instead of losing the answer.
func (p *askPersister) Remove(q askuser.Question, ans askuser.Answer) {
	if p.root.Err() != nil {
		if !ans.Cancelled {
			if err := sessions.SetPendingAnswer(q.SessionID, q.ID, ans); err != nil {
				log.Printf("durable ask: record answer %s/%s during shutdown: %v", q.SessionID, q.ID, err)
			}
		}
		return
	}
	if err := sessions.RemovePendingQuestion(q.SessionID, q.ID); err != nil {
		log.Printf("durable ask: remove %s/%s: %v", q.SessionID, q.ID, err)
	}
}
