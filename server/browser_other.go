//go:build !linux && !darwin && !windows

package main

// openBrowser is a no-op on unsupported platforms.
func openBrowser(url string) {}
