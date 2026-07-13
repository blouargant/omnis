package main

import (
	"github.com/blouargant/omnis/internal/budget"
)

// startTurnBudget arms the per-turn spend ceiling for a session about to run a
// turn. Every server-side turn entry point calls it — the interactive path
// (handleMessages) and the injected path (mailbox delivery, background-task
// notifications, scheduled routines, spawned tasks) — so no rail can start an
// unbounded run.
//
// The ceiling is read from the CURRENT generation's settings rather than the
// session's pinned one, so editing `turn_budget` and hot-reloading takes effect
// on the next turn of every session, including ones still draining on an older
// generation. It is a spend policy, not part of the agent tree.
//
// Nil store or unlimited limits ⇒ no turn is armed and the budget callbacks fall
// through, byte-identical to a build without the budget.
func (d serverDeps) startTurnBudget(sessionID string) {
	if d.Budget == nil || sessionID == "" {
		return
	}
	d.Budget.StartTurn(sessionID, d.turnLimits())
}

// turnLimits resolves the configured per-turn ceiling from the live generation.
func (d serverDeps) turnLimits() budget.Limits {
	if d.Manager == nil {
		return budget.Limits{}
	}
	inst := d.Manager.Current()
	if inst == nil {
		return budget.Limits{}
	}
	return inst.Settings.TurnBudget
}
