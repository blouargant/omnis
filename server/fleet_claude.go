package main

import (
	"github.com/blouargant/omnis/internal/claudecode"
	"github.com/blouargant/omnis/internal/sessions"
)

// installClaudeAllowlistResolver maps a driver session to its project's claude
// allowlist override: the session's collection profile's ClaudeAllowedTools
// (empty ⇒ claudecode.DefaultAllowedTools). A claude Driver session is filed
// under its project collection (materializeSession Collection), so the
// session's collection IS the project name. Called once at server startup,
// beside installFleetResolver — takes the session registry directly (the same
// one closed over by agent.SetCollectionResolver just above, in main.go),
// since the full serverDeps struct isn't built yet at that point in run().
func installClaudeAllowlistResolver(reg *sessions.Registry) {
	claudecode.SetAllowlistResolver(func(sessionID string) []string {
		return claudeAllowedToolsFor(reg, sessionID)
	})
}

// claudeAllowedToolsFor is the resolver logic split out from
// installClaudeAllowlistResolver so it is unit-testable directly (the
// claudecode package's resolver hook is a private variable — there is no
// exported way to invoke it back once installed). A missing session or a
// session with no collection (General) yields nil, which claudecode maps to
// DefaultAllowedTools.
func claudeAllowedToolsFor(reg *sessions.Registry, sessionID string) []string {
	if reg == nil {
		return nil
	}
	meta, ok := reg.Get(sessionID)
	if !ok || meta.Collection == "" {
		return nil
	}
	return sessions.CollectionProfileFull(meta.Collection).ClaudeAllowedTools
}
