//go:build windows

package main

import (
	"os/exec"
)

// openBrowser opens url in the user's default browser.
// On Windows a graphical session is always assumed to be available.
func openBrowser(url string) {
	_ = exec.Command("cmd", "/c", "start", url).Start()
}
