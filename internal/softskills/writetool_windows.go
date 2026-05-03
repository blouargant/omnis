//go:build windows

package softskills

import "os"

// flockExclusive and flockUnlock are no-ops on Windows.
// The in-process mutex in the caller serializes concurrent goroutines.
func flockExclusive(_ *os.File) error { return nil }
func flockUnlock(_ *os.File) error    { return nil }
