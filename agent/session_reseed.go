// session_reseed.go — rebuild a session's in-memory model context from a
// flattened transcript, so the model "remembers" exactly the kept turns after a
// conversation fork or rewind.
//
// The on-disk display history (conversation_<id>.json) is the source of truth;
// this keeps the runner's in-memory session.Service in sync with a truncated /
// copied history. The reconstruction is deliberately text-only: tool calls,
// function responses, and attachments from the original turns are not replayed —
// the same fidelity the model already gets after a server restart (which drops
// in-memory context entirely) or a context compression.

package agent

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Exchange is one persisted user→assistant turn, flattened to plain text. The
// agent package cannot import internal/sessions (that package imports agent), so
// callers (the server) map their ConversationTurn slices onto this local type —
// mirroring conversationTurnShim in session_signals.go.
type Exchange struct {
	User      string
	Assistant string
}

// ReseedSessionContext rebuilds the in-memory model context for a session from a
// flattened transcript. activeSquad is the squad the session will run on next
// (resolved the same way the dispatch loop resolves it — a missing name falls
// back to the default squad); it is always (re)seeded so the next turn has
// context. Any *other* squad in the pinned generation is reseeded only when it
// already holds an in-memory session for this id (a routing-visited squad), so
// untouched squads are not fabricated.
//
// Best-effort: a per-squad failure is logged and does not abort the rest. A nil
// Manager, an empty sessionID, or a session with no live generation is a no-op.
func (m *Manager) ReseedSessionContext(ctx context.Context, userID, sessionID, activeSquad string, turns []Exchange) error {
	if m == nil || sessionID == "" {
		return nil
	}
	inst := m.Lookup(sessionID)
	if inst == nil {
		return nil
	}
	active := inst.Squad(activeSquad)
	if active == nil {
		active = inst.Default()
	}
	for _, sq := range inst.Squads {
		if sq == nil {
			continue
		}
		// Always seed the active squad; seed others only if they already have a
		// live in-memory session for this id (visited via routing).
		if sq != active && !squadHasSession(ctx, sq, userID, sessionID) {
			continue
		}
		if err := reseedSquadSession(ctx, sq, userID, sessionID, turns); err != nil {
			log.Printf("reseed: squad %q session %s: %v", sq.Name, sessionID, err)
		}
	}
	return nil
}

// squadHasSession reports whether sq's session service already holds an
// in-memory session for (userID, sessionID).
func squadHasSession(ctx context.Context, sq *SquadInstance, userID, sessionID string) bool {
	svc := sq.RunnerConfig.SessionService
	if svc == nil {
		return false
	}
	resp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   sq.RunnerConfig.AppName,
		UserID:    userID,
		SessionID: sessionID,
	})
	return err == nil && resp != nil && resp.Session != nil
}

// reseedSquadSession replaces sq's in-memory session for (userID, sessionID)
// with a fresh one carrying one user event + one model event per exchange.
func reseedSquadSession(ctx context.Context, sq *SquadInstance, userID, sessionID string, turns []Exchange) error {
	svc := sq.RunnerConfig.SessionService
	if svc == nil {
		return fmt.Errorf("squad has no session service")
	}
	appName := sq.RunnerConfig.AppName
	// Drop any existing in-memory session so the rebuild is exact. A missing
	// session is not an error here (the squad may never have run this id).
	_ = svc.Delete(ctx, &session.DeleteRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	resp, err := svc.Create(ctx, &session.CreateRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	sess := resp.Session

	// Author for model events: the leader's name when present (matches how ADK
	// records the coordinator's own turns), else a generic "model".
	modelAuthor := "model"
	if sq.Leader != nil {
		modelAuthor = sq.Leader.Name()
	}

	appendEvent := func(author, role, text string) error {
		ev := session.NewEvent("")
		ev.Author = author
		ev.TurnComplete = true
		ev.Content = &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}}
		return svc.AppendEvent(ctx, sess, ev)
	}
	for _, ex := range turns {
		if ex.User != "" {
			if err := appendEvent("user", "user", ex.User); err != nil {
				return fmt.Errorf("append user event: %w", err)
			}
		}
		if ex.Assistant != "" {
			if err := appendEvent(modelAuthor, "model", ex.Assistant); err != nil {
				return fmt.Errorf("append model event: %w", err)
			}
		}
	}
	return nil
}
