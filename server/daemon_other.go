//go:build !unix

package main

import "syscall"

// daemonSupported is false on platforms without setsid/Kill detachment +
// signalling; start/stop/status return errDaemonUnsupported. Mirrors
// restart_other.go's posture for cross-platform builds.
const daemonSupported = false

func detachSysProcAttr() *syscall.SysProcAttr { return nil }

func pidAlive(pid int) bool { return false }

func signalTerminate(pid int) error { return errDaemonUnsupported }
