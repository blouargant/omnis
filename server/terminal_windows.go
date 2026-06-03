//go:build windows

package main

import "errors"

// startPTYSession is unsupported on Windows: creack/pty has no ConPTY backend
// here, so the terminal feature reports a clean error instead of breaking the
// build. (A ConPTY implementation could be added later behind this same seam.)
func startPTYSession(dir string) (ptySession, error) {
	return nil, errors.New("interactive terminal is not supported on Windows")
}
