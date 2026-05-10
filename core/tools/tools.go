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
		mustTool("bash",
			"Run a shell command via /bin/sh -c. Use for arbitrary shell operations. Times out after 120s by default. "+
				"Arguments: `command` (string, required) — the full shell command line to run. Do NOT use any other field name (e.g. `cmd`, `script`, `file_path`); calls with extra or missing properties are rejected.",
			func(_ tool.Context, in BashIn) (BashOut, error) {
				out, _ := RunBash(context.Background(), in)
				return BashOut{Output: out}, nil
			}),
		mustTool("read",
			"Read a file and return numbered lines. Use when you need to inspect file content or reference specific line numbers in a subsequent write. "+
				"Arguments: `file_path` (string, required), "+
				"`start_line` (int, optional, 1-based), `end_line` (int, optional). Returns up to 50,000 characters.",
			func(_ tool.Context, in ReadIn) (ReadOut, error) {
				out, _ := RunRead(context.Background(), in)
				return ReadOut{Content: out}, nil
			}),
		mustTool("write",
			"Write content to a file. Automatically snapshots the previous contents so you can revert. Creates parent directories if needed. "+
				"Arguments: `file_path` (string, required), `content` (string, required).",
			func(_ tool.Context, in WriteIn) (WriteOut, error) {
				out, _ := RunWrite(context.Background(), in)
				return WriteOut{Result: out}, nil
			}),
		mustTool("grep",
			"Search a regex pattern across files or a single file. Returns file:line matches. Prefer this over 'bash grep'. "+
				"Arguments: `pattern` (string, required) — extended regex; `path` (string, optional) — file or directory to search, defaults to '.' (current directory); `recursive` (bool, optional) — recurse into subdirectories, default false. "+
				"Do NOT pass `file_path`, `start_line`, or `end_line` — those belong to the 'read' tool.",
			func(_ tool.Context, in GrepIn) (GrepOut, error) {
				out, _ := RunGrep(context.Background(), in)
				return GrepOut{Matches: out}, nil
			}),
		mustTool("glob",
			"Find files matching a glob pattern, e.g. '**/*.go'. Returns sorted matches.",
			func(_ tool.Context, in GlobIn) (GlobOut, error) {
				out, _ := RunGlob(context.Background(), in)
				return GlobOut{Files: out}, nil
			}),
		mustTool("revert",
			"Restore a file to its state before the last write call. Use when a write produced incorrect results. "+
				"Arguments: `file_path` (string, required).",
			func(_ tool.Context, in RevertIn) (RevertOut, error) {
				out, _ := RunRevert(context.Background(), in)
				return RevertOut{Result: out}, nil
			}),
		mustTool("mime",
			"Inspect a file's content (magic bytes) to detect its true MIME type and canonical extension, "+
				"then compare against the filename extension. Returns a formatted identity card. "+
				"Arguments: `file_path` (string, required).",
			func(_ tool.Context, in MimeIn) (MimeOut, error) {
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
