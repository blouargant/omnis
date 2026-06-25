package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blouargant/omnis/internal/paths"
)

// errDaemonUnsupported is returned by the start/stop/status subcommands on
// platforms without process detachment + signalling support (see
// daemon_other.go). Mirrors restart_other.go's posture.
var errDaemonUnsupported = errors.New("background server (start/stop) is not supported on this platform; run omnis-server in the foreground")

// pidFilePath is where the background server records its PID. Anchored under
// $OMNIS_HOME so start/stop/status agree regardless of the caller's CWD.
func pidFilePath() string {
	return filepath.Join(paths.Home(), "omnis-server.pid")
}

// daemonLogPath is where the detached child's stdout/stderr are redirected so
// nothing is lost once the controlling terminal is freed.
func daemonLogPath() string {
	return filepath.Join(paths.Home(), "logs", "omnis-server.log")
}

// readPID reads the PID recorded in the pid file. It returns (0, nil) when the
// file is absent, and an error only on an unreadable or malformed file.
func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("malformed pid file %s: %w", pidFilePath(), err)
	}
	return pid, nil
}

// startDaemon re-execs this binary as a detached background process running the
// foreground HTTP server, records its PID, and returns immediately so the
// controlling terminal's handle is freed. extraArgs are forwarded to the child
// (the flags that followed "start" on the command line).
func startDaemon(extraArgs []string) error {
	if !daemonSupported {
		return errDaemonUnsupported
	}

	if pid, err := readPID(); err != nil {
		return err
	} else if pid > 0 && pidAlive(pid) {
		return fmt.Errorf("omnis-server already running (pid %d) — use 'omnis-server stop' first", pid)
	}

	self, err := os.Executable()
	if err != nil || self == "" {
		self = os.Args[0]
	}

	logPath := daemonLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	defer logFile.Close()

	// Forward the operator's flags verbatim. The child runs the same
	// foreground path, so it opens a browser exactly like "omnis-server" does
	// (per server.yaml's open_browser) — opening the *actually bound* address
	// after any port auto-increment. openBrowser is fire-and-forget and only
	// needs DISPLAY/WAYLAND_DISPLAY (inherited), not a terminal. Pass
	// --no-browser to "start" to suppress it.
	cmd := exec.Command(self, extraArgs...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Inherit CWD: config search (.agents) and the default web dir ("web") are
	// CWD-relative, so the child must start where "start" was invoked.
	cmd.Env = append(os.Environ(), "OMNIS_SERVER_DAEMONIZED=1")
	cmd.SysProcAttr = detachSysProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start background server: %w", err)
	}
	pid := cmd.Process.Pid

	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("write pid file: %w", err)
	}
	// Don't Wait() — let the child outlive us. Release frees the parent-side
	// process handle so we exit cleanly without leaving a zombie.
	_ = cmd.Process.Release()

	// Brief liveness grace: catch an immediate failure (e.g. port already in
	// use) so we report it here rather than falsely claiming success.
	time.Sleep(400 * time.Millisecond)
	if !pidAlive(pid) {
		_ = os.Remove(pidFilePath())
		return fmt.Errorf("omnis-server exited immediately after start — see %s", logPath)
	}

	fmt.Printf("omnis-server started in background (pid %d)\n", pid)
	fmt.Printf("  logs: %s\n", logPath)
	fmt.Printf("  stop: %s stop\n", filepath.Base(self))
	return nil
}

// stopDaemon signals the recorded background server to shut down and waits for
// it to exit, then removes the pid file. Missing/stale pid files are handled
// gracefully.
func stopDaemon() error {
	if !daemonSupported {
		return errDaemonUnsupported
	}

	pid, err := readPID()
	if err != nil {
		return err
	}
	if pid == 0 {
		fmt.Println("omnis-server is not running (no pid file)")
		return nil
	}
	if !pidAlive(pid) {
		_ = os.Remove(pidFilePath())
		fmt.Printf("omnis-server is not running (cleared stale pid %d)\n", pid)
		return nil
	}

	if err := signalTerminate(pid); err != nil {
		return fmt.Errorf("signal omnis-server (pid %d): %w", pid, err)
	}

	// The server handles SIGTERM with a graceful shutdown (see run()); give it
	// time to drain in-flight turns before reporting.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			_ = os.Remove(pidFilePath())
			fmt.Printf("omnis-server stopped (pid %d)\n", pid)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("omnis-server (pid %d) did not stop within 15s; it may still be draining — re-run 'stop' or send SIGKILL manually", pid)
}

// statusDaemon prints whether the recorded background server is running.
func statusDaemon() error {
	pid, err := readPID()
	if err != nil {
		return err
	}
	if pid == 0 || !pidAlive(pid) {
		fmt.Println("omnis-server: stopped")
		return nil
	}
	fmt.Printf("omnis-server: running (pid %d)\n", pid)
	return nil
}
