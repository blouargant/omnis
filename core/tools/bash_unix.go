//go:build !windows

package tools

import (
	"context"
	"os/exec"
	"syscall"
)

// newShellCommand runs the command line through /bin/sh -c in its own
// process group. Putting the shell in a fresh group lets cmd.Cancel kill the
// entire group (negative PID) when the context deadline fires, so any child
// processes the shell spawned are reaped too and can't keep the
// stdout/stderr pipes open past the timeout.
func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}

// wrapCaptureCwd appends a line that prints the shell's working directory
// after the user command runs, so an embedded `cd` persists across separate
// RunBashInteractive calls. Newline-separated (not `&&`) so the pwd is emitted
// regardless of the command's exit status, and the original status is
// preserved via the trailing `exit`.
func wrapCaptureCwd(command string) string {
	return command + "\n__omnis_rc=$?\nprintf '%s%s\\n' '" + cwdSentinel + "' \"$(pwd)\"\nexit $__omnis_rc"
}
