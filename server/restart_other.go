//go:build !unix

package main

import "errors"

// reExec is unsupported on non-Unix platforms. The HTTP /server/restart
// endpoint will still trigger a clean shutdown, but the operator must
// manually relaunch the binary.
func reExec(bin string, argv, env []string) error {
	_ = bin
	_ = argv
	_ = env
	return errors.New("restart not supported on this platform; relaunch the binary manually")
}
