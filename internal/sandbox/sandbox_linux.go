//go:build linux

package sandbox

import (
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// writeRights is every Landlock filesystem right that changes something,
// for the given ABI version. Reads and execution are deliberately absent:
// the ruleset does not handle them, so they stay unrestricted.
func writeRights(abi int) uint64 {
	r := uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	return r
}

func landlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

var (
	availOnce sync.Once
	avail     bool
)

// Available reports whether the kernel offers Landlock and this binary can
// find itself to re-exec as the helper.
func Available() bool {
	availOnce.Do(func() {
		if landlockABI() < 1 {
			return
		}
		if _, err := os.Executable(); err != nil {
			return
		}
		avail = true
	})
	return avail
}

// MaybeRunHelper turns the process into the sandbox helper when invoked with
// HelperArg, and never returns in that case. Otherwise it is a no-op.
func MaybeRunHelper() {
	if len(os.Args) < 2 || os.Args[1] != HelperArg {
		return
	}
	writable, command, ok := parseHelperArgs(os.Args[2:])
	if !ok {
		fail("bad helper arguments")
	}
	// Landlock restricts the calling thread (and what it execs): keep the
	// restriction and the exec on the same OS thread.
	runtime.LockOSThread()
	if err := restrict(writable); err != nil {
		fail(err.Error())
	}
	env := os.Environ()
	err := syscall.Exec(command[0], command, env)
	fail("exec " + command[0] + ": " + err.Error())
}

func restrict(writable []string) error {
	abi := landlockABI()
	if abi < 1 {
		return errString("Landlock is not available on this kernel")
	}
	handled := writeRights(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rfd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return errString("create ruleset: " + errno.Error())
	}
	defer unix.Close(int(rfd))
	for _, p := range writable {
		fd, err := unix.Open(p, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			continue // a writable dir that does not exist grants nothing
		}
		rule := unix.LandlockPathBeneathAttr{Allowed_access: handled, Parent_fd: int32(fd)}
		var st unix.Stat_t
		if unix.Fstat(fd, &st) == nil && st.Mode&unix.S_IFMT != unix.S_IFDIR {
			// Rights that only make sense on a directory are rejected on a file.
			rule.Allowed_access &= unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE
			rule.Allowed_access &= handled
		}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, rfd, unix.LANDLOCK_RULE_PATH_BENEATH,
			uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		unix.Close(fd)
		if errno != 0 {
			return errString("add rule for " + p + ": " + errno.Error())
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return errString("no_new_privs: " + err.Error())
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, rfd, 0, 0); errno != 0 {
		return errString("restrict self: " + errno.Error())
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
