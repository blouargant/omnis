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
	src := skill.NewFileSystemSource(os.DirFS(dir))
	ts, err := skilltoolset.New(ctx, skilltoolset.Config{Source: src})
	if err != nil {
		return nil, err
	}
	return ts, nil
}
