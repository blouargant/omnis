package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/askuser"
)

func TestPushVisible(t *testing.T) {
	cases := []struct {
		login string
		m     pushMsg
		want  bool
	}{
		{"", pushMsg{Event: "x", SID: "s", Owner: "bob"}, true}, // single-user: everything
		{"alice", pushMsg{Event: "session_created", SID: "s", Owner: "alice"}, true},
		{"alice", pushMsg{Event: "session_created", SID: "s", Owner: "bob"}, false},
		{"alice", pushMsg{Event: "session_deleted", SID: "s"}, false}, // unknown owner ⇒ withheld
		{"alice", pushMsg{Event: "collections_changed", Owner: "bob"}, false},
		{"alice", pushMsg{Event: "collections_changed", Owner: "alice"}, true},
		{"alice", pushMsg{Event: "update_available"}, true}, // global by nature
	}
	for i, c := range cases {
		if got := pushVisible(c.login, c.m); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestBroadcastResolvesOwner(t *testing.T) {
	b := newSessionPushBroadcaster()
	b.ownerOf = func(sid string) string { return map[string]string{"s1": "alice"}[sid] }
	ch := b.subscribeAll()
	defer b.unsubscribeAll(ch)
	b.broadcast("session_created", "s1")
	if m := <-ch; m.Owner != "alice" {
		t.Fatalf("owner not resolved: %+v", m)
	}
	b.broadcastOwned("session_deleted", "gone", "bob")
	if m := <-ch; m.Owner != "bob" {
		t.Fatalf("explicit owner lost: %+v", m)
	}
}

func TestEventsStreamIsScoped(t *testing.T) {
	r, d, aliceSID, bobSID := newCookieTestEngine(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: "tb"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	d.PushEvents.ownerOf = func(sid string) string {
		if m, ok := d.Registry.Snapshot(sid); ok {
			return m.UserID
		}
		return ""
	}
	time.Sleep(100 * time.Millisecond) // let the handler subscribe
	d.PushEvents.broadcast("session_renamed", aliceSID)
	d.PushEvents.broadcast("session_renamed", bobSID)
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			if strings.Contains(line, aliceSID) {
				t.Fatal("bob received alice's event")
			}
			if strings.Contains(line, bobSID) {
				return
			}
		}
	}
	t.Fatal("bob never received his own event")
}

// openEvents connects tok to /api/events and streams every "event:"/"data:"
// line pair (as "event|data") on the returned channel until ctx ends.
func openEvents(t *testing.T, ctx context.Context, url, tok string) <-chan string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/events", nil)
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: tok})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan string, 64)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		ev := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				ev = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				out <- ev + "|" + line
			}
		}
	}()
	return out
}

// waitFor reads lines until one contains want; it fails on any line containing
// forbidden (when non-empty) seen first, or on the deadline.
func waitFor(t *testing.T, lines <-chan string, want, forbidden string, deadline time.Duration) string {
	t.Helper()
	timer := time.After(deadline)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before %q", want)
			}
			if forbidden != "" && strings.Contains(l, forbidden) {
				t.Fatalf("received forbidden event %q", l)
			}
			if strings.Contains(l, want) {
				return l
			}
		case <-timer:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

// Spec §8: bob's /api/events stream carries none of alice's live ask_user or
// ask_user_cancel bus events; alice's stream carries them.
func TestEventsStreamScopesLiveAskUser(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	bus := events.NewBus()
	d.AgentEvents = newAgentEventBroadcaster(bus)
	srv := httptest.NewServer(newEngine(d))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bobLines := openEvents(t, ctx, srv.URL, "tb")
	aliceLines := openEvents(t, ctx, srv.URL, "ta")
	time.Sleep(100 * time.Millisecond) // let both handlers subscribe

	bus.Emit(events.EventAskUser, map[string]any{"session_id": aliceSID, "question_id": "qa", "prompt": "alice-only"})
	bus.Emit(events.EventAskUserCancel, map[string]any{"session_id": aliceSID, "question_id": "qa"})
	// Bob's own events are the sentinels: once both arrive, alice's (emitted
	// first, delivered in order) would already have shown up.
	bus.Emit(events.EventAskUser, map[string]any{"session_id": bobSID, "question_id": "qb", "prompt": "bob-only"})
	bus.Emit(events.EventAskUserCancel, map[string]any{"session_id": bobSID, "question_id": "qb"})

	waitFor(t, bobLines, "ask_user|", aliceSID, 2*time.Second)
	waitFor(t, bobLines, "ask_user_cancel|", aliceSID, 2*time.Second)
	if l := waitFor(t, aliceLines, "ask_user|", bobSID, 2*time.Second); !strings.Contains(l, "alice-only") {
		t.Fatalf("alice got the wrong question: %s", l)
	}
	waitFor(t, aliceLines, "ask_user_cancel|", bobSID, 2*time.Second)
}

// Spec §8: the connect-time replay of pending questions sends bob none of
// alice's; alice's replay includes hers.
func TestEventsStreamScopesAskUserReplay(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	d.AskUserRegistry = askuser.NewRegistry()
	d.AskUserRegistry.Restore(askuser.Question{ID: "qa", SessionID: aliceSID, Kind: askuser.KindText, Prompt: "alice-pending"}, func(askuser.Question, askuser.Answer) {})
	d.AskUserRegistry.Restore(askuser.Question{ID: "qb", SessionID: bobSID, Kind: askuser.KindText, Prompt: "bob-pending"}, func(askuser.Question, askuser.Answer) {})
	srv := httptest.NewServer(newEngine(d))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bobLines := openEvents(t, ctx, srv.URL, "tb")
	// The replay is written before the live loop; bob's own question is in it.
	// Drain the replay window and make sure alice's never appears.
	waitFor(t, bobLines, "bob-pending", "alice-pending", 2*time.Second)
	grace := time.After(300 * time.Millisecond)
	for done := false; !done; {
		select {
		case l := <-bobLines:
			if strings.Contains(l, "alice-pending") || strings.Contains(l, aliceSID) {
				t.Fatalf("bob was replayed alice's question: %s", l)
			}
		case <-grace:
			done = true
		}
	}
	aliceLines := openEvents(t, ctx, srv.URL, "ta")
	waitFor(t, aliceLines, "alice-pending", "bob-pending", 2*time.Second)
}
