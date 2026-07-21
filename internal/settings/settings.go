// Package settings provides the in-process "settings" tool group mounted on the
// Helper agent. It lets a user, in chat, read and change any omnis setting —
// UI preferences (theme/locale/notifications), agents, the models catalogue and
// providers, squads, permissions, hooks, MCP servers and A2A peers — and have
// the change written to the right config layer and hot-reloaded.
//
// All file IO goes through internal/configedit so the layer-aware write logic is
// shared with the web-UI editor (package main, which the agent process cannot
// import). Security-sensitive changes (permissions, hooks, provider credentials)
// are gated behind a process-wide Confirmer wired to the ask-user widget by the
// agent package; routine changes apply directly. With no Confirmer set (CLI/TUI),
// sensitive changes are declined — never silently applied.
package settings

import (
	"context"
	"sync"

	"github.com/blouargant/omnis/core/adk"
)

// Deps wires the tool group to its host surface. Mirrors registries.Deps.
type Deps struct {
	// RequestReload triggers a hot-reload of the agent generation so a settings
	// change is wired into the running fleet without a manual reload. Returns
	// whether a reload actually fired. The server wires this to Manager.Reload;
	// CLI/TUI leave it nil (changes apply on the next start).
	RequestReload func() bool
}

// Confirmer asks the user, in the active session, to approve a sensitive
// settings change. Returns true to proceed.
type Confirmer interface {
	Confirm(ctx context.Context, sessionID, summary string) (bool, error)
}

var (
	confirmerMu sync.RWMutex
	confirmer   Confirmer
)

// SetConfirmer registers the process-wide sensitive-change confirmer. Called
// once by the server/agent after the ask-user registry is built. Passing nil
// disables sensitive changes (they are declined rather than applied).
func SetConfirmer(c Confirmer) {
	confirmerMu.Lock()
	confirmer = c
	confirmerMu.Unlock()
}

// confirmSensitive asks the registered confirmer to approve a sensitive change.
// Returns (proceed, reason): proceed=false with a reason when there is no
// confirmer (off-surface safety) or the user declined.
func confirmSensitive(tc adk.ToolContext, summary string) (bool, string) {
	confirmerMu.RLock()
	c := confirmer
	confirmerMu.RUnlock()
	if c == nil {
		return false, "this change is security-sensitive and requires confirmation, which is not available on this surface"
	}
	ok, err := c.Confirm(tc, tc.SessionID(), summary)
	if err != nil {
		return false, "confirmation failed: " + err.Error()
	}
	if !ok {
		return false, "the user declined the change"
	}
	return true, ""
}

// reload runs the host's hot-reload hook if present and reports whether it fired.
func (d Deps) reload() bool {
	if d.RequestReload == nil {
		return false
	}
	return d.RequestReload()
}

// LoaderProtocol is appended to the instruction of any agent that mounts the
// "settings" tool group.
const LoaderProtocol = `
SETTINGS MANAGEMENT — you can read and change any omnis setting on the user's behalf:
- Read first: 'get_settings' shows the current value of a section (agents, models, permissions, mcp, a2a, hooks, squads, server, preferences) and which config layer it lives in (credentials are redacted). Call it with no 'section' to list the sections. Pair it with the documentation tools: the docs explain what a setting MEANS, get_settings shows its current VALUE.
- Change with the most specific tool: 'set_preference' (theme/locale/notifications), 'set_agent' (an agent's model_ref, enabled flag, tools, skills, etc.), 'set_model' (a models.json catalogue entry / provider connection). For anything else use the generic 'update_config' / 'remove_config' (a JSON-pointer edit of a section: permissions rules, hooks, MCP servers, A2A peers, squads).
- Only change a setting on an explicit user request, and confirm the exact change in plain language first. Security-sensitive changes (permissions, hooks, provider credentials) additionally pop a confirmation widget automatically — do not try to bypass it.
- Every write reports back the file it wrote, the layer, whether it hot-reloaded, and whether a server restart is required (an embedding-model change needs a restart, not just a reload). Relay that to the user honestly.
`
