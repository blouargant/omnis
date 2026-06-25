//go:build !windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// TestTerminalWebSocketEcho exercises the full terminal path: WebSocket upgrade,
// PTY-backed shell spawn, stdin → PTY (binary frame), PTY → stdout, and resize
// control (text frame). It runs a real shell, so it only builds on Unix.
func TestTerminalWebSocketEcho(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/terminal/ws", handleTerminal(serverDeps{Token: ""}))
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	// Resize control (text frame) — must not be treated as stdin.
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"cols":80,"rows":24}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	// Stdin (binary frame): a marker echo we can scan for in the PTY output.
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("echo OMNIS_MARKER_OK\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	_ = ws.SetReadDeadline(deadline)
	var got strings.Builder
	for time.Now().Before(deadline) {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage || mt == websocket.TextMessage {
			got.WriteString(string(data))
			// The literal command also echoes back from the TTY; the resolved
			// echo output is the marker on its own line. Require it to appear at
			// least twice (typed + printed) to be sure stdin reached the shell.
			if strings.Count(got.String(), "OMNIS_MARKER_OK") >= 2 {
				return // success
			}
		}
	}
	t.Fatalf("did not observe echoed marker in PTY output; got:\n%s", got.String())
}

// TestTerminalWebSocketCwdSync verifies that a `cd` inside the live shell makes
// the watcher emit a `{"cwd":…}` control frame (so the web UI Folders panel can
// follow). Linux-only in practice — the watcher reads /proc/<pid>/cwd.
func TestTerminalWebSocketCwdSync(t *testing.T) {
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/terminal/ws", handleTerminal(serverDeps{Token: ""}))
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("cd "+target+"\n")); err != nil {
		t.Fatalf("write cd: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	_ = ws.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.TextMessage {
			continue // binary = PTY output
		}
		var m struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(data, &m) == nil && m.Cwd == target {
			return // watcher reported the new cwd
		}
	}
	t.Fatalf("did not receive a cwd control frame for %q", target)
}

// TestTerminalWebSocketRejectsBadToken verifies the query-param token gate.
func TestTerminalWebSocketRejectsBadToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/terminal/ws", handleTerminal(serverDeps{Token: "secret"}))
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws?token=wrong"
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		ws.Close()
		t.Fatal("expected handshake to fail with a bad token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %v (resp=%v)", err, resp)
	}
}
