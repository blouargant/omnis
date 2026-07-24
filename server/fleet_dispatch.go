// fleet_dispatch.go — server side of fleet_dispatch. After a Conductor turn,
// drainFleetDispatches materialises one Driver per queued dispatch: a Coding-squad
// session rooted at the project's collection cwd and filed under its collection,
// running the task in the background with the result delivered back to the
// Conductor (reusing the spawn rail). Mirrors drainSpawns.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/blouargant/omnis/internal/fleet"
)

// fleetDriverOptions maps a fleet project to the spawnOptions for its Driver.
// Returns ok=false when the project can't be dispatched (unknown, or an engine
// with no squad yet — see fleet.EngineSquad). Pure + unit-testable: the caller
// re-resolves the project against the live fleet registry at drain time rather
// than trusting anything cached on the directive, so the project's cwd/engine
// are always current.
func fleetDriverOptions(projectName, userID string) (spawnOptions, bool) {
	for _, p := range fleet.Projects() {
		if !strings.EqualFold(p.Name, projectName) {
			continue
		}
		squad, ok := fleet.EngineSquad(p.Engine)
		if !ok {
			return spawnOptions{}, false
		}
		return spawnOptions{
			Squad:      squad,
			Title:      "driver: " + p.Name,
			Dir:        p.Cwd,
			Collection: p.Name,
			UserID:     userID,
		}, true
	}
	return spawnOptions{}, false
}

// drainFleetDispatches materialises every Driver the Conductor requested via
// fleet_dispatch during the just-finished turn on parentID, and delivers each
// Driver's result back to the Conductor. Uses the server root context (via
// materializeSession/runSpawnedTask) so a client disconnect / Stop never
// cancels a dispatch. Mirrors drainSpawns.
func drainFleetDispatches(d serverDeps, parentID, parentUserID string) {
	if d.Manager == nil {
		return
	}
	infra := d.Manager.Infra()
	if infra == nil || infra.FleetDispatches == nil {
		return
	}
	dirs := infra.FleetDispatches.Drain(parentID)
	for _, dd := range dirs {
		if dd == nil {
			continue
		}
		opts, ok := fleetDriverOptions(dd.Project, parentUserID)
		if !ok {
			continue // unknown/unsupported — the tool already reported it to the model
		}
		if d.Manager != nil && !d.Manager.HasSquad(opts.Squad) {
			log.Printf("fleet dispatch: skipping project %q — resolved squad %q does not exist", dd.Project, opts.Squad)
			continue // avoid materializeSession silently falling back to the System squad
		}
		// If the Conductor session is a forked experiment, isolate this project's
		// Driver in a git worktree instead of the project's main checkout.
		if meta, ok := d.Registry.Get(parentID); ok && meta.FleetExperiment {
			wt, err := fleetWorktreeDir(parentID, dd.Project, opts.Dir)
			if err != nil {
				// Don't dispatch into the main tree — that would break isolation.
				// Report the failure back to the Conductor so it can tell the user.
				runSpawnedTaskNotice(d, parentID, parentUserID, fmt.Sprintf(
					"Could not run project %q in this experiment: %v. Commit or stash that project's repo, then retry.", dd.Project, err))
				continue
			}
			opts.Dir = wt
		}
		meta := materializeSession(d, opts)
		if meta == nil {
			continue
		}
		// Deliver the Driver's result back into the Conductor session.
		runSpawnedTask(d, meta.ID, "driver: "+dd.Project, parentID, parentUserID, dd.Task)
	}
}
