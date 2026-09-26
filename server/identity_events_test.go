package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
