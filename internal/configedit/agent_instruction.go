package configedit

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/omnis/internal/paths"
)

// agentInstructionBaseBelow resolves the agent's instruction body (YAML
// frontmatter stripped, trailing newlines trimmed) from the registry layers
// STRICTLY BELOW writeLayer — i.e. what the instruction would resolve to with no
// user override. It is the baseline a layer-aware save diffs the submitted
// instruction against: equal ⇒ the user left it unchanged (or restored it to the
// shipped default) ⇒ the override should be removed, not re-forked as a shadow
// copy that then shadows package updates. It mirrors
// agent.ReadAgentInstructionBelowLayer for the settings-tool write path so that
// configedit stays free of the agent package (which imports configedit).
func agentInstructionBaseBelow(name, writeLayer string) string {
	wr := layerRank(writeLayer)
	dirs := paths.AgentsRegistrySearchDirs() // high→low precedence
	for _, dir := range dirs {
		if layerRank(paths.Layer(dir)) >= wr {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, name, "instruction.md")); err == nil {
			return strings.TrimRight(stripFrontmatterBody(b), "\n")
		}
	}
	for _, dir := range dirs {
		if layerRank(paths.Layer(dir)) >= wr {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, "default.md")); err == nil {
			return strings.TrimRight(stripFrontmatterBody(b), "\n")
		}
	}
	return ""
}

// stripFrontmatterBody removes a leading `---` YAML frontmatter block and returns
// the markdown body. Pure string handling (no YAML parse), mirroring the body
// split in agent.ParseInstructionMarkdown so the comparison matches what the
// agent reader surfaces for well-formed frontmatter.
func stripFrontmatterBody(content []byte) string {
	s := string(content)
	trimmed := strings.TrimLeft(s, "\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return s
	}
	rest := trimmed[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return s
	}
	after := strings.TrimPrefix(rest[end:], "\n---")
	return strings.TrimLeft(after, "\n")
}

// isEmptyOverlayValue reports whether a per-agent field carries no override
// intent — a nil (JSON null) or an empty/whitespace string. Booleans, numbers,
// and arrays (including an empty one, a deliberate "cleared" list) are NOT empty.
// Dropping these lets an unchanged agent produce an empty overlay so no user-layer
// shadow is forked from the empty scalars the editor GET echoes back.
func isEmptyOverlayValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	default:
		return false
	}
}
