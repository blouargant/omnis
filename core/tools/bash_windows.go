//go:build windows

package tools

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// newShellCommand runs the command line through cmd.exe /C in a new process
// group. Windows has neither /bin/sh nor signal-based process-group kills, so
// the Unix "negative-PID SIGKILL" is emulated by shelling out to
// `taskkill /T /F`, which terminates the whole process tree rooted at the
// shell when the context deadline fires.
//
// NOTE: the Bash tool's command strings are POSIX-shell oriented. On native
// Windows they execute under cmd.exe, so shell builtins, quoting, and
// pipelines that assume /bin/sh semantics may not behave identically. Hosts
// that need bash semantics on Windows should run yoke-server under WSL or a
// Git-Bash environment. See packaging/README.md.
func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe", "/C", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		return kill.Run()
	}
	return cmd
}

// wrapCaptureCwd appends an `echo` of the current directory (%CD%) after the
// user command so an embedded `cd` persists across separate
// RunBashInteractive calls. Unlike the Unix variant the command's exit status
// is not preserved (cmd.exe makes that awkward) — interactive callers rely on
// the visible output rather than the exit code.
func wrapCaptureCwd(command string) string {
	return command + " & echo " + cwdSentinel + "%CD%"
}
