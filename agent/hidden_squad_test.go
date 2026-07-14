package agent

import (
	"strings"
	"testing"
)

// A hidden squad is a machine-facing entry point: runnable by name, but invisible
// to the router. If the router could see "Session Search" it would route ordinary
// conversations into a squad whose only agent searches past chats and cannot do
// anything else — a dead end for the user.
func TestHiddenSquadIsNotRoutable(t *testing.T) {
	rs := RuntimeSettings{
		RouterSquad: "omnis",
		Squads: []RuntimeSquadConfig{
			{Name: "omnis", Members: []string{"omnis"}},
			{Name: "default", Leader: "leader", Description: "General purpose."},
			{Name: "session search", Members: []string{"session_search"}, Description: "Finds past chats.", Hidden: true},
		},
	}

	for _, name := range routerSquadCatalogue(rs) {
		if name == "session search" {
			t.Fatalf("hidden squad offered to the router: %v", routerSquadCatalogue(rs))
		}
	}
	if block := routerCatalogueBlock(rs); strings.Contains(block, "session search") {
		t.Errorf("hidden squad described in the router's instruction:\n%s", block)
	}
	// The router catalogue still advertises the real squads.
	if block := routerCatalogueBlock(rs); !strings.Contains(block, "default") {
		t.Errorf("visible squad missing from the router catalogue:\n%s", block)
	}

	// ...but it remains resolvable by name, which is how the search box runs it.
	if _, ok := rs.Squad("session search"); !ok {
		t.Error("hidden squad is not resolvable by name — the search box could not run it")
	}
}
