package agentmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// initPrompt is the shared "/init" instruction sent to the leader as a normal
// user turn on every surface (web UI, TUI, CLI). It asks the agent to inspect
// the repository and write a starter AGENT.md.
const initPrompt = `Analyze this repository and create an ` + FileName + ` file at the project root (the repository's top level) that documents how an AI coding agent should work in this codebase.

Use your file-system tools to explore: read the README, build/config files, and a representative sample of the source tree. Then write ` + FileName + ` (overwrite it if it already exists, after reading it first to preserve anything still useful). Structure it with concise Markdown sections such as:

- A one-line description of what the project is.
- **Commands**: how to build, test, run, and lint (copy the exact commands).
- **Architecture**: the high-level structure and the key packages/directories and their roles.
- **Conventions**: naming, formatting, and any project-specific patterns a contributor must follow.
- **Gotchas**: non-obvious rules, precedence chains, or pitfalls.

Keep it factual and derived from the actual code — do not invent commands or structure. When done, briefly summarize what you wrote.`

// InitPrompt returns the shared "/init" bootstrap instruction.
func InitPrompt() string { return initPrompt }

// AppendMemory appends a one-line memory (the "#" shortcut) to the project
// AGENT.md resolved from cwd, creating the file with a heading when missing.
// The target is the repo root's AGENT.md (or cwd's when not in a repo). It
// returns the absolute path written. A leading "#" and surrounding whitespace
// on line are stripped; an empty line after trimming is an error.
func AppendMemory(cwd, line string) (string, error) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
	if line == "" {
		return "", fmt.Errorf("empty memory")
	}
	if strings.TrimSpace(cwd) == "" {
		c, err := os.Getwd()
		if err != nil {
			return "", err
		}
		cwd = c
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	target := filepath.Join(repoRoot(abs), FileName)

	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var b strings.Builder
	if len(existing) == 0 {
		b.WriteString("# " + FileName + "\n\nProject memory for AI agents.\n\n## Notes\n")
	} else {
		b.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteString("\n")
		}
		// Ensure a Notes section exists; append one when absent.
		if !strings.Contains(string(existing), "## Notes") {
			b.WriteString("\n## Notes\n")
		}
	}
	b.WriteString("- " + line + "\n")
	if err := os.WriteFile(target, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return target, nil
}
