//go:build linux

package main

import (
	"os"
	"os/exec"
)

// openBrowser opens url in the user's default browser. It is a no-op when no
// graphical session is detected (DISPLAY and WAYLAND_DISPLAY both unset).
func openBrowser(url string) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return
	}
	_ = exec.Command("xdg-open", url).Start()
}
