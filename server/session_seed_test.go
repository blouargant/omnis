package main

import (
	"testing"

	toolkitagent "github.com/blouargant/omnis/agent"
)

func hasSquadSet(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(s string) bool { return set[s] }
}

func TestResolveStartingSquad(t *testing.T) {
	tests := []struct {
		name              string
		explicit, profile string
		hasSquad          func(string) bool
		router            string
		start             string
		want              string
	}{
		{"explicit wins over profile", "Coding", "kubernetes", hasSquadSet("coding", "kubernetes"), "omnis", "", "coding"},
		// The web UI's squad picker has no "unset" state: loadSquads() falls back
		// to the default reported by /api/squads — the router — and newChat() sends
		// that as body.squad on EVERY new chat. So an explicit router must not
		// count as a team pick, or a collection's default squad is unreachable
		// from the browser (the reported bug: a new chat in a collection whose
		// profile names "knowledge" started on the router, which then mis-routed
		// it — the router being the one root the collection-context plugin is
		// deliberately not mounted on).
		{"explicit router yields to the collection default", "Omnis", "Knowledge", hasSquadSet("knowledge", "omnis"), "omnis", "", "knowledge"},
		{"explicit router with no collection default stays on the router", "omnis", "", hasSquadSet("omnis"), "omnis", "", "omnis"},
		{"explicit router with a stale collection default stays on the router", "omnis", "Ghost", hasSquadSet("omnis"), "omnis", "", "omnis"},
		{"routing disabled: explicit still wins over the collection default", "Coding", "kubernetes", hasSquadSet("coding", "kubernetes"), "", "", "coding"},
		{"seed from collection profile", "", "Kubernetes", hasSquadSet("kubernetes"), "omnis", "", "kubernetes"},
		{"stale profile squad falls through to router", "", "Ghost", hasSquadSet("kubernetes"), "omnis", "", "omnis"},
		{"no profile → router", "", "", hasSquadSet("kubernetes"), "omnis", "", "omnis"},
		{"no router → default squad", "", "", hasSquadSet("kubernetes"), "", "", toolkitagent.DefaultSquadName},
		{"nil manager → default squad", "", "Kubernetes", nil, "", "", toolkitagent.DefaultSquadName},
		// A deployment's start_squad replaces the router as the new-chat default.
		{"start squad is the default", "", "", hasSquadSet("omnis", "ia parc"), "omnis", "ia parc", "ia parc"},
		{"explicit start squad (sent by the browser) yields to the collection default", "IA Parc", "Coding", hasSquadSet("coding", "ia parc"), "omnis", "ia parc", "coding"},
		{"explicit start squad with no collection default stays", "ia parc", "", hasSquadSet("ia parc"), "omnis", "ia parc", "ia parc"},
		{"a deliberate router pick is still honoured", "omnis", "", hasSquadSet("omnis", "ia parc"), "omnis", "ia parc", "omnis"},
		{"a real team pick still wins", "Coding", "", hasSquadSet("coding", "ia parc"), "omnis", "ia parc", "coding"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveStartingSquad(tc.explicit, tc.profile, tc.hasSquad, tc.router, tc.start); got != tc.want {
				t.Fatalf("resolveStartingSquad(%q,%q,…,%q) = %q, want %q",
					tc.explicit, tc.profile, tc.router, got, tc.want)
			}
		})
	}
}
