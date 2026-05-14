// Package skills wraps ADK's skilltoolset with a directory-based source
// (Phase 2 / s05). Each skill lives in `<dir>/<name>/SKILL.md` with YAML
// front matter describing what it does and a body of instructions.
package skills

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/skilltoolset"
	"google.golang.org/adk/tool/skilltoolset/skill"

	"github.com/blouargant/yoke/internal/fsutil"
)

// Toolset returns an ADK tool.Toolset reading skills from `dir`.
// `dir` is created if missing so demos still work.
func Toolset(ctx context.Context, dir string) (tool.Toolset, error) {
	if dir == "" {
		dir = "skills"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("skills dir: %w", err)
	}
	// Mandatory skills (review, agent-builder) are compiled into the binary and
	// always available. User-defined skills from the filesystem are layered on
	// top via a merged source. Builtin skills take priority (listed first) so a
	// duplicate name in the user directory is rejected at startup.
	fsSrc := skill.NewFileSystemSource(fsutil.NewSymlinkDirFS(dir))
	src := skill.NewMergedSource(builtinSource(), fsSrc)
	// NOTE: the default system instruction shipped by ADK v1.2 tells the
	// model to call `load_skill` with `skill_name="..."`, but the tool's
	// JSON schema actually requires `name`. That mismatch produces noisy
	// `tool_error` entries in the event log on every first attempt. We
	// override the instruction with one that matches the real schema.
	const skillInstruction = `You can use specialized 'skills' to help you with complex tasks. You MUST use the skill tools to interact with these skills.

Skills are folders of instructions and resources that extend your capabilities for specialized tasks. Each skill folder contains:
- **SKILL.md** (required): The main instruction file with skill metadata and detailed markdown instructions.
- **references/** (Optional): Additional documentation or examples for skill usage.
- **assets/** (Optional): Templates, scripts or other resources used by the skill.
- **scripts/** (Optional): Executable scripts that can be run via bash.

This is very important:

1. Use the ` + "`list_skills`" + ` tool to discover what skills are available.
2. If a skill seems relevant to the current user query, you MUST call the ` + "`load_skill`" + ` tool with the argument ` + "`name=\"<SKILL_NAME>\"`" + ` (the parameter is literally ` + "`name`" + `, not ` + "`skill_name`" + `) to read its full instructions before proceeding.
3. Once you have read the instructions, follow them exactly as documented before replying to the user. If the instructions list multiple steps, complete all of them in order.
4. The ` + "`load_skill_resource`" + ` tool is for viewing files within a skill's directory (e.g., ` + "`references/*`, `assets/*`, `scripts/*`" + `). Do NOT use other tools to access these files.
`
	ts, err := skilltoolset.New(ctx, skilltoolset.Config{
		Source:            src,
		SystemInstruction: skillInstruction,
	})
	if err != nil {
		return nil, err
	}
	return ts, nil
}
