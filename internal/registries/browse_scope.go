package registries

import (
	"path"
	"strings"
)

// This file scopes the "loose" browsers so a multi-kind registry (one repo that
// serves several kinds from conventional sub-directories) doesn't mis-classify a
// sibling kind's files as its own. The marker-based browsers (skills → SKILL.md,
// agents' native agent.json, a2a → a2a.json, squads → squad.json, permissions →
// permissions.json) already scope precisely by a unique filename. But three
// browsers match broadly and would otherwise pull in foreign files:
//
//   - BrowseCommands matches any *.md,
//   - BrowseAgents matches any *.md (Claude-format single-file agents),
//   - BrowseMCPTools falls back to "any *.json".
//
// e.g. in a marketplace repo laid out as plugins/<group>/{skills,agents,commands}/…
// the Commands view would list agent .md files, SKILL.md, and skill references/*.md.
// belongsToForeignKind lets those browsers skip anything that plainly belongs to
// another kind.

// kindDirOwner maps a conventional directory segment to the kind that owns it.
// A multi-kind registry separates content by these directories; "references" is
// a skill-internal docs directory that is never a top-level installable item.
var kindDirOwner = map[string]string{
	"skills":      KindSkills,
	"agents":      KindAgents,
	"commands":    KindCommands,
	"mcp":         KindMCP,
	"mcp-servers": KindMCP,
	"a2a":         KindA2A,
	"a2a-agents":  KindA2A,
	"squads":      KindSquads,
	"permissions": KindPermissions,
	"references":  "references",
}

// kindMarkerOwner maps a well-known manifest/marker filename to the kind that
// owns it, so a permissive browser never mistakes another kind's marker for its
// own (notably MCP's "any *.json" must not pick up agent.json/squad.json/…).
var kindMarkerOwner = map[string]string{
	"SKILL.md":        KindSkills,
	AgentManifestFile: KindAgents,      // agent.json
	MCPManifestFile:   KindMCP,         // mcp.json
	MCPMarkdownFile:   KindMCP,         // mcp.md
	A2AManifestFile:   KindA2A,         // a2a.json
	SquadManifestFile: KindSquads,      // squad.json
	PermissionFile:    KindPermissions, // permissions.json
}

// belongsToForeignKind reports whether filePath plainly belongs to a kind OTHER
// than selfKind — either because its basename is another kind's marker file, or
// because it sits under another kind's conventional directory. Files not under
// any recognised kind directory (the single-purpose layout, where the registry
// URL points straight at the kind's own directory) are never considered foreign,
// so that layout keeps working unchanged.
func belongsToForeignKind(filePath, selfKind string) bool {
	if owner, ok := kindMarkerOwner[path.Base(filePath)]; ok && owner != selfKind {
		return true
	}
	dir := path.Dir(filePath)
	if dir == "." || dir == "/" || dir == "" {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if owner, ok := kindDirOwner[seg]; ok && owner != selfKind {
			return true
		}
	}
	return false
}
