package main

import (
	"net/http"
	"testing"

	"github.com/blouargant/omnis/internal/askuser"
)

// Resolve falls back to a cross-session lookup by question id (for MCP input
// prompts registered under a sub-agent session). In cookie mode that fallback
// must not let bob answer alice's question from his own session.
func TestAskUserAnswerRefusesOtherUsersQuestion(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	d.AskUserRegistry = askuser.NewRegistry()
	r := newEngine(d)
	answered := make(chan struct{}, 1)
	d.AskUserRegistry.Restore(askuser.Question{ID: "q-alice", SessionID: aliceSID, Kind: askuser.KindText, Prompt: "secret?"},
		func(askuser.Question, askuser.Answer) { answered <- struct{}{} })

	if w := doWithBody(r, "tb", "POST", "/api/sessions/"+bobSID+"/ask-user/q-alice", `{"text":"hijack"}`); w.Code != http.StatusNotFound {
		t.Fatalf("bob answering alice's question: got %d %s, want 404", w.Code, w.Body)
	}
	if len(d.AskUserRegistry.Pending(aliceSID)) != 1 {
		t.Fatal("alice's question must still be pending")
	}
	if w := doWithBody(r, "ta", "POST", "/api/sessions/"+aliceSID+"/ask-user/q-alice", `{"text":"mine"}`); w.Code != http.StatusNoContent {
		t.Fatalf("alice answering her own question: %d %s", w.Code, w.Body)
	}
	<-answered
}
