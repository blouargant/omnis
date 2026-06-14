package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/yoke/internal/filter"
)

// alwaysBlock contains substrings that RunBash refuses outright. The
// permissions package implements the full three-tier YAML governance; this
// is the hard floor that always applies, even when permissions are disabled.
var alwaysBlock = []string{"rm -rf /", ":(){:|:&};:", "mkfs"}

// SafetyFloorBlock reports whether command trips the hard safety floor,
// returning the offending substring. It is the single source of truth shared
// by RunBash, RunBashInteractive, and any other surface that executes a shell
// command (e.g. the bash_background queue) so the floor can never be bypassed
// by routing around RunBash.
func SafetyFloorBlock(command string) (string, bool) {
	for _, b := range alwaysBlock {
		if strings.Contains(command, b) {
			return b, true
		}
	}
	return "", false
}

// cwdSentinel prefixes the line RunBashInteractive appends to capture the
// shell's working directory after the command ran (so an embedded `cd`
// persists across the interactive "!" shell-escape). It is unlikely to
// collide with real command output; the parser scans from the end and takes
// the last match. See wrapCaptureCwd in bash_unix.go / bash_windows.go.
const cwdSentinel = "__YOKE_CWD__:"

var (
	bashFilterMu       sync.RWMutex
	bashFilterEnabled  bool
	bashFilterRegistry *filter.Registry

	bashDefaultTimeout   time.Duration = 120 * time.Second
	bashDefaultTimeoutMu sync.RWMutex
)

// SetBashDefaultTimeout sets the default timeout applied when RunBash receives
// a zero or negative Timeout value.
func SetBashDefaultTimeout(d time.Duration) {
	if d <= 0 {
		d = 120 * time.Second
	}
	bashDefaultTimeoutMu.Lock()
	bashDefaultTimeout = d
	bashDefaultTimeoutMu.Unlock()
}

// BashOutputFilterConfig controls optional output filtering for RunBash.
type BashOutputFilterConfig struct {
	Enabled    bool
	FiltersDir string
}

// ConfigureBashOutputFilter loads and enables/disables bash output filtering.
func ConfigureBashOutputFilter(cfg BashOutputFilterConfig) error {
	bashFilterMu.Lock()
	defer bashFilterMu.Unlock()

	bashFilterEnabled = false
	bashFilterRegistry = nil

	if !cfg.Enabled {
		return nil
	}
	rulesDir := strings.TrimSpace(cfg.FiltersDir)
	if rulesDir == "" {
		rulesDir = filter.DefaultRulesDir()
	}
	filters, err := filter.LoadDir(rulesDir)
	if err != nil {
		return fmt.Errorf("bash output filter: load rules from %q: %w", rulesDir, err)
	}
	bashFilterRegistry = filter.NewRegistry(filters)
	bashFilterEnabled = true
	return nil
}

func maybeApplyBashOutputFilter(command, output string) string {
	bashFilterMu.RLock()
	enabled := bashFilterEnabled
	reg := bashFilterRegistry
	bashFilterMu.RUnlock()

	if !enabled || reg == nil || strings.TrimSpace(output) == "" {
		return output
	}
	filtered, applied, err := filter.ApplyForCommand(reg, command, output)
	if err != nil || !applied {
		return output
	}
	return strings.TrimRight(filtered, "\n")
}

func maybeInjectBashFilterArgs(command string) string {
	bashFilterMu.RLock()
	enabled := bashFilterEnabled
	reg := bashFilterRegistry
	bashFilterMu.RUnlock()

	if !enabled || reg == nil || strings.TrimSpace(command) == "" {
		return command
	}
	// Keep shell behavior unchanged for complex expressions.
	if strings.ContainsAny(command, "|;&<>()`$") {
		return command
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return command
	}

	binary := parts[0]
	allArgs := []string{}
	if len(parts) > 1 {
		allArgs = parts[1:]
	}

	subcommand := ""
	args := allArgs
	if len(allArgs) > 0 {
		subcommand = allArgs[0]
		args = allArgs[1:]
	}

	f := reg.Match(filepath.Base(binary), subcommand, args)
	if f == nil || f.Inject == nil {
		return command
	}

	injectedArgs, changed := reg.ShouldInject(f, allArgs)
	if !changed {
		return command
	}

	tokens := append([]string{binary}, injectedArgs...)
	quoted := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		quoted = append(quoted, shellQuote(tok))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '+' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

type BashIn struct {
	Command string `json:"command" jsonschema:"required,the exact shell command line to execute (this field is required and is the only accepted argument besides the optional 'timeout')"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"timeout in seconds, default 120"`
	// Cwd is the directory to run the command in. It is set internally by the
	// tool handler from the session's working directory and is excluded from the
	// LLM-facing schema (json:"-"); empty means the process working directory.
	Cwd string `json:"-"`
}
type BashOut struct {
	Output string `json:"output"`
}

// RunBash executes a shell command via /bin/sh -c, with a default 120s
// timeout. Output is truncated at MaxToolOutput.
func RunBash(ctx context.Context, in BashIn) (string, error) {
	if b, blocked := SafetyFloorBlock(in.Command); blocked {
		return fmt.Sprintf("Error: command blocked by safety floor (%q)", b), nil
	}
	timeout := time.Duration(in.Timeout) * time.Second
	if timeout <= 0 {
		bashDefaultTimeoutMu.RLock()
		timeout = bashDefaultTimeout
		bashDefaultTimeoutMu.RUnlock()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execCommand := maybeInjectBashFilterArgs(in.Command)
	// newShellCommand wires up the platform's shell plus a Cancel hook that
	// kills the whole process tree when the context deadline fires, so
	// orphaned children can't keep the stdout/stderr pipes open and hang
	// CombinedOutput past the timeout. See bash_unix.go / bash_windows.go.
	cmd := newShellCommand(cctx, execCommand)
	if in.Cwd != "" {
		cmd.Dir = in.Cwd
	}
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("Error: command timed out after %s\n%s", timeout, truncate(s)), nil
	}
	if err != nil && s == "" {
		return fmt.Sprintf("Error: %v", err), nil
	}
	s = maybeApplyBashOutputFilter(in.Command, s)
	if s == "" {
		return "(no output)", nil
	}
	return truncate(s), nil
}

// RunBashInteractive runs command through the platform shell with cwd as the
// working directory and reports the working directory after the command ran,
// so an embedded `cd` persists to the caller's next invocation. It shares
// RunBash's safety floor, timeout, output filtering, and truncation.
//
// This backs the interactive "!" shell-escape in the TUI and web UI. By
// design it bypasses the agent permission layer (the user typed the command
// explicitly), but the hard safety floor still applies. timeoutSec ≤ 0 uses
// the configured default. On any error newCwd falls back to the input cwd.
func RunBashInteractive(ctx context.Context, command, cwd string, timeoutSec int) (output, newCwd string, err error) {
	if b, blocked := SafetyFloorBlock(command); blocked {
		return fmt.Sprintf("Error: command blocked by safety floor (%q)", b), cwd, nil
	}
	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		bashDefaultTimeoutMu.RLock()
		timeout = bashDefaultTimeout
		bashDefaultTimeoutMu.RUnlock()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	execCommand := maybeInjectBashFilterArgs(command)
	cmd := newShellCommand(cctx, wrapCaptureCwd(execCommand))
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.WaitDelay = 5 * time.Second
	out, runErr := cmd.CombinedOutput()
	s, resultCwd := extractCapturedCwd(string(out), cwd)
	s = strings.TrimRight(s, "\n")
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("Error: command timed out after %s\n%s", timeout, truncate(s)), resultCwd, nil
	}
	if runErr != nil && s == "" {
		return fmt.Sprintf("Error: %v", runErr), resultCwd, nil
	}
	s = maybeApplyBashOutputFilter(command, s)
	if s == "" {
		s = "(no output)"
	}
	return truncate(s), resultCwd, nil
}

// CapturedRun is the result of RunShellCaptured: stdout and stderr separated,
// the process exit code, and the timed-out / safety-floor-blocked flags.
type CapturedRun struct {
	Stdout   string
	Stderr   string
	ExitCode int // process exit code; -1 when it could not start or was blocked
	TimedOut bool
	Blocked  bool // refused by the safety floor (Stderr carries the reason)
}

// RunShellCaptured executes command through the platform shell (the same
// process-group-isolated shell + kill-on-timeout as RunBash) with cwd as the
// working directory, feeding stdin to the process and capturing stdout and
// stderr separately along with the exit code. Unlike RunBash/RunBashInteractive
// it does not combine the streams, apply the output filter, or truncate — its
// caller needs the raw stdout/stderr/exit-code triple to implement a control
// protocol (the hooks engine, which speaks Claude Code's hook output schema).
//
// It shares RunBash's hard safety floor: a command tripping it returns with
// Blocked=true and ExitCode=-1 without executing. timeout <= 0 uses the
// configured default. Like the "!" shell-escape it bypasses the agent
// permission layer (hook commands are user-authored config), but the safety
// floor still applies.
func RunShellCaptured(ctx context.Context, command, cwd string, stdin []byte, timeout time.Duration) CapturedRun {
	if b, blocked := SafetyFloorBlock(command); blocked {
		return CapturedRun{Stderr: fmt.Sprintf("command blocked by safety floor (%q)", b), ExitCode: -1, Blocked: true}
	}
	if timeout <= 0 {
		bashDefaultTimeoutMu.RLock()
		timeout = bashDefaultTimeout
		bashDefaultTimeoutMu.RUnlock()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := newShellCommand(cctx, command)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.WaitDelay = 5 * time.Second

	runErr := cmd.Run()
	res := CapturedRun{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		TimedOut: errors.Is(cctx.Err(), context.DeadlineExceeded),
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			if res.Stderr == "" {
				res.Stderr = runErr.Error()
			}
		}
	}
	return res
}

// extractCapturedCwd removes the cwdSentinel line emitted by wrapCaptureCwd
// from out and returns the cleaned output plus the captured directory. The
// scan runs from the end so any literal sentinel in real output earlier loses
// to the appended one. fallback is returned when no sentinel is present or it
// carries an empty path.
func extractCapturedCwd(out, fallback string) (string, string) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], cwdSentinel) {
			continue
		}
		dir := strings.TrimRight(strings.TrimPrefix(lines[i], cwdSentinel), "\r")
		cleaned := strings.Join(append(lines[:i], lines[i+1:]...), "\n")
		if strings.TrimSpace(dir) == "" {
			dir = fallback
		}
		return cleaned, dir
	}
	return out, fallback
}
