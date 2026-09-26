package sessindex

import (
	"context"
	"testing"
)

// seedOwned lays down alice's and bob's sessions, both mentioning "rollout",
// and installs an owner resolver (alice-caller is alice's own hidden search
// session, the id the agent's tool context carries).
func seedOwned(t *testing.T) {
	t.Helper()
	seedCorpus(t)
	writeConv(t, "alice-chat", conv{Title: "Alice rollout", Turns: []turn{{UserText: "plan the rollout", AssistantText: "alice secret rollout plan", At: at(2)}}})
	writeConv(t, "bob-chat", conv{Title: "Bob rollout", Turns: []turn{{UserText: "plan the rollout", AssistantText: "bob secret rollout plan", At: at(2)}}})
	owners := map[string]string{"alice-chat": "alice", "alice-caller": "alice", "bob-chat": "bob", "bob-caller": "bob"}
	SetOwnerResolver(func(id string) string { return owners[id] })
	t.Cleanup(func() { SetOwnerResolver(nil) })
}

func TestSearchIsScopedToCallersOwner(t *testing.T) {
	seedOwned(t)
	res, err := searchSessions(context.Background(), Deps{}, "bob-caller", searchIn{Query: "rollout"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].SessionID != "bob-chat" {
		t.Fatalf("bob must only find his own session, got %+v", res.Results)
	}
}

func TestReadRefusesOtherUsersSession(t *testing.T) {
	seedOwned(t)
	if _, err := readSession("bob-caller", readIn{SessionID: "alice-chat"}); err == nil {
		t.Fatal("bob must not read alice's transcript")
	}
	if out, err := readSession("alice-caller", readIn{SessionID: "alice-chat"}); err != nil || len(out.Turns) != 1 {
		t.Fatalf("alice reading her own session: %+v %v", out, err)
	}
}

func TestListIsScopedToCallersOwner(t *testing.T) {
	seedOwned(t)
	out := listSessions("alice-caller", listIn{})
	if len(out.Sessions) != 1 || out.Sessions[0].SessionID != "alice-chat" {
		t.Fatalf("alice must list only her own sessions, got %+v", out.Sessions)
	}
}

// No resolver (single-user): everything searchable is visible, as before.
func TestNoResolverMeansNoScoping(t *testing.T) {
	seedOwned(t)
	SetOwnerResolver(nil)
	res, err := searchSessions(context.Background(), Deps{}, "bob-caller", searchIn{Query: "rollout"})
	if err != nil || len(res.Results) != 2 {
		t.Fatalf("unscoped search = %+v %v", res.Results, err)
	}
	if _, err := readSession("bob-caller", readIn{SessionID: "alice-chat"}); err != nil {
		t.Fatalf("unscoped read refused: %v", err)
	}
}
