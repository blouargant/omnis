package main

import (
	"strings"

	toolkitagent "github.com/blouargant/omnis/agent"
)

// resolveStartingSquad picks the squad a new chat starts on:
//
//	explicit team pick → collection default → explicit router → router → default
//
// The router sits BELOW the collection default on purpose. Routing to it is not
// a choice of team, it is "decide for me" — and a collection whose profile names
// a squad has already answered that question, more specifically. The web UI also
// has no way to express "no preference": loadSquads() seeds its picker from the
// default reported by /api/squads (the router when routing is on) and newChat()
// sends that as body.squad on every new chat, so honouring an explicit router as
// a real pick made a collection's default squad unreachable from the browser.
// That cost the context too, since the collection-context plugin is deliberately
// not mounted on the router — a chat filed under a collection started on a root
// that could not see the collection's instructions, and mis-routed from there.
//
// A real team pick (the squad menu, the empty-pane picker) still wins over the
// collection default, and an explicit router still wins when the collection has
// no usable default squad.
//
// The collection default applies only when that squad still exists — a stale or
// deleted profile squad falls through rather than 400ing a new chat. It stays a
// seed, not a lock: routing still runs and the seeded squad can hand back.
//
// hasSquad / routerSquad come from the live Manager; both are nil/"" on surfaces
// or tests without one, which collapses to the plain default — the pre-seed
// behaviour.
func resolveStartingSquad(explicit, profileSquad string, hasSquad func(string) bool, routerSquad string) string {
	e := strings.ToLower(strings.TrimSpace(explicit))
	if e != "" && e != routerSquad {
		return e
	}
	if ps := strings.ToLower(strings.TrimSpace(profileSquad)); ps != "" && hasSquad != nil && hasSquad(ps) {
		return ps
	}
	if e != "" {
		return e
	}
	if routerSquad != "" {
		return routerSquad
	}
	return toolkitagent.DefaultSquadName
}
