package tools

import (
	"context"
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// New returns the full extended tool set as ADK tools. Failure to build any
// tool panics (these are static configurations that should always succeed).
func New() []tool.Tool {
	return []tool.Tool{
		mustTool("Bash",
			"Run a shell command via /bin/sh -c. Use for arbitrary shell operations. Times out after 120s by default. "+
				"Arguments: `command` (string, required) — the full shell command line to run. Do NOT use any other field name (e.g. `cmd`, `script`, `file_path`); calls with extra or missing properties are rejected.",
			func(ctx tool.Context, in BashIn) (BashOut, error) {
				in.Cwd = sessionCwd(ctx)
				out, _ := RunBash(context.Background(), in)
				return BashOut{Output: out}, nil
			}),
		mustTool("Read",
			"Read a file and return numbered lines. Use when you need to inspect file content or reference specific line numbers in a subsequent write. "+
				"Arguments: `file_path` (string, required), "+
				"`start_line` (int, optional, 1-based), `end_line` (int, optional). Returns up to 50,000 characters.",
			func(ctx tool.Context, in ReadIn) (ReadOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunRead(context.Background(), in)
				return ReadOut{Content: out}, nil
			}),
		mustTool("Write",
			"Write content to a file. Automatically snapshots the previous contents so you can revert. Creates parent directories if needed. "+
				"Arguments: `file_path` (string, required), `content` (string, required).",
			func(ctx tool.Context, in WriteIn) (WriteOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunWrite(context.Background(), in)
				return WriteOut{Result: out}, nil
			}),
		mustTool("Edit",
			"Perform a surgical in-place string replacement in a file. "+
				"Automatically snapshots the file so you can revert. "+
				"Fails if `old_string` is not found, or if it appears more than once and `replace_all` is false — "+
				"add more surrounding context to make `old_string` unique, or set `replace_all:true`. "+
				"Arguments: `file_path` (string, required), `old_string` (string, required), `new_string` (string, required), "+
				"`replace_all` (bool, optional, default false).",
			func(ctx tool.Context, in EditIn) (EditOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunEdit(context.Background(), in)
				return EditOut{Result: out}, nil
			}),
		mustTool("MultiEdit",
			"Apply many string replacements across one or more files in a single call — the efficient way to make a "+
				"multi-file change (a refactor, a rename-by-hand, a coordinated edit) instead of many separate Edit calls. "+
				"Each edit follows the same rules as Edit (old_string must be present, and unique unless replace_all is set); "+
				"edits within a file apply in order. The batch is atomic: if any edit in any file would fail, nothing is "+
				"written and the error names the offending file/edit. Snapshots each written file so you can revert. "+
				"Arguments: `files` (array, required) — each item is `{ file_path (string), edits (array of "+
				"{ old_string, new_string, replace_all? }) }`.",
			func(ctx tool.Context, in MultiEditIn) (MultiEditOut, error) {
				cwd := sessionCwd(ctx)
				for i := range in.Files {
					in.Files[i].Path = resolveAgainst(cwd, in.Files[i].Path)
				}
				out, _ := RunMultiEdit(context.Background(), in)
				return MultiEditOut{Result: out}, nil
			}),
		mustTool("Grep",
			"Search a regex pattern across files or a single file. Returns file:line matches. Prefer this over 'Bash grep'. "+
				"Arguments: `pattern` (string, required) — extended regex; `path` (string, optional) — file or directory to search, defaults to '.' (current directory); `recursive` (bool, optional) — recurse into subdirectories, default false. "+
				"Do NOT pass `file_path`, `start_line`, or `end_line` — those belong to the 'Read' tool.",
			func(ctx tool.Context, in GrepIn) (GrepOut, error) {
				in.Cwd = sessionCwd(ctx)
				out, _ := RunGrep(context.Background(), in)
				return GrepOut{Matches: out}, nil
			}),
		mustTool("Glob",
			"Find files matching a glob pattern, e.g. '**/*.go'. Returns sorted matches.",
			func(ctx tool.Context, in GlobIn) (GlobOut, error) {
				in.Cwd = sessionCwd(ctx)
				out, _ := RunGlob(context.Background(), in)
				return GlobOut{Files: out}, nil
			}),
		mustTool("revert",
			"Restore a file to its state before the last write call. Use when a write produced incorrect results. "+
				"Arguments: `file_path` (string, required).",
			func(ctx tool.Context, in RevertIn) (RevertOut, error) {
				in.Path = resolveAgainst(sessionCwd(ctx), in.Path)
				out, _ := RunRevert(context.Background(), in)
				return RevertOut{Result: out}, nil
			}),
		mustTool("mime",
			"Inspect a file's content (magic bytes) to detect its true MIME type and canonical extension, "+
				"then compare against the filename extension. Returns a formatted identity card. "+
				"Arguments: `file_path` (string, required).",
			func(ctx tool.Context, in MimeIn) (MimeOut, error) {
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
