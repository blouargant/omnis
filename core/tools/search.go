package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type GrepIn struct {
	Pattern   string `json:"pattern"`
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}
type GrepOut struct {
	Matches string `json:"matches"`
}

// RunGrep shells out to /usr/bin/grep -nE for portability. Returns the
// matching lines with file:line: prefixes.
func RunGrep(ctx context.Context, in GrepIn) (string, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	args := []string{"-nE"}
	if in.Recursive || isDir(path) {
		args = append(args, "-r")
	}
	args = append(args, in.Pattern, path)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "grep", args...).CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	// grep exits 1 when no matches; that is not an error for us
	if err != nil && s == "" {
		return "(no matches)", nil
	}
	return truncate(s), nil
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

type GlobIn struct {
	Pattern string `json:"pattern" jsonschema:"glob pattern, e.g. **/*.go"`
}
type GlobOut struct {
	Files string `json:"files"`
}

// RunGlob expands a glob pattern and returns a sorted, newline-separated
// list of matches. Supports ** by walking when the pattern starts with **/.
func RunGlob(_ context.Context, in GlobIn) (string, error) {
	var matches []string
	if strings.Contains(in.Pattern, "**") {
		// crude ** support: split on '**/' and walk
		parts := strings.SplitN(in.Pattern, "**/", 2)
		root := "."
		if parts[0] != "" {
			root = strings.TrimSuffix(parts[0], "/")
		}
		suffix := parts[1]
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if ok, _ := filepath.Match(suffix, filepath.Base(p)); ok {
				matches = append(matches, p)
			}
			return nil
		})
	} else {
		m, err := filepath.Glob(in.Pattern)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		matches = m
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return truncate(strings.Join(matches, "\n")), nil
}
