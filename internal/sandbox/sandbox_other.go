//go:build !linux

package sandbox

import "os"

// Available is always false off Linux: there is no Landlock.
func Available() bool { return false }

// MaybeRunHelper refuses to run a command it cannot restrict.
func MaybeRunHelper() {
	if len(os.Args) >= 2 && os.Args[1] == HelperArg {
		fail("not supported on this platform")
	}
}
