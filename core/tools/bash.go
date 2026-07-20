package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/omnis/internal/filter"
)

// alwaysBlock contains representative catastrophic command substrings refused
// outright via a coarse fast-path. The permissions package implements the full
// three-tier YAML governance; this is the hard floor that always applies, even
// when permissions are disabled. NOTE: substring matching alone is trivially
// bypassed (flag reorder, extra whitespace, long-form flags), so the fast-path
// is backed by the structural checks in SafetyFloorBlock below — do not rely on
// this list for coverage.
var alwaysBlock = []string{"rm -rf /", ":(){:|:&};:", "mkfs"}

// forkBombRe matches a fork bomb after all whitespace is stripped, independent
// of the function name: `name(){ name|name& };name` collapses to a shape like
// `(){:|:&};`. This catches the canonical `:(){ :|:& };:` and its spaced forms
// that the literal fast-path misses.
var forkBombRe = regexp.MustCompile(`\(\)\{[^}]*\|[^}]*&\}[;&]`)

// shellSplitRe splits a command line into candidate sub-commands on the shell
// operators that separate independent commands (`;`, `|`, `||`, `&&`, `&`,
// newlines). Detection does not need real pipe semantics — each segment is a
// standalone command to inspect.
var shellSplitRe = regexp.MustCompile(`[;&|\n]+`)

// blockDeviceRe matches a raw block device path (a real disk, not /dev/null or
// /dev/zero) as a dd output target or shell redirect target.
var blockDeviceRe = regexp.MustCompile(`/dev/(sd[a-z]|nvme\d|vd[a-z]|hd[a-z]|mmcblk\d|disk\d)`)

// SafetyFloorBlock reports whether command trips the hard safety floor,
// returning a short reason. It is the single source of truth shared by RunBash,
// RunBashInteractive, RunShellCaptured, and the bg queue so the floor can never
// be bypassed by routing around RunBash.
//
// It is deliberately conservative (it must not block ordinary development
// commands), so it targets a small set of unambiguously catastrophic patterns
// and is robust to the evasions the old substring-only check missed: flag
// reordering (`rm -fr /`), split/long flags (`rm -r -f /`, `rm --recursive
// --force /`), extra whitespace, and whitespace in the fork bomb. It also blocks
// a recursive-force rm of the home directory written as a tilde or HOME variable
// (`rm -rf ~`, `rm -rf $HOME/*`, `rm -rf ${HOME}`) and the two-step `cd ~ && rm
// -rf *` form — the tokens are statically visible even though their expanded
// value is not. It is NOT a general destructive-command detector — decode-then-
// exec chains (`… | base64 -d | bash`) and targets computed at runtime (a var
// holding a path the floor never sees literally) remain out of reach of static
// inspection and are governed by the permission layer.
func SafetyFloorBlock(command string) (string, bool) {
	// Fast path: exact catastrophic literals (also exercised by tests).
	for _, b := range alwaysBlock {
		if strings.Contains(command, b) {
			return b, true
		}
	}
	// Fork bomb, whitespace-insensitive and function-name agnostic.
	if forkBombRe.MatchString(stripWhitespace(command)) {
		return "fork bomb", true
	}
	// Structural checks per independent sub-command.
	for _, sub := range shellSplitRe.Split(command, -1) {
		if reason, bad := blockedSubcommand(sub); bad {
			return reason, true
		}
	}
	// Cross-segment: `cd <home-root> && rm -rf <wipe>` wipes HOME, which no
	// single-segment check can see (the target of the rm is the cwd the cd set).
	if cdThenHomeWipe(command) {
		return "recursive force rm of the home directory (after cd)", true
	}
	return "", false
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
}

// benignWrappers are command prefixes that don't change what the wrapped
// command does destructively, so the floor looks past them to the real command.
var benignWrappers = map[string]bool{
	"sudo": true, "doas": true, "env": true, "nohup": true,
	"time": true, "command": true, "exec": true, "nice": true, "ionice": true,
}

// blockedSubcommand inspects a single (operator-free) command segment and
// reports whether it is one of the catastrophic patterns the floor blocks.
func blockedSubcommand(sub string) (string, bool) {
	// Redirect into a raw block device (`> /dev/sda`).
	if strings.ContainsAny(sub, ">") && blockDeviceRe.MatchString(sub) &&
		regexp.MustCompile(`>\s*`+blockDeviceRe.String()).MatchString(sub) {
		return "redirect to block device", true
	}

	cmd, args := segmentCmdArgs(sub)
	if cmd == "" {
		return "", false
	}

	switch {
	case cmd == "rm":
		if rmHasFlag(args, 'r', "recursive") && rmHasFlag(args, 'f', "force") {
			if hasAbsolutePathArg(args) {
				return "recursive force rm of an absolute path", true
			}
			if hasHomeTargetArg(args) {
				return "recursive force rm of the home directory", true
			}
		}
	case cmd == "mkfs" || strings.HasPrefix(cmd, "mkfs."):
		return "mkfs", true
	case cmd == "dd":
		for _, a := range args {
			if strings.HasPrefix(a, "of=") && blockDeviceRe.MatchString(a) {
				return "dd to block device", true
			}
		}
	case cmd == "chmod" || cmd == "chown":
		if rmHasFlag(args, 'r', "recursive") && targetsRoot(args) {
			return "recursive " + cmd + " of /", true
		}
	case cmd == "find":
		if len(args) > 0 && args[0] == "/" && containsToken(args, "-delete") {
			return "find / -delete", true
		}
	}
	return "", false
}

func isEnvAssignment(t string) bool {
	eq := strings.IndexByte(t, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range t[:eq] {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// rmHasFlag reports whether the args carry a short flag rune (in a merged
// cluster like `-rf`/`-fr`) or its long-form equivalent (`--recursive`).
func rmHasFlag(args []string, short byte, long string) bool {
	for _, a := range args {
		if a == "--"+long {
			return true
		}
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' {
			for k := 1; k < len(a); k++ {
				c := a[k]
				if c == short || (short == 'r' && c == 'R') {
					return true
				}
			}
		}
	}
	return false
}

func hasAbsolutePathArg(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.HasPrefix(a, "/") {
			return true
		}
	}
	return false
}

// homeWipeRemainders are the sub-tokens that, appended to a home-root prefix,
// still target the home directory itself (its whole contents) rather than a
// named sub-path — so `~/*` wipes home but `~/project` does not.
var homeWipeRemainders = map[string]bool{
	"": true, "*": true, ".": true, "..": true, "./": true, "./*": true, ".*": true,
}

// isHomeRootTarget reports whether arg is a shell token that expands to the
// user's HOME directory itself (or all of its contents) — `~`, `~/`, `$HOME`,
// `${HOME}` (optionally quoted), and their whole-directory wildcards (`~/*`,
// `$HOME/.`, …). A named sub-path such as `~/project` is NOT a home-root target,
// so removing it stays ordinary work. The token forms are statically visible
// even though their expanded value is not, which is what lets the floor — the
// only guard under bypassPermissions / the `!` escape — catch them.
func isHomeRootTarget(arg string) bool {
	a := strings.Trim(arg, `"'`)
	for _, p := range []string{"~", "$HOME", "${HOME}"} {
		if a == p {
			return true
		}
		if strings.HasPrefix(a, p+"/") {
			return homeWipeRemainders[a[len(p)+1:]]
		}
	}
	return false
}

func hasHomeTargetArg(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if isHomeRootTarget(a) {
			return true
		}
	}
	return false
}

// segmentCmdArgs resolves an operator-free command segment to its command base
// name and argument list, skipping leading benign wrappers (`sudo`, `env`, …),
// their flags, and VAR=val assignments — the same normalisation blockedSubcommand
// performs before inspecting a command.
func segmentCmdArgs(sub string) (string, []string) {
	toks := strings.Fields(sub)
	i := 0
	for i < len(toks) {
		t := strings.TrimPrefix(toks[i], `\`)
		switch {
		case benignWrappers[filepath.Base(t)]:
			i++
		case strings.HasPrefix(t, "-"):
			i++
		case strings.Contains(t, "=") && isEnvAssignment(t):
			i++
		default:
			return filepath.Base(t), toks[i+1:]
		}
	}
	return "", nil
}

// rmWipesCwd reports whether an rm's targets include the current working
// directory itself (`*`, `.`, `..`, `./*`) rather than a named entry — the shape
// that, run from the home directory, deletes it.
func rmWipesCwd(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch strings.Trim(a, `"'`) {
		case "*", ".", "..", "./", "./*", ".*":
			return true
		}
	}
	return false
}

// cdThenHomeWipe detects `cd <home-root> … rm -r -f <cwd-wipe>` across command
// segments: after cd-ing to the home root, a recursive rm of the working
// directory wipes HOME. A cd into (or back out to) a named directory clears the
// flag, so wiping a project directory after cd-ing into it stays allowed.
func cdThenHomeWipe(command string) bool {
	atHomeRoot := false
	for _, sub := range shellSplitRe.Split(command, -1) {
		cmd, args := segmentCmdArgs(sub)
		switch {
		case cmd == "cd":
			atHomeRoot = len(args) == 1 && isHomeRootTarget(args[0])
		case cmd == "rm" && atHomeRoot:
			if rmHasFlag(args, 'r', "recursive") && rmHasFlag(args, 'f', "force") &&
				rmWipesCwd(args) {
				return true
			}
		}
	}
	return false
}

func targetsRoot(args []string) bool {
	for _, a := range args {
		if a == "/" {
			return true
		}
	}
	return false
}

func containsToken(args []string, tok string) bool {
	for _, a := range args {
		if a == tok {
			return true
		}
	}
	return false
}

// cwdSentinel prefixes the line RunBashInteractive appends to capture the
// shell's working directory after the command ran (so an embedded `cd`
// persists across the interactive "!" shell-escape). It is unlikely to
// collide with real command output; the parser scans from the end and takes
// the last match. See wrapCaptureCwd in bash_unix.go / bash_windows.go.
const cwdSentinel = "__OMNIS_CWD__:"

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
