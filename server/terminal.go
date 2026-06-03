package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ptySession is a platform-abstracted pseudo-terminal: a bidirectional byte
// stream (the shell's stdin/stdout) plus a window-resize control. The concrete
// implementation lives in terminal_unix.go (creack/pty) and terminal_windows.go
// (an unsupported stub), so cross-platform builds stay green.
type ptySession interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
}

// terminalUpgrader upgrades the HTTP request to a WebSocket. The bearer token
// (query param — browsers can't set headers on a WebSocket) is the auth gate;
// CheckOrigin additionally restricts browser clients to same-origin to prevent
// cross-site WebSocket hijacking. Non-browser clients (no Origin) are allowed.
var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

// handleTerminal serves an interactive shell over a WebSocket
// (GET /api/terminal/ws). It is registered OUTSIDE the auth-header middleware
// because browsers cannot attach an Authorization header to a WebSocket
// handshake; the bearer token is passed as the `token` query param and verified
// here with a constant-time compare (empty server token = unauthenticated mode,
// matching authMiddleware).
//
// Like the "!" shell-escape and the Monaco file-save route, the terminal
// **bypasses the agent permission layer by design**: the authenticated
// token-holder already has full host file access, and this is an explicit,
// user-driven shell. Unlike the agent's Bash tool there is no safety floor —
// it is a real interactive TTY (vim/top/ssh all work), so commands are not
// inspected. Output is never added to any conversation/LLM history.
//
// Working directory: an explicit `?cwd=` (validated to be a directory) wins,
// otherwise the `?session=`'s Folders/!cd working directory, otherwise the
// global "no session" browse directory.
func handleTerminal(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Token != "" && !constantTimeEqual(c.Query("token"), d.Token) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		dir := bashCwd.getGlobal()
		if sid := c.Query("session"); sid != "" {
			dir = bashCwd.get(sid)
		}
		if cwd := strings.TrimSpace(c.Query("cwd")); cwd != "" {
			if info, err := os.Stat(cwd); err == nil && info.IsDir() {
				dir = cwd
			}
		}

		ws, err := terminalUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return // Upgrade has already written the error response.
		}
		defer ws.Close()
		runTerminalSession(ws, dir)
	}
}

// runTerminalSession bridges a live WebSocket to a PTY-backed shell until either
// side closes. Wire protocol:
//   - client → server: BinaryMessage = raw stdin bytes; TextMessage = a
//     `{"cols":N,"rows":N}` resize control.
//   - server → client: BinaryMessage = raw PTY output bytes.
func runTerminalSession(ws *websocket.Conn, dir string) {
	pty, err := startPTYSession(dir)
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte("\r\n[terminal unavailable: "+err.Error()+"]\r\n"))
		return
	}
	defer pty.Close()

	var writeMu sync.Mutex
	done := make(chan struct{})

	// PTY → WebSocket. Closing `done` (and the ws read error it triggers) is the
	// single signal that the shell exited.
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := pty.Read(buf)
			if n > 0 {
				writeMu.Lock()
				werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n])
				writeMu.Unlock()
				if werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// WebSocket → PTY (input + resize). The shell exiting closes the PTY, which
	// makes the next ReadMessage fail and ends this loop too.
readLoop:
	for {
		mt, data, rerr := ws.ReadMessage()
		if rerr != nil {
			break
		}
		switch mt {
		case websocket.BinaryMessage:
			if _, werr := pty.Write(data); werr != nil {
				break readLoop
			}
		case websocket.TextMessage:
			var r struct {
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &r) == nil && r.Cols > 0 && r.Rows > 0 {
				_ = pty.Resize(r.Cols, r.Rows)
			}
		}
	}

	_ = pty.Close() // unblocks the PTY→WS goroutine
	<-done
}
