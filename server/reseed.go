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
