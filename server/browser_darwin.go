//go:build darwin

package main

import "os/exec"

// openBrowser opens url in the user's default browser.
// On macOS a graphical session is always assumed to be available.
func openBrowser(url string) {
	_ = exec.Command("open", url).Start()
}
