package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/yoke/internal/paths"
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
