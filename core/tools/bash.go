package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blouargant/yoke/internal/filter"
)

// alwaysBlock contains substrings that RunBash refuses outright. The
// permissions package implements the full three-tier YAML governance; this
// is the hard floor that always applies, even when permissions are disabled.
var alwaysBlock = []string{"rm -rf /", ":(){:|:&};:", "mkfs"}

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
		rulesDir = filter.DefaultRulesDir
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
}
type BashOut struct {
	Output string `json:"output"`
}

// RunBash executes a shell command via /bin/sh -c, with a default 120s
// timeout. Output is truncated at MaxToolOutput.
func RunBash(ctx context.Context, in BashIn) (string, error) {
	for _, b := range alwaysBlock {
		if strings.Contains(in.Command, b) {
			return fmt.Sprintf("Error: command blocked by safety floor (%q)", b), nil
		}
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
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", execCommand)
	// Put the shell in its own process group so that all child processes it
	// spawns are part of the same group. When the context deadline fires,
	// cmd.Cancel kills the entire group (negative PID), ensuring orphaned
	// children don't keep the stdout/stderr pipes open and cause
	// CombinedOutput to hang past the timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
