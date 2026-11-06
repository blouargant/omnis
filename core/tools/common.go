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

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// MaxToolOutput caps the size of any tool string result, mirroring the
// article's 50_000 character truncation.
const MaxToolOutput = 50_000

// truncate caps s at MaxToolOutput, keeping the head.
func truncate(s string) string {
	if len(s) > MaxToolOutput {
		return truncateAt(s, MaxToolOutput) + fmt.Sprintf("\n... (truncated at %d bytes)", MaxToolOutput)
	}
	return s
}

// truncateAt returns the longest prefix of s that fits in n bytes without
// splitting a UTF-8 sequence.
func truncateAt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// capCommandOutput caps a command's output at MaxToolOutput keeping the TAIL —
// build and test failures are reported at the end — and writes the whole
// output to a temp file the agent can Read or Grep instead of re-running the
// command. The kept tail starts on a whole line. The notice goes first so a
// later head/tail shaper keeps it.
func capCommandOutput(s string) string {
	if len(s) <= MaxToolOutput {
		return s
	}
	const noticeRoom = 400
	cut := len(s) - (MaxToolOutput - noticeRoom)
	if nl := strings.IndexByte(s[cut:], '\n'); nl >= 0 && nl < 4096 {
		cut += nl + 1
	}
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	tail := s[cut:]
	total := strings.Count(s, "\n") + 1
	from := total - strings.Count(tail, "\n")
	where := "the full output could not be saved"
	if f, err := os.CreateTemp("", "omnis-bash-*.log"); err == nil {
		_, werr := f.WriteString(s)
		cerr := f.Close()
		if werr == nil && cerr == nil {
			where = "Full output: " + f.Name() + " (Read it with start_line, or Grep it, instead of re-running the command)"
		}
	}
	return fmt.Sprintf("[Output truncated: showing lines %d-%d of %d. %s]\n%s", from, total, total, where, tail)
}
