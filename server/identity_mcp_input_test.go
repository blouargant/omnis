package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/mcp"
)

type subAgentToolCtx struct {
	context.Context
	id string
}

func (c subAgentToolCtx) SessionID() string { return c.id }

func waitPending(t *testing.T, reg *askuser.Registry, sid string) askuser.Question {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := reg.Pending(sid); len(p) == 1 {
			return p[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no pending question for session %s", sid)
	return askuser.Question{}
}

// An MCP ${input:id} prompt raised under a SUB-AGENT (ephemeral SessionID)
// lands on the owner's user-facing session, is answerable by her through the
// real router, and — with cookie-mode owner scoping installed — her answer is
// never served to bob's prompt for the same input.
func TestCookieModeMCPInputIsOwnedAndScoped(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	d.AskUserRegistry = askuser.NewRegistry()
	r := newEngine(d)
	defer installOwnerScoping(d.Registry)()

	res := mcp.NewInputResolver(d.AskUserRegistry)
	in := mcp.Input{ID: "pat", Type: mcp.InputPromptString}
	ctxFor := func(root string) context.Context {
		return subAgentToolCtx{Context: events.WithRootSession(context.Background(), root), id: "ephemeral-" + root}
	}

	got := make(chan string, 1)
	go func() { v, _ := res.Resolve(ctxFor(aliceSID), in); got <- v }()
	q := waitPending(t, d.AskUserRegistry, aliceSID)
	if w := doWithBody(r, "tb", "POST", "/api/sessions/"+bobSID+"/ask-user/"+q.ID, `{"text":"hijack"}`); w.Code != http.StatusNotFound {
		t.Fatalf("bob answering alice's MCP input: %d, want 404", w.Code)
	}
	if w := doWithBody(r, "ta", "POST", "/api/sessions/"+aliceSID+"/ask-user/"+q.ID, `{"text":"alice-secret"}`); w.Code != http.StatusNoContent {
		t.Fatalf("alice answering her MCP input: %d %s", w.Code, w.Body)
	}
	if v := <-got; v != "alice-secret" {
		t.Fatalf("alice value = %q", v)
	}

	gotB := make(chan string, 1)
	go func() { v, _ := res.Resolve(ctxFor(bobSID), in); gotB <- v }()
	qb := waitPending(t, d.AskUserRegistry, bobSID) // bob is prompted, not served alice's cache
	if w := doWithBody(r, "tb", "POST", "/api/sessions/"+bobSID+"/ask-user/"+qb.ID, `{"text":"bob-secret"}`); w.Code != http.StatusNoContent {
		t.Fatalf("bob answering his MCP input: %d %s", w.Code, w.Body)
	}
	if v := <-gotB; v != "bob-secret" {
		t.Fatalf("bob value = %q", v)
	}
}
