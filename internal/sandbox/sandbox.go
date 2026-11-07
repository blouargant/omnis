// Package sandbox runs a shell command that cannot modify the filesystem
// outside a few scratch locations. It backs read-only agents (agent.json
// `read_only: true`): their Bash runs here, so a reviewer told to "review, not
// patch" is unable to `sed -i` a file or `git stash` the author's work — the
// kernel refuses the write, whatever the model decides.
//
// Linux only, through Landlock (kernel ≥ 5.13, unprivileged, works inside
// containers). The omnis binary re-executes itself in helper mode
// (HelperArg); the helper restricts its own thread with a ruleset that
// handles every write right, grants them only beneath the writable
// directories, then execs the command, which inherits the restriction. Reads
// and execution are not restricted. Elsewhere Available() is false and the
// caller falls back to a stricter rule (see core/tools RunBash).
//
// Every binary that runs agent tools must call MaybeRunHelper first thing in
// main (and test binaries that exercise the sandbox, in TestMain).
package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HelperArg is the hidden first argument that turns the binary into the
// sandbox helper.
const HelperArg = "__omnis-readonly-exec"

// Wrap rewrites cmd so it runs under the read-only sandbox: the binary
// itself becomes the program, in helper mode, followed by the original
// program and arguments. Everything else on cmd (Dir, Env, SysProcAttr,
// Cancel) is kept. Call it only when Available() is true.
func Wrap(cmd *exec.Cmd, writable []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{self, HelperArg}
	for _, w := range writable {
		args = append(args, "--rw", w)
	}
	args = append(args, "--", cmd.Path)
	if len(cmd.Args) > 1 {
		args = append(args, cmd.Args[1:]...)
	}
	cmd.Path = self
	cmd.Args = args
	return nil
}

// DefaultWritable is where a read-only command may still write: the temp
// directories (probes, copies, test binaries), /dev (/dev/null, ttys) and the
// per-user build and package caches compilers and test runners fill. Nothing
// a project lives in.
func DefaultWritable() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		for _, have := range out {
			if have == p {
				return
			}
		}
		out = append(out, p)
	}
	add(os.TempDir())
	add("/tmp")
	add("/var/tmp")
	add("/dev")
	if d, err := os.UserCacheDir(); err == nil {
		add(d)
	}
	home, _ := os.UserHomeDir()
	if gp := os.Getenv("GOPATH"); gp != "" {
		for _, p := range filepath.SplitList(gp) {
			add(filepath.Join(p, "pkg", "mod"))
		}
	} else if home != "" {
		add(filepath.Join(home, "go", "pkg", "mod"))
	}
	if mc := os.Getenv("GOMODCACHE"); mc != "" {
		add(mc)
	}
	if home != "" {
		add(filepath.Join(home, ".npm"))
		add(filepath.Join(home, ".kube", "cache")) // kubectl discovery cache
	}
	return out
}

// parseHelperArgs splits the helper's arguments into writable paths and the
// command to exec.
func parseHelperArgs(args []string) (writable, command []string, ok bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rw":
			if i+1 >= len(args) {
				return nil, nil, false
			}
			writable = append(writable, args[i+1])
			i++
		case "--":
			command = args[i+1:]
			return writable, command, len(command) > 0
		default:
			return nil, nil, false
		}
	}
	return nil, nil, false
}

// fail is how the helper reports a sandbox it could not set up: a command
// that would run unrestricted is never executed.
func fail(msg string) {
	os.Stderr.WriteString("omnis: read-only sandbox: " + strings.TrimSpace(msg) + "\n")
	os.Exit(126)
}
