package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/omnis/internal/paths"
)

// ReadAgentInstruction returns the agent's system instruction with any YAML
// frontmatter stripped. The lookup walks the registry search chain for
// <dir>/<name>/instruction.md and falls back to <dir>/default.md.
func ReadAgentInstruction(name string) string {
	raw, _ := readRawInstruction(name)
	if raw == "" {
		return ""
	}
	body := StripInstructionFrontmatter([]byte(raw))
	return strings.TrimRight(body, "\n")
}

// ReadAgentInstructionBelowLayer resolves the agent's instruction body (YAML
// frontmatter stripped, trailing newlines trimmed) from the registry layers
// STRICTLY BELOW writeLayer — i.e. what ReadAgentInstruction would return if the
// agent had no instruction.md at writeLayer or above. It is the baseline a
// layer-aware save diffs the submitted instruction against: equal ⇒ the user left
// it unchanged (or restored it to the shipped default) ⇒ the override should be
// removed rather than re-forked as a shadow copy. writeLayer is
// "system"|"user"|"local"; the per-agent instruction.md from the highest layer
// below it wins (first-wins, matching ReadAgentInstruction), then default.md.
func ReadAgentInstructionBelowLayer(name, writeLayer string) string {
	wr := registryLayerRank(writeLayer)
	dirs := paths.AgentsRegistrySearchDirs() // high→low precedence
	for _, dir := range dirs {
		if registryLayerRank(paths.Layer(dir)) >= wr {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, name, "instruction.md")); err == nil {
			return strings.TrimRight(StripInstructionFrontmatter(b), "\n")
		}
	}
	for _, dir := range dirs {
		if registryLayerRank(paths.Layer(dir)) >= wr {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, "default.md")); err == nil {
			return strings.TrimRight(StripInstructionFrontmatter(b), "\n")
		}
	}
	return ""
}

// registryLayerRank orders config layers low→high (system < user < local) so a
// below-layer read can skip the write layer and everything above it. Unknown
// layers rank as user (the common default write target).
func registryLayerRank(layer string) int {
	switch layer {
	case "system":
		return 0
	case "local":
		return 2
	default: // "user" and anything unrecognised
		return 1
	}
}

// ReadAgentInstructionFrontmatter returns the parsed YAML frontmatter at the
// top of registry/agents/<name>/instruction.md. Returns a zero value when the
// file is missing or has no frontmatter. default.md is intentionally NOT
// consulted — frontmatter is per-agent metadata, not a global fallback.
func ReadAgentInstructionFrontmatter(name string) InstructionFrontmatter {
	for _, dir := range paths.AgentsRegistrySearchDirs() {
		b, err := os.ReadFile(filepath.Join(dir, name, "instruction.md"))
		if err != nil {
			continue
		}
		fm, _ := ParseInstructionMarkdown(b)
		return fm
	}
	return InstructionFrontmatter{}
}

// readRawInstruction returns the raw instruction.md content for the named
// agent, falling back to default.md. The second return value reports whether
// the result came from the agent's own file (true) or the default fallback
// (false). Callers that only need the body can ignore it.
func readRawInstruction(name string) (string, bool) {
	dirs := paths.AgentsRegistrySearchDirs()
	for _, dir := range dirs {
		if b, err := os.ReadFile(filepath.Join(dir, name, "instruction.md")); err == nil {
			return string(b), true
		}
	}
	for _, dir := range dirs {
		if b, err := os.ReadFile(filepath.Join(dir, "default.md")); err == nil {
			return string(b), false
		}
	}
	return "", false
}
