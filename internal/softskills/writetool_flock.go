//go:build !windows

package softskills

import (
	"os"
	"syscall"
)

// flockExclusive / flockUnlock use POSIX advisory locks on unix platforms.
// The in-process mutex in the caller still serializes concurrent goroutines.
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
