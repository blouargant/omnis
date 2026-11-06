package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// snapshots holds the previous content of files written by RunWrite.
// A nil entry marks "file did not exist" so RunRevert can delete it.
var (
	snapMu    sync.Mutex
	snapshots = map[string]*string{}
)

type ReadIn struct {
	Path      string `json:"file_path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}
type ReadOut struct {
	Content string `json:"content"`
}

// Read output limits. The char cap sits below the coding-efficiency output
// shaper's budget (32k chars), so a long read is cut HERE, on a whole line and
// with a resume hint, instead of having its middle dropped by the shaper.
const (
	readMaxChars = 30_000
	readMaxLines = 2_000
)

// RunRead returns numbered lines of a file, optionally bounded by a
// [start_line, end_line] inclusive range (1-indexed). A read that hits the
// line or character cap stops on a whole line and ends with the start_line to
// continue from.
func RunRead(_ context.Context, in ReadIn) (string, error) {
	data, err := os.ReadFile(in.Path)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", in.Path, err), nil
	}
	lines := strings.Split(string(data), "\n")
	// trailing newline produces a trailing empty element; drop it for sane numbering
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := in.StartLine
	if start <= 0 {
		start = 1
	}
	end := in.EndLine
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "(empty range)", nil
	}
	var b strings.Builder
	shown := start - 1
	for i := start - 1; i < end; i++ {
		line := fmt.Sprintf("%4d\t%s\n", i+1, lines[i])
		if i > start-1 && (b.Len()+len(line) > readMaxChars || i-(start-1) >= readMaxLines) {
			break
		}
		if b.Len()+len(line) > readMaxChars { // a single line over the cap
			line = truncateAt(line, readMaxChars) + "\n"
		}
		b.WriteString(line)
		shown = i + 1
	}
	if b.Len() == 0 {
		return "(empty file)", nil
	}
	if shown < end {
		fmt.Fprintf(&b, "\n[Showing lines %d-%d of %d. Use start_line=%d to continue.]", start, shown, len(lines), shown+1)
	}
	return b.String(), nil
}

type WriteIn struct {
	Path    string `json:"file_path"`
	Content string `json:"content"`
}
type WriteOut struct {
	Result string `json:"result"`
	// Created is true when the write made a new file (it did not exist before);
	// the web UI lists created files in the environment panel's Résultats card.
	Created bool `json:"created,omitempty"`
}

// RunWrite writes content to a file, snapshotting the previous contents (if
// any) so RunRevert can restore them.
func RunWrite(_ context.Context, in WriteIn) (string, error) {
	defer LockFiles(in.Path)()
	return writeSnapshotted(in.Path, in.Content), nil
}

// writeSnapshotted is RunWrite's body; the caller holds the file's lock.
func writeSnapshotted(path, content string) string {
	snapMu.Lock()
	if data, err := os.ReadFile(path); err == nil {
		s := string(data)
		snapshots[path] = &s
	} else if os.IsNotExist(err) {
		snapshots[path] = nil // marker: file did not exist
	}
	snapMu.Unlock()
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("Error writing %s: %v", path, err)
	}
	return fmt.Sprintf("wrote %s (%d bytes; snapshot saved - call revert to undo)", path, len(content))
}

type EditIn struct {
	Path       string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}
type EditOut struct {
	Result string `json:"result"`
}

// normalizedMatchNote is appended to a result whose old_string only matched
// after normalization, so the model knows its text differed from the file.
const normalizedMatchNote = " — matched after normalizing line endings, trailing whitespace and typographic quotes/dashes"

// RunEdit replaces the first (or all) occurrence of OldString with NewString
// in the named file, snapshotting the previous content so RunRevert can undo.
// Returns an error message if OldString is not found or (when ReplaceAll is
// false) appears more than once — the caller must be explicit. An exact match
// is tried first, then a normalized one (see findEditMatches). The whole
// read-modify-write runs under the file's lock (see LockFiles).
func RunEdit(_ context.Context, in EditIn) (string, error) {
	if in.OldString == "" {
		return fmt.Sprintf("Error: empty old_string for %s — use Write to create or replace a whole file", in.Path), nil
	}
	defer LockFiles(in.Path)()
	data, err := os.ReadFile(in.Path)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", in.Path, err), nil
	}
	content := string(data)
	ranges, normalized := findEditMatches(content, in.OldString)
	count := len(ranges)
	if count == 0 {
		return fmt.Sprintf("Error: old_string not found in %s — Read the file again and copy the exact text", in.Path), nil
	}
	if !in.ReplaceAll && count > 1 {
		return fmt.Sprintf("Error: old_string appears %d times in %s — set replace_all:true or provide more context to make it unique", count, in.Path), nil
	}

	snapMu.Lock()
	s := content
	snapshots[in.Path] = &s
	snapMu.Unlock()

	updated := applyEditMatches(content, ranges, in.NewString, normalized)
	if err := os.WriteFile(in.Path, []byte(updated), 0o644); err != nil {
		return fmt.Sprintf("Error writing %s: %v", in.Path, err), nil
	}
	note := ""
	if normalized {
		note = normalizedMatchNote
	}
	return fmt.Sprintf("edited %s (%d replacement(s); snapshot saved - call revert to undo)%s", in.Path, count, note), nil
}

// EditOp is one string replacement within a file, as used by MultiEdit.
type EditOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// MultiEditFile groups the ordered edits to apply to one file.
type MultiEditFile struct {
	Path  string   `json:"file_path"`
	Edits []EditOp `json:"edits"`
}

// MultiEditIn is a batch of per-file edits applied atomically: every edit in
// every file is validated first, and only if all pass are any files written.
type MultiEditIn struct {
	Files []MultiEditFile `json:"files"`
}
type MultiEditOut struct {
	Result string `json:"result"`
}

// RunMultiEdit applies a batch of exact-string replacements across one or more
// files in a single call. Edits within a file apply in order (a later edit sees
// earlier ones' result); each edit follows RunEdit's rules — old_string must be
// present, and appear exactly once unless replace_all is set. The whole batch is
// atomic on validation: if ANY edit in ANY file would fail, nothing is written
// and the error names the offending file/edit. On success each file is written
// through the snapshotting Write path, so the batch is revertible per file.
func RunMultiEdit(_ context.Context, in MultiEditIn) (string, error) {
	if len(in.Files) == 0 {
		return "Error: no files supplied to MultiEdit", nil
	}
	paths := make([]string, 0, len(in.Files))
	for _, f := range in.Files {
		paths = append(paths, f.Path)
	}
	defer LockFiles(paths...)()
	// Phase 1 — validate + compute every file's final content, no writes.
	type plan struct {
		path    string
		content string
		count   int
	}
	seen := map[string]bool{}
	anyNormalized := false
	plans := make([]plan, 0, len(in.Files))
	for fi, f := range in.Files {
		if f.Path == "" {
			return fmt.Sprintf("Error: file %d has an empty file_path", fi+1), nil
		}
		if seen[f.Path] {
			return fmt.Sprintf("Error: %s appears more than once — merge its edits into a single files[] entry", f.Path), nil
		}
		seen[f.Path] = true
		if len(f.Edits) == 0 {
			return fmt.Sprintf("Error: %s has no edits", f.Path), nil
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return fmt.Sprintf("Error reading %s: %v", f.Path, err), nil
		}
		content := string(data)
		applied := 0
		for ei, e := range f.Edits {
			if e.OldString == "" {
				return fmt.Sprintf("Error: %s edit %d has an empty old_string", f.Path, ei+1), nil
			}
			ranges, normalized := findEditMatches(content, e.OldString)
			count := len(ranges)
			if count == 0 {
				return fmt.Sprintf("Error: old_string of %s edit %d not found", f.Path, ei+1), nil
			}
			if !e.ReplaceAll && count > 1 {
				return fmt.Sprintf("Error: old_string of %s edit %d appears %d times — set replace_all:true or add context to make it unique", f.Path, ei+1, count), nil
			}
			content = applyEditMatches(content, ranges, e.NewString, normalized)
			applied += count
			anyNormalized = anyNormalized || normalized
		}
		plans = append(plans, plan{path: f.Path, content: content, count: applied})
	}
	// Phase 2 — commit. Each write snapshots the prior content (revertible).
	total := 0
	for _, p := range plans {
		if out := writeSnapshotted(p.path, p.content); strings.HasPrefix(out, "Error") {
			return out, nil
		}
		total += p.count
	}
	note := ""
	if anyNormalized {
		note = normalizedMatchNote
	}
	return fmt.Sprintf("MultiEdit applied %d replacement(s) across %d file(s); snapshots saved - call revert per file to undo.%s", total, len(plans), note), nil
}

type RevertIn struct {
	Path string `json:"file_path"`
}
type RevertOut struct {
	Result string `json:"result"`
}

// RunRevert restores a file to its pre-write state.
func RunRevert(_ context.Context, in RevertIn) (string, error) {
	defer LockFiles(in.Path)()
	snapMu.Lock()
	snap, ok := snapshots[in.Path]
	if ok {
		delete(snapshots, in.Path)
	}
	snapMu.Unlock()
	if !ok {
		return fmt.Sprintf("Error: no snapshot for %s", in.Path), nil
	}
	if snap == nil {
		// file did not exist before — delete it
		if err := os.Remove(in.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Sprintf("Error removing %s: %v", in.Path, err), nil
		}
		return fmt.Sprintf("reverted: removed %s (was newly created)", in.Path), nil
	}
	if err := os.WriteFile(in.Path, []byte(*snap), 0o644); err != nil {
		return fmt.Sprintf("Error reverting %s: %v", in.Path, err), nil
	}
	return fmt.Sprintf("reverted %s (%d bytes restored)", in.Path, len(*snap)), nil
}
