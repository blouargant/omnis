// Command server exposes the lead agent over an HTTP API (Gin) and serves a
// minimal vanilla-JS web chat UI from web/. One process owns one agent and
// one ADK runner; concurrent chats are isolated by sessionID through the
// runner's in-memory session service.
//
// Required env:
//
//	GOAGENT_SERVER_TOKEN  Bearer token required on every /api/* call
//	                      (except /api/health). Server refuses to start if
//	                      empty (fail-closed).
//
// Optional env:
//
//	GOAGENT_SERVER_ADDR     Listen address. Default ":8080".
//	GOAGENT_CONFIG_PATH     Path to config/agent.yaml (forwarded to agent.NewAgent).
//	GOAGENT_SKILLS_DIR      Skills directory.
//	GOAGENT_SOFTSKILLS_DIR  Soft-skills directory.
//	GOAGENT_WEB_DIR         Static UI directory. Default "web".
//	GOAGENT_DEBUG           "1"/"true" to enable debug logging on the agent.
//
// Plus all GOAGENT_* model selectors honored by core/llm.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blouargant/agent-toolkit/agent"
	"github.com/blouargant/agent-toolkit/core/agentkit"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	token := strings.TrimSpace(os.Getenv("GOAGENT_SERVER_TOKEN"))
	if token == "" {
		return errors.New("GOAGENT_SERVER_TOKEN is required (server refuses to start unauthenticated)")
	}

	addr := envOr("GOAGENT_SERVER_ADDR", ":8080")
	webDir := envOr("GOAGENT_WEB_DIR", "web")

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	debug, _ := strconv.ParseBool(os.Getenv("GOAGENT_DEBUG"))

	log.Printf("server: building lead agent...")
	result, err := agent.NewAgent(rootCtx, agent.Options{
		ConfigPath:       os.Getenv("GOAGENT_CONFIG_PATH"),
		ConfigPathStrict: os.Getenv("GOAGENT_CONFIG_PATH") != "",
		SkillsDir:        os.Getenv("GOAGENT_SKILLS_DIR"),
		SoftSkillsDir:    os.Getenv("GOAGENT_SOFTSKILLS_DIR"),
		AppName:          envOr("GOAGENT_APP_NAME", "agent-toolkit-server"),
		DebugLogging:     debug,
	})
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}

	r, err := agentkit.Runner("server", result.Agent, result.Plugins...)
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	registry := newRegistry()

	runGuard := newSessionRunGuard()
	pushEvents := newSessionPushBroadcaster()
	pushMgr := newPushManager(runGuard, pushEvents, result.WatchMailbox)

	// Start watchers for sessions that were persisted from a previous run.
	for _, meta := range registry.List() {
		_ = result.RegisterSession(meta.UserID, meta.ID, func() string {
			if meta.Title != "" {
				return meta.Title
			}
			return meta.ID
		}())
		pushMgr.Watch(rootCtx, serverDeps{
			Runner:     r,
			Registry:   registry,
			RunGuard:   runGuard,
			PushEvents: pushEvents,
			WatchMailbox: result.WatchMailbox,
		}, meta.ID, meta.UserID)
	}

	engine := newEngine(serverDeps{
		Token:           token,
		Runner:          r,
		Registry:        registry,
		WebDir:          webDir,
		AgentEvents:     newAgentEventBroadcaster(result.EventBus),
		RegisterSession:   result.RegisterSession,
		RenameSession:     result.RenameSession,
		UnregisterSession: result.UnregisterSession,
		WatchMailbox:    result.WatchMailbox,
		RunGuard:        runGuard,
		PushMgr:         pushMgr,
		PushEvents:      pushEvents,
		rootCtx:         rootCtx,
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE responses are long-lived.
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("server: listening on %s (web dir: %s)", addr, webDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-rootCtx.Done():
		log.Printf("server: shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
