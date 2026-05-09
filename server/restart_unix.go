//go:build unix

package main

import "syscall"

// reExec replaces the current process with bin, preserving argv and the
// environment. On success it never returns. Unix-only: see restart_other.go
// for the fallback used on platforms without execve(2).
func reExec(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}
