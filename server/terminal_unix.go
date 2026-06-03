//go:build !windows

package main

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// unixPTY is a creack/pty-backed shell session.
type unixPTY struct {
	cmd *exec.Cmd
	f   *os.File
}

// startPTYSession launches the user's login shell ($SHELL, falling back to
// /bin/bash then /bin/sh) attached to a fresh PTY, rooted at dir, with a
// TERM=xterm-256color environment so colour/cursor handling works.
func startPTYSession(dir string) (ptySession, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixPTY{cmd: cmd, f: f}, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *unixPTY) Close() error {
	_ = p.f.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	return nil
}
