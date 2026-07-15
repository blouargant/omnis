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
		want              string
	}{
		{"explicit wins over profile", "Coding", "kubernetes", hasSquadSet("coding", "kubernetes"), "omnis", "coding"},
		{"seed from collection profile", "", "Kubernetes", hasSquadSet("kubernetes"), "omnis", "kubernetes"},
		{"stale profile squad falls through to router", "", "Ghost", hasSquadSet("kubernetes"), "omnis", "omnis"},
		{"no profile → router", "", "", hasSquadSet("kubernetes"), "omnis", "omnis"},
		{"no router → default squad", "", "", hasSquadSet("kubernetes"), "", toolkitagent.DefaultSquadName},
		{"nil manager → default squad", "", "Kubernetes", nil, "", toolkitagent.DefaultSquadName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveStartingSquad(tc.explicit, tc.profile, tc.hasSquad, tc.router); got != tc.want {
				t.Fatalf("resolveStartingSquad(%q,%q,…,%q) = %q, want %q",
					tc.explicit, tc.profile, tc.router, got, tc.want)
			}
		})
	}
}
