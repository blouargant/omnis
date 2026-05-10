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

// RunRead returns numbered lines of a file, optionally bounded by a
// [start_line, end_line] inclusive range (1-indexed).
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
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%4d\t%s\n", i+1, lines[i])
	}
	if b.Len() == 0 {
		return "(empty file)", nil
	}
	return truncate(b.String()), nil
}

type WriteIn struct {
	Path    string `json:"file_path"`
	Content string `json:"content"`
}
type WriteOut struct {
	Result string `json:"result"`
}

// RunWrite writes content to a file, snapshotting the previous contents (if
// any) so RunRevert can restore them.
func RunWrite(_ context.Context, in WriteIn) (string, error) {
	snapMu.Lock()
	if data, err := os.ReadFile(in.Path); err == nil {
		s := string(data)
		snapshots[in.Path] = &s
	} else if os.IsNotExist(err) {
		snapshots[in.Path] = nil // marker: file did not exist
	}
	snapMu.Unlock()
	if dir := filepath.Dir(in.Path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil {
		return fmt.Sprintf("Error writing %s: %v", in.Path, err), nil
	}
	return fmt.Sprintf("wrote %s (%d bytes; snapshot saved - call revert to undo)", in.Path, len(in.Content)), nil
}

type RevertIn struct {
	Path string `json:"file_path"`
}
type RevertOut struct {
	Result string `json:"result"`
}

// RunRevert restores a file to its pre-write state.
func RunRevert(_ context.Context, in RevertIn) (string, error) {
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
