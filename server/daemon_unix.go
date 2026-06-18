//go:build unix

package main

import "syscall"

// daemonSupported reports that background start/stop works on this platform.
const daemonSupported = true

// detachSysProcAttr makes the child its own session leader (setsid) so it
// survives the parent's exit and the controlling terminal closing.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// pidAlive reports whether a process with pid exists. Signal 0 probes for
// existence without delivering a signal; EPERM means the process exists but is
// owned by another user (still "alive" for our purposes).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// signalTerminate asks the process to shut down gracefully (SIGTERM), which the
// server traps via signal.NotifyContext to run a clean shutdown.
func signalTerminate(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
