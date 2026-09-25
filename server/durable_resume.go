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
	if archived, ok := c.sreg.IsArchived(q.SessionID); !ok || archived {
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
