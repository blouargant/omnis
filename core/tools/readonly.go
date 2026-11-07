package tools

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/permissions"
	"github.com/blouargant/omnis/internal/sandbox"
)

// Read-only agents (agent.json `read_only: true`). Their shell commands and
// file writes may change nothing outside the scratch directories; the rule is
// enforced, not requested: the Bash command runs under the Landlock sandbox,
// where the kernel refuses every write elsewhere.

// ReadOnlyWritable is where a read-only agent may still write.
func ReadOnlyWritable() []string { return sandbox.DefaultWritable() }

// confineReadOnly puts cmd under the read-only sandbox. Where the sandbox is
// unavailable (no Landlock: macOS, Windows, an old kernel), only a command
// the permission layer recognises as read-only may run; the returned message
// refuses anything else. "" means cmd may run.
func confineReadOnly(cmd *exec.Cmd, command string) string {
	if sandbox.Available() {
		if err := sandbox.Wrap(cmd, ReadOnlyWritable()); err != nil {
			return "Error: read-only agent: cannot start the sandbox: " + err.Error()
		}
		return ""
	}
	if permissions.AllReadOnly(command) {
		return ""
	}
	return "Error: this agent is read-only and the filesystem sandbox is not available on this host, " +
		"so only read-only commands (ls, cat, grep, git diff/log/status, …) may run. Refused: " + command
}

// NewReadOnlyBash is the Bash tool of a read-only agent.
func NewReadOnlyBash() tool.Tool {
	return mustTool("Bash",
		"Run a shell command via /bin/sh -c. Times out after 120s by default (pass `timeout` for long builds). "+
			"You are READ-ONLY: the command cannot create, change or delete any file except under "+
			strings.Join(scratchRoots(), ", ")+" — a write anywhere else fails with \"Permission denied\" "+
			"(git stash/checkout/commit included). To run a probe or a modified copy, work in a temp directory "+
			"(e.g. `cp -a . /tmp/review-copy`). Output over 50,000 bytes keeps its END and the full output is saved to a file. "+
			"Arguments: `command` (string, required) — the full shell command line to run.",
		func(ctx adk.ToolContext, in BashIn) (BashOut, error) {
			in.Cwd = sessionCwd(ctx)
			in.Env = ShellEnvFrom(ctx)
			in.ReadOnly = true
			out, _ := RunBash(context.Background(), in)
			return BashOut{Output: out}, nil
		})
}

// scratchRoots is the short list named to the model.
func scratchRoots() []string {
	var out []string
	for _, p := range ReadOnlyWritable() {
		if p == "/dev" || strings.Contains(p, string(filepath.Separator)+".") || strings.Contains(p, "pkg/mod") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// UnderWritable reports whether path (absolute, or relative to cwd) lies
// under one of the read-only agent's writable roots, symlinks resolved so a
// link in /tmp pointing into a project does not count.
func UnderWritable(cwd, path string) bool {
	p := resolveAgainst(cwd, path)
	if !filepath.IsAbs(p) {
		var err error
		if p, err = filepath.Abs(p); err != nil {
			return false
		}
	}
	p = resolveExisting(filepath.Clean(p))
	for _, root := range ReadOnlyWritable() {
		r := resolveExisting(filepath.Clean(root))
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveExisting resolves symlinks on the longest existing prefix of p.
func resolveExisting(p string) string {
	rest := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
