package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/sessions"
)

// readChatReply drives GET /api/events through the real router, waits for the
// handler to register its multiplexed subscriber, fires one broadcast, and
// returns the decoded data object of the resulting chat_reply SSE frame.
func readChatReply(t *testing.T, send func(b *sessionPushBroadcaster)) map[string]any {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())

	bcast := newSessionPushBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := newEngine(serverDeps{
		Registry:   sessions.NewEmptyRegistry(),
		PushEvents: bcast,
		rootCtx:    ctx,
	})
	srv := httptest.NewServer(engine)
	defer srv.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	// The handler subscribes as it starts; broadcasting before that would drop the
	// frame on the floor, so wait until the subscriber is registered.
	deadline := time.Now().Add(2 * time.Second)
	for {
		bcast.mu.RLock()
		n := len(bcast.all)
		bcast.mu.RUnlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no /api/events subscriber registered")
		}
		time.Sleep(2 * time.Millisecond)
	}
	send(bcast)

	// Scan the stream for the chat_reply frame's data line.
	sc := bufio.NewScanner(resp.Body)
	sawEvent := false
	for sc.Scan() {
		line := sc.Text()
		if line == "event: chat_reply" {
			sawEvent = true
			continue
		}
		if sawEvent && strings.HasPrefix(line, "data: ") {
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				t.Fatalf("decode chat_reply data: %v", err)
			}
			return payload
		}
	}
	t.Fatal("chat_reply frame never arrived on /api/events")
	return nil
}

// TestChatReplyCarriesOriginClientID pins the origin-scoping contract: a turn
// started by a browser tags its chat_reply with that browser's client_id, so the
// user's OTHER devices can tell the finished turn is not theirs and stay quiet.
// Without it every connected browser counts as "away" from a session it is not
// displaying and raises an OS notification — a chat started on a phone pinged the
// desktop.
func TestChatReplyCarriesOriginClientID(t *testing.T) {
	payload := readChatReply(t, func(b *sessionPushBroadcaster) {
		b.broadcastFrom("chat_reply", "teaching-kite", "the answer", "browser-A")
	})
	if got := payload["session_id"]; got != "teaching-kite" {
		t.Fatalf("session_id = %v, want teaching-kite", got)
	}
	if got := payload["text"]; got != "the answer" {
		t.Fatalf("text = %v, want the answer", got)
	}
	if got := payload["client_id"]; got != "browser-A" {
		t.Fatalf("client_id = %v, want browser-A", got)
	}
}

// TestChatReplyWithoutOriginOmitsClientID is the no-op contract: a turn no browser
// started (spawned, scheduled, mailbox, A2A) carries no origin, so the field is
// absent and every client still notifies — the pre-existing behaviour.
func TestChatReplyWithoutOriginOmitsClientID(t *testing.T) {
	payload := readChatReply(t, func(b *sessionPushBroadcaster) {
		b.broadcastWithText("chat_reply", "teaching-kite", "the answer")
	})
	if _, ok := payload["client_id"]; ok {
		t.Fatalf("client_id present for an origin-less turn: %v", payload["client_id"])
	}
}
