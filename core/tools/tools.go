package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

// New returns the full extended tool set as ADK tools. Failure to build any
// tool panics (these are static configurations that should always succeed).
func New() []tool.Tool {
	return []tool.Tool{
		mustTool("Bash",
			"Run a shell command (/bin/sh -c) in the working directory. Times out after 120 s unless `timeout` (seconds) is set. "+
				"Output over 50 KB keeps its end and saves the full output to a file it names: Read or Grep that file instead of re-running the command.",
			func(ctx adk.ToolContext, in BashIn) (BashOut, error) {
				in.Cwd = sessionCwd(ctx)
				in.Env = ShellEnvFrom(ctx)
				out, _ := RunBash(context.Background(), in)
				return BashOut{Output: out}, nil
			}),
		mustTool("Read",
			"Read a file as numbered lines; `start_line`/`end_line` (1-based) select a range. "+
				"At most 2,000 lines / 30,000 characters per call: a longer file is cut on a whole line and ends with the `start_line` to continue from.",
			func(ctx adk.ToolContext, in ReadIn) (ReadOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunRead(context.Background(), in)
				return ReadOut{Content: out}, nil
			}),
		mustTool("Write",
			"Create or overwrite a file, creating parent directories. Use it for new files and full rewrites, Edit for changes. "+
				"The previous content is kept for revert.",
			func(ctx adk.ToolContext, in WriteIn) (WriteOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				_, statErr := os.Stat(in.Path)
				out, _ := RunWrite(context.Background(), in)
				created := os.IsNotExist(statErr) && !strings.HasPrefix(out, "Error")
				return WriteOut{Result: out, Created: created}, nil
			}),
		mustTool("Edit",
			"Replace `old_string` with `new_string` in a file. `old_string` must occur exactly once unless `replace_all` is true: "+
				"include surrounding lines to make it unique. The previous content is kept for revert.",
			func(ctx adk.ToolContext, in EditIn) (EditOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunEdit(context.Background(), in)
				return EditOut{Result: out}, nil
			}),
		mustTool("MultiEdit",
			"Several Edit-style replacements across one or more files in one atomic call: "+
				"`files` = [{file_path, edits: [{old_string, new_string, replace_all?}]}]. "+
				"Edits apply in order; if any fails, nothing is written. The previous contents are kept for revert.",
			func(ctx adk.ToolContext, in MultiEditIn) (MultiEditOut, error) {
				cwd := sessionCwd(ctx)
				for i := range in.Files {
					in.Files[i].Path = resolveAgainst(cwd, in.Files[i].Path)
				}
				out, _ := RunMultiEdit(context.Background(), in)
				return MultiEditOut{Result: out}, nil
			}),
		mustTool("Grep",
			"Search file contents for a regex (or `literal`) pattern; directories are searched recursively. "+
				"In a git repository, .gitignore is respected and binary files are skipped. "+
				"Stops at 100 matches (`limit`, or narrow with `path`/`glob`); lines are cut at 500 characters.",
			func(ctx adk.ToolContext, in GrepIn) (GrepOut, error) {
				in.Cwd = sessionCwd(ctx)
				out, _ := RunGrep(context.Background(), in)
				return GrepOut{Matches: out}, nil
			}),
		mustTool("Glob",
			"Find files matching a glob pattern, e.g. '**/*.go'. Returns sorted paths.",
			func(ctx adk.ToolContext, in GlobIn) (GlobOut, error) {
				in.Cwd = sessionCwd(ctx)
				out, _ := RunGlob(context.Background(), in)
				return GlobOut{Files: out}, nil
			}),
		mustTool("revert",
			"Restore a file to its content before the last Write, Edit or MultiEdit.",
			func(ctx adk.ToolContext, in RevertIn) (RevertOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunRevert(context.Background(), in)
				return RevertOut{Result: out}, nil
			}),
		mustTool("mime",
			"Detect a file's real MIME type and extension from its content, and compare them with its name.",
			func(ctx adk.ToolContext, in MimeIn) (MimeOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunMime(context.Background(), in)
				return MimeOut{Card: out}, nil
			}),
	}
}

func mustTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build tool %s: %w", name, err))
	}
	return t
}
