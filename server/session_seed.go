package main

import (
	"strings"

	toolkitagent "github.com/blouargant/omnis/agent"
)

// resolveStartingSquad picks the squad a new chat starts on. An explicit client
// choice always wins (validated separately by the caller). Otherwise the
// collection's default squad seeds it — a hint, not a lock: routing still runs
// and the seeded squad can hand back to the router — but only when that squad
// still exists (a stale/deleted profile squad falls through rather than 400ing a
// new chat). Otherwise the Omnis router when routing is enabled (routerSquad
// non-empty), else the default squad.
//
// hasSquad / routerSquad come from the live Manager; both are nil/"" on surfaces
// or tests without one, which collapses to the plain default — the pre-seed
// behaviour.
func resolveStartingSquad(explicit, profileSquad string, hasSquad func(string) bool, routerSquad string) string {
	if s := strings.ToLower(strings.TrimSpace(explicit)); s != "" {
		return s
	}
	if ps := strings.ToLower(strings.TrimSpace(profileSquad)); ps != "" && hasSquad != nil && hasSquad(ps) {
		return ps
	}
	if routerSquad != "" {
		return routerSquad
	}
	return toolkitagent.DefaultSquadName
}
