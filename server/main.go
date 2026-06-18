// Command server exposes the lead agent over an HTTP API (Gin) and serves a
// minimal vanilla-JS web chat UI from web/. One process owns one agent and
// one ADK runner; concurrent chats are isolated by sessionID through the
// runner's in-memory session service.
//
// Invocation forms:
//
//	yoke-server [flags]        run the server in the foreground (default)
//	yoke-server start [flags]  start the server detached in the background,
//	                              recording its PID in $YOKE_HOME/yoke-server.pid
//	                              and logging to $YOKE_HOME/logs/yoke-server.log
//	yoke-server stop           signal the background server to shut down
//	yoke-server status         report whether the background server is running
//
// The detached child started by "start" runs the same foreground path; the
// start/stop/status orchestration lives in daemon.go.
//
// Optional env:
//
//	YOKE_SERVER_TOKEN  Bearer token checked on every /api/* call. When
//	                      empty, token validation is skipped (unauthenticated
//	                      mode). Set it to require a Bearer token.
//
//
//	YOKE_SERVER_ADDR         Listen address. Default ":8080".
//	YOKE_HOME                Per-user state root. Default $HOME/.yoke. Every
//	                            mutable file (logs, uploads, soft-skills,
//	                            mailboxes, user config overrides) lands here.
//	YOKE_CONFIG_DIRS         Colon-separated config search chain (high→low
//	                            precedence). Replaces the default chain of
//	                            .agents : $YOKE_HOME : /etc/yoke.
//	YOKE_CONFIG_PATH         Explicit path to agent.json (forwarded to
//	                            agent.NewAgent; bypasses the chain).
//	YOKE_SKILLS_DIR          Skills directory.
//	YOKE_SOFTSKILLS_DIR      Soft-skills directory.
//	YOKE_WEB_DIR             Static UI directory. Default "web".
//	YOKE_DEBUG               "1"/"true" to enable debug logging on the agent.
//	YOKE_SERVER_GC_INTERVAL  How often to garbage-collect orphan log/upload
//	                            files (e.g. "1h", "30m"). Default "1h". Set
//	                            to "0" or "off" to disable.
//
// Plus all YOKE_* model selectors honored by core/llm.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blouargant/yoke/agent"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/sessions"
)

func main() {
	// Background-daemon subcommands are dispatched before run()'s flag parsing.
	// "start" re-execs this binary detached and frees the terminal; "stop"
	// signals it to shut down; "status" reports liveness. Any other first arg
	// (or none) falls through to the normal foreground server — which is also
	// what the detached child runs.
	if args := os.Args[1:]; len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "start":
			fatalIf(startDaemon(args[1:]))
			return
		case "stop":
			fatalIf(stopDaemon())
			return
		case "status":
			fatalIf(statusDaemon())
			return
		case "help", "--help", "-h":
			printServerUsage()
			return
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// fatalIf prints err and exits non-zero when err is non-nil.
func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// printServerUsage documents the yoke-server invocation forms.
func printServerUsage() {
	fmt.Fprint(os.Stderr, `yoke-server — HTTP API server + web UI for the yoke agent.

Usage:
  yoke-server [flags]        run the server in the foreground (default)
  yoke-server start [flags]  start the server detached in the background
  yoke-server stop           stop the background server
  yoke-server status         report whether the background server is running
  yoke-server help           show this help

Flags (foreground / forwarded to the background child):
  --no-browser   disable automatic browser launch at startup

The background server logs to $YOKE_HOME/logs/yoke-server.log and records its
PID in $YOKE_HOME/yoke-server.pid. Configuration is read from server.yaml and
the YOKE_* environment variables (see the package doc).
`)
}

func run() error {
	var noBrowser bool
	flag.BoolVar(&noBrowser, "no-browser", false, "disable automatic browser launch at startup")
	flag.Parse()

	serverCfg := loadServerConfig()

	token := strings.TrimSpace(os.Getenv("YOKE_SERVER_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(serverCfg.Token)
	}
	if token == "" {
		log.Println("server: YOKE_SERVER_TOKEN not set — running without authentication")
	}

	var addr string
	var addrFromEnv bool
	if v := os.Getenv("YOKE_SERVER_ADDR"); v != "" {
		addr = v
		addrFromEnv = true
	} else if serverCfg.Addr != "" {
		addr = serverCfg.Addr
	} else if serverCfg.Port > 0 {
		addr = fmt.Sprintf(":%d", serverCfg.Port)
	} else {
		addr = ":8080"
	}
	if serverCfg.PortAutoIncrement && !addrFromEnv {
		resolved, err := findFreePort(addr)
		if err != nil {
			return err
		}
		addr = resolved
	}
	webDir := envOr("YOKE_WEB_DIR", serverCfg.WebDir)
	if webDir == "" {
		webDir = "web"
	}
	basePath := normalizeBasePath(envOr("YOKE_SERVER_BASE_PATH", serverCfg.BasePath))

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	debug, _ := strconv.ParseBool(os.Getenv("YOKE_DEBUG"))

	agentOpts := agent.Options{
		ConfigPath:       os.Getenv("YOKE_CONFIG_PATH"),
		ConfigPathStrict: os.Getenv("YOKE_CONFIG_PATH") != "",
		SoftSkillsDir:    os.Getenv("YOKE_SOFTSKILLS_DIR"),
		AppName:          envOr("YOKE_APP_NAME", "yoke-server"),
		DebugLogging:     debug,
		// The server runs pushManager, which drains each session's leader
		// mailbox in the background and injects incoming messages as synthetic
		// turns. Suppress the leader's redundant per-turn teammate_check poll.
		BackgroundMailboxDelivery: true,
		// The web UI keeps ask-user and permission cards on screen and simply
		// waits for the user, so disarm the registry's 5-minute question
		// timeout — only a turn abort (context cancellation) ends the wait.
		DisableAskUserTimeout: true,
		// Don't abort boot when a model can't be built (e.g. OPENAI_BASE_URL /
		// OPENAI_API_KEY unset): the server starts and the web UI's
		// provider-health banner reports the unreachable provider; turns fail
		// with the real error only if the user actually chats.
		DeferModelErrors: true,
	}

	log.Printf("server: yoke home: %s", paths.Home())
	log.Printf("server: config search: %v", paths.ConfigSearchDirs())

	// Resolve the JSON config files exposed by the web UI editor up-front
	// so the HTTP handlers never derive paths from URL parameters.
	cfgFiles := resolveConfigFiles(agentOpts)
	skillDeps := resolveSkillsDeps(cfgFiles)

	log.Printf("server: building lead agent...")
	infra, err := agent.BuildInfrastructure(rootCtx, agentOpts)
	if err != nil {
		return fmt.Errorf("build infrastructure: %w", err)
	}
	if agentOpts.Repo == "" {
		agentOpts.Repo = infra.Repo
	}
	if agentOpts.AppName == "" {
		agentOpts.AppName = infra.AppName
	}
	firstInst, err := agent.BuildInstance(rootCtx, infra, agentOpts, 1)
	if err != nil {
		infra.Close()
		return fmt.Errorf("build agent: %w", err)
	}
	manager := agent.NewManager(infra, firstInst)
	defer manager.Close()

	// Wire the helper agent's registry-install tools to trigger a hot-reload so
	// a just-installed agent / MCP server / squad / A2A peer is wired into the
	// running fleet without a manual "Reload" click. In-flight sessions stay
	// pinned to their generation; new turns pick up the rebuilt one.
	agent.SetReloadHook(func() {
		if _, err := manager.Reload(rootCtx, agentOpts); err != nil {
			log.Printf("server: registry-install hot-reload failed: %v", err)
			return
		}
		log.Printf("server: hot-reload triggered by registry install (generation=%d)", manager.CurrentGeneration())
	})
	defer agent.SetReloadHook(nil)

	// Make the agent's file-system tools (Bash, Read, Write, Edit, Grep, Glob)
	// operate in each session's working directory — the same per-session cwd the
	// "!cd" shell-escape and the web-UI Folders panel mutate (bashCwd). Sessions
	// that never navigated resolve to the process working directory, so default
	// behaviour is unchanged.
	fstools.SetCwdResolver(func(sessionID string) string { return bashCwd.get(sessionID) })
	defer fstools.SetCwdResolver(nil)

	registry := sessions.NewRegistry()

	// Periodic garbage collection of orphan files in logs/ and logs/uploads/.
	// Runs an initial sweep synchronously so leftover files from a previous
	// run are cleaned up before the server starts accepting traffic.
	gcInterval, gcEnabled := parseGCInterval(os.Getenv("YOKE_SERVER_GC_INTERVAL"))
	if gcEnabled {
		log.Printf("server: garbage collector enabled (interval=%s)", gcInterval)
	} else {
		log.Printf("server: garbage collector disabled — running startup sweep only")
	}
	startGC(rootCtx, registry, gcDeps{
		listRegistry: infra.ListSessionRegistry,
		unregister:   infra.UnregisterSession,
	}, gcInterval)

	startIdleCurator(rootCtx, IdleCuratorConfig{
		Registry:    registry,
		Bus:         infra.Bus,
		IdleTimeout: firstInst.CuratorIdleTimeout,
	})

	// Index idle sessions into the cross-session precedent store. Runs on a
	// fixed staleness threshold independent of the curator, so Web UI sessions
	// (which never fire EventSessionEnd) still feed semantic recall.
	startIdleIndexer(rootCtx, IdleIndexerConfig{
		Registry: registry,
		Bus:      infra.Bus,
	})

	// Build/refresh the documentation semantic index in the background at
	// startup (no-op when nothing changed). Lets the Helper answer questions
	// about yoke grounded in its own docs.
	startDocsIndexer(rootCtx, infra, firstInst.Settings)

	// Warm the remote-registry semantic index in the background at startup so the
	// first search_registries call doesn't pay for browsing + embedding every
	// configured registry (no-op when a fresh persisted index is already loaded).
	startRegistryIndexer(rootCtx, infra, firstInst.Settings)

	runGuard := newSessionRunGuard()
	pushEvents := newSessionPushBroadcaster()
	pushMgr := newPushManager(runGuard, pushEvents, infra.WatchMailbox)

	// After a hot-reload, sessions stay pinned to their original generation
	// until the next turn. The rebind scanner releases the pin of idle
	// sessions on old generations so those generations can be torn down
	// promptly (frees MCP subprocesses, plugins, etc.) instead of lingering
	// for as long as the chat tab is open.
	startIdleRebind(rootCtx, IdleRebindConfig{
		Manager:     manager,
		Registry:    registry,
		Guard:       runGuard,
		IdleTimeout: resolveRebindIdle(),
	})

	// Start watchers for sessions that were persisted from a previous run.
	// Each persisted session pins to the current (only) generation so it
	// keeps using the same agent across future reloads.
	for _, meta := range registry.List() {
		_ = infra.RegisterSession(meta.UserID, meta.ID, func() string {
			if meta.Title != "" {
				return meta.Title
			}
			return meta.ID
		}())
		manager.Pin(meta.ID)
		pushMgr.Watch(rootCtx, serverDeps{
			Manager:      manager,
			Registry:     registry,
			RunGuard:     runGuard,
			PushEvents:   pushEvents,
			WatchMailbox: infra.WatchMailbox,
		}, meta.ID, meta.UserID)
	}

	// The restart coordinator is a one-shot signal raised by the
	// /api/server/restart handler. The actual shutdown + re-exec is
	// performed below from this goroutine so it cannot race with normal
	// server lifecycle (see comment on restartCoordinator).
	restart := newRestartCoordinator()

	engine := newEngine(serverDeps{
		Token:               token,
		Manager:             manager,
		Registry:            registry,
		WebDir:              webDir,
		BasePath:            basePath,
		EventBus:            infra.Bus,
		AskUserRegistry:     infra.AskUserRegistry,
		AgentEvents:         newAgentEventBroadcaster(infra.Bus),
		RegisterSession:     infra.RegisterSession,
		RenameSession:       infra.RenameSession,
		UnregisterSession:   infra.UnregisterSession,
		ListSessionRegistry: infra.ListSessionRegistry,
		WatchMailbox:        infra.WatchMailbox,
		RunGuard:            runGuard,
		LiveTurns:           newLiveTurnRegistry(),
		PushMgr:             pushMgr,
		PushEvents:          pushEvents,
		AgentOptions:        agentOpts,
		rootCtx:             rootCtx,
		ConfigFiles:         cfgFiles,
		SkillsDeps:          skillDeps,
		Restart:             restart,
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

	if serverCfg.OpenBrowser && !noBrowser {
		host, port, err := net.SplitHostPort(addr)
		if err == nil {
			if host == "" {
				host = "localhost"
			}
			openBrowser("http://" + net.JoinHostPort(host, port) + basePath)
		}
	}

	if serverCfg.A2AEnabled {
		a2aPort := serverCfg.A2APort
		if a2aPort <= 0 {
			a2aPort = 8081
		}
		a2aAddr := fmt.Sprintf(":%d", a2aPort)
		if serverCfg.PortAutoIncrement {
			resolved, err := findFreePort(a2aAddr)
			if err != nil {
				return err
			}
			a2aAddr = resolved
		}
		a2aSrv := newA2AServer(a2aDeps{
			Manager:         manager,
			Registry:        registry,
			RunGuard:        runGuard,
			PushEvents:      pushEvents,
			PushMgr:         pushMgr,
			RegisterSession: infra.RegisterSession,
			RootCtx:         rootCtx,
		}, token)
		go func() {
			if err := a2aSrv.serve(rootCtx, a2aAddr); err != nil {
				log.Printf("a2a: server error: %v", err)
			}
		}()
	}

	var restartRequested bool
	select {
	case <-rootCtx.Done():
		log.Printf("server: shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-restart.Done():
		restartRequested = true
		log.Printf("server: restart requested")
		// Cancel rootCtx so all goroutines and request handlers watching
		// ctx.Done() (SSE streams in particular) return promptly, allowing
		// srv.Shutdown to complete before the deadline.
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	if restartRequested {
		bin, err := os.Executable()
		if err != nil || bin == "" {
			bin = os.Args[0]
		}
		log.Printf("server: re-execing %s", bin)
		if err := reExec(bin, os.Args, os.Environ()); err != nil {
			return fmt.Errorf("re-exec %s: %w", bin, err)
		}
		// reExec replaces this process on success and never returns;
		// reaching this point is impossible without an error above.
	}
	return nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// normalizeBasePath ensures the path starts with "/" and has no trailing
// slash. An empty or root-only input returns "" so the server registers
// routes at the root (Gin treats an empty group prefix as "/").
func normalizeBasePath(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// findFreePort tries to bind to addr; if the port is in use it increments the
// port number and retries up to 100 times. Returns the first address that can
// be bound. The temporary listener is closed immediately after the check.
func findFreePort(addr string) (string, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, nil // not a host:port form — return as-is
	}
	basePort, err := strconv.Atoi(portStr)
	if err != nil {
		return addr, nil
	}
	for i := 0; i < 100; i++ {
		tryPort := basePort + i
		tryAddr := net.JoinHostPort(host, strconv.Itoa(tryPort))
		ln, err := net.Listen("tcp", tryAddr)
		if err == nil {
			_ = ln.Close()
			if i > 0 {
				log.Printf("server: port %d in use, using %d", basePort, tryPort)
			}
			return tryAddr, nil
		}
	}
	return "", fmt.Errorf("no free port in range %d-%d", basePort, basePort+99)
}
