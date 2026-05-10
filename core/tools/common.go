// Package tools implements the article's "extended tool arsenal":
// bash, read, write, grep, glob, revert. Each tool is exposed as an ADK
// tool.Tool via functiontool.New, so it plugs straight into llmagent.New.
//
// Implementation notes:
//   - run_read returns numbered lines so the model can reference them in a
//     subsequent run_write call (article phase 4 / s14).
//   - run_write snapshots the previous file contents into an in-memory map;
//     run_revert restores from that snapshot. Snapshots are per-process.
//   - Errors are returned as strings in the result, never raised, matching
//     the article's harness contract ("handlers never raise to the loop").
package tools

import "fmt"

// MaxToolOutput caps the size of any tool string result, mirroring the
// article's 50_000 character truncation.
const MaxToolOutput = 50_000

// truncate caps s at MaxToolOutput.
func truncate(s string) string {
	if len(s) > MaxToolOutput {
		return s[:MaxToolOutput] + fmt.Sprintf("\n... (truncated at %d bytes)", MaxToolOutput)
	}
	return s
}
