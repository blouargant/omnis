// Package cli implements omnis's stdio interface: an interactive REPL when
// stdin is a terminal, or a one-shot turn when invoked with a prompt
// argument or piped input.
//
// The CLI deliberately stays plain-text: ANSI dim is used for the trace of
// tool calls in interactive mode, but markdown is not pre-rendered so output
// remains stable when piped to files or other tools. For a styled experience,
// use `omnis tui`; for a web UI, run the server.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"golang.org/x/term"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/agentmd"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/bg"
	"github.com/blouargant/omnis/internal/fileref"
)

// Config bundles everything the CLI needs to run.
type Config struct {
	// Runner is the ADK runner driving the lead agent. Used as the sole runner
	// when Manager is nil (e.g. examples that build a bare runner).
	Runner *runner.Runner
	// Manager, when set, enables the Omnis routing dispatch loop: each turn runs
	// through Manager.RunWithRouting so the router can hand control to the
	// best-suited squad (and squads can hand back). When nil the CLI runs the
	// single Runner above with no routing.
	Manager *toolkitagent.Manager
	// Squad is the starting squad for the session (the router squad when routing
	// is enabled). Only consulted when Manager is set.
	Squad string
	// Bus is the optional shared event bus. Currently unused inside the
	// CLI, but reserved so callers can wire trace overlays in the future
	// without changing the constructor signature.
	Bus *events.Bus
	// AskUserRegistry, when non-nil, gets a stdin-backed asker installed
	// so the agent's ask_user tool prompts the user on stderr.
	AskUserRegistry *askuser.Registry
	// BgQueues, when non-nil, is drained before each turn so completed
	// background-task notifications are surfaced to the model (the CLI/TUI
	// between-turn drain — there is no server-style push goroutine here).
	BgQueues *bg.SessionQueues
	// UserID, SessionID default to "user" and a timestamped value.
	UserID    string
	SessionID string
	AppName   string
	// Prompt, when non-empty, runs a single non-interactive turn and exits.
	// If Prompt is empty and stdin is not a terminal, all of stdin is read
	// and used as the prompt.
	Prompt string
	// Stdout / Stderr / Stdin override the default OS streams. Mainly for
	// tests.
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
}

// Run dispatches between one-shot and REPL based on Config.Prompt and TTY
// detection.
//
//   - Prompt set, or stdin not a terminal → one-shot
//   - Otherwise → interactive REPL
func Run(ctx context.Context, cfg Config) error {
	if cfg.Runner == nil {
		return errors.New("cli: Runner required")
	}
	if cfg.UserID == "" {
		cfg.UserID = "user"
	}
	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("cli-%d", time.Now().Unix())
	}
	if cfg.AppName == "" {
		cfg.AppName = "omnis"
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}

	if cfg.AskUserRegistry != nil {
		askuser.InstallStdinAsker(cfg.AskUserRegistry)
	}

	interactive := cfg.Prompt == "" && isStdinTerminal(cfg.Stdin)
	if !interactive {
		prompt := cfg.Prompt
		if prompt == "" {
			data, err := io.ReadAll(cfg.Stdin)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			prompt = strings.TrimSpace(string(data))
			if prompt == "" {
				return errors.New("no prompt provided (pass an argument or pipe input)")
			}
		}
		return runOneShot(ctx, cfg, prompt)
	}
	return runRepl(ctx, cfg)
}

// isStdinTerminal returns true when r is os.Stdin attached to a tty. Any
// non-*os.File (e.g. a buffer in tests) is treated as non-interactive.
func isStdinTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// runOneShot runs a single turn with prompt and returns when the model
// finishes or ctx is cancelled.
func runOneShot(ctx context.Context, cfg Config, prompt string) error {
	// "#<text>" appends a memory to the project AGENT.md and exits without a turn.
	if strings.HasPrefix(strings.TrimSpace(prompt), "#") {
		path, err := agentmd.AppendMemory("", prompt)
		if err != nil {
			return err
		}
		fmt.Fprintf(cfg.Stdout, "saved to %s\n", path)
		return nil
	}
	// "/init" expands to the AGENT.md bootstrap prompt and runs as a turn.
	if strings.EqualFold(strings.TrimSpace(prompt), "/init") {
		prompt = agentmd.InitPrompt()
	}
	turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_, err := runTurn(turnCtx, cfg, prompt, false /*showTrace*/, cfg.Squad)
	return err
}

// runRepl is the interactive read-eval-print loop. Each turn gets its own
// SIGINT-cancellable context so Ctrl-C cancels the in-flight turn without
// killing the REPL; Ctrl-D (EOF) or `/quit` exits cleanly.
func runRepl(ctx context.Context, cfg Config) error {
	fmt.Fprintf(cfg.Stdout, "%s — interactive mode. Type /help, /quit or press Ctrl-D to exit.\n", cfg.AppName)

	reader := bufio.NewReader(cfg.Stdin)
	var readMu sync.Mutex // serializes goroutine reads across iterations

	// currentSquad tracks which squad the session is on across turns so the
	// Omnis router's routing persists (and a squad keeps its context when the
	// conversation returns to it). Starts on the router squad when routing is on.
	currentSquad := cfg.Squad

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		fmt.Fprint(cfg.Stdout, "> ")
		line, err := readLineCtx(ctx, &readMu, reader)
		if err == io.EOF {
			fmt.Fprintln(cfg.Stdout)
			return nil
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}

		prompt := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if prompt == "" {
			continue
		}
		// "#<text>" appends a one-line memory to the project AGENT.md instead
		// of starting a turn — symmetric with the web UI / TUI shortcut.
		if strings.HasPrefix(prompt, "#") {
			if path, err := agentmd.AppendMemory("", prompt); err != nil {
				fmt.Fprintf(cfg.Stderr, "memory: %v\n", err)
			} else {
				fmt.Fprintf(cfg.Stdout, "saved to %s\n", path)
			}
			continue
		}
		// "/init" expands to the AGENT.md bootstrap prompt and runs as a normal
		// agent turn; other slash commands are REPL-only.
		if strings.EqualFold(prompt, "/init") {
			prompt = agentmd.InitPrompt()
		} else if strings.HasPrefix(prompt, "/") {
			if quit := handleSlash(cfg, prompt); quit {
				return nil
			}
			continue
		}

		turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
		currentSquad, err = runTurn(turnCtx, cfg, prompt, true /*showTrace*/, currentSquad)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
}

// readLineCtx reads one line, returning io.EOF on EOF, ctx.Err() if ctx is
// cancelled before the line completes. The underlying read goroutine is
// allowed to leak briefly when ctx fires — it unblocks once the next byte
// arrives, which is acceptable for a single-process REPL on shutdown.
func readLineCtx(ctx context.Context, mu *sync.Mutex, r *bufio.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		mu.Lock()
		defer mu.Unlock()
		s, err := r.ReadString('\n')
		ch <- result{line: s, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		return res.line, res.err
	}
}

// runTurn streams one user turn to cfg.Stdout and returns the squad the turn
// ended on (so the REPL can resume there). When showTrace is true, tool calls
// and responses are echoed dimly to cfg.Stderr so the user sees the agent's
// activity in interactive mode. In one-shot mode the trace is suppressed so
// piped output stays clean.
//
// When cfg.Manager is set the turn runs through the Omnis routing dispatch
// loop (the router can hand control to the best-suited squad, and squads can
// hand back); otherwise it runs the single cfg.Runner with no routing.
func runTurn(ctx context.Context, cfg Config, prompt string, showTrace bool, startSquad string) (string, error) {
	parts := []*genai.Part{{Text: prompt}}
	// Inline the content of any "@path" file references in the prompt, resolved
	// against the process working directory.
	if note := fileref.Context(prompt, ""); note != "" {
		parts = append(parts, &genai.Part{Text: note})
	}
	// Surface any completed background-task notifications to the model by
	// draining the per-session queue between turns (the CLI has no push
	// goroutine). The router only sees the prompt, so this stays off routerParts.
	if cfg.BgQueues != nil {
		if pending := cfg.BgQueues.For(cfg.UserID, cfg.SessionID).Drain(); len(pending) > 0 {
			parts = append(parts, &genai.Part{Text: bg.FormatBatch(pending)})
		}
	}
	// The router only needs the user's words to pick a squad — not the inlined
	// @file contents — so it gets a prompt-only view; the answering squad still
	// receives the full `parts` (with the inlined file content).
	routerParts := []*genai.Part{{Text: prompt}}

	// stream renders one ADK event sequence: assistant text to stdout, tool
	// activity (when showTrace) to stderr. Returns the assistant text produced.
	// When quiet, the assistant text is accumulated but NOT written to stdout —
	// the router hop uses this to withhold its routing chatter until we know
	// whether it routed.
	stream := func(seq iter.Seq2[*session.Event, error], quiet bool) (string, error) {
		var text strings.Builder
		for ev, err := range seq {
			if err != nil {
				if errors.Is(err, context.Canceled) {
					fmt.Fprintln(cfg.Stderr, "\n(cancelled)")
					return text.String(), nil
				}
				return text.String(), fmt.Errorf("run: %w", err)
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			for _, p := range ev.Content.Parts {
				if p == nil {
					continue
				}
				switch {
				case p.Text != "":
					if !quiet {
						fmt.Fprint(cfg.Stdout, p.Text)
					}
					text.WriteString(p.Text)
				case p.FunctionCall != nil:
					if showTrace {
						fmt.Fprintf(cfg.Stderr, "\n\x1b[2m→ %s\x1b[0m\n", p.FunctionCall.Name)
					}
				case p.FunctionResponse != nil:
					if showTrace {
						fmt.Fprintf(cfg.Stderr, "\x1b[2m✓ %s\x1b[0m\n", p.FunctionResponse.Name)
					}
				}
			}
		}
		return text.String(), nil
	}

	// No manager → single-runner path (back-compat for examples).
	if cfg.Manager == nil {
		seq := cfg.Runner.Run(ctx, cfg.UserID, cfg.SessionID,
			&genai.Content{Role: "user", Parts: parts},
			adkagent.RunConfig{})
		text, err := stream(seq, false)
		if strings.TrimSpace(text) != "" {
			fmt.Fprintln(cfg.Stdout)
		}
		return startSquad, err
	}

	routerSquad := cfg.Manager.RouterSquad()
	// Routing path: each hop streams one squad turn; control returns here
	// between hops so a topic switch routes to another squad seamlessly.
	//
	// The router hop is suppressed: the model often narrates ("Routed to the
	// default squad…") despite the instruction. We accumulate its text quietly
	// and print it only if the router did not route (a clarifying question); a
	// route discards it. The "── routed to X squad ──" trace (notify) is the only
	// routing signal.
	runHop := func(rctx context.Context, sq *toolkitagent.SquadInstance, squadName string, hopParts []*genai.Part) (string, error) {
		seq := sq.Runner.Run(rctx, cfg.UserID, cfg.SessionID,
			&genai.Content{Role: "user", Parts: hopParts},
			adkagent.RunConfig{})
		isRouter := routerSquad != "" && squadName == routerSquad
		if !isRouter {
			return stream(seq, false)
		}
		text, err := stream(seq, true /*quiet*/)
		if err != nil {
			return text, err
		}
		if cfg.Manager.PendingRoute(cfg.SessionID) {
			return "", nil // routed → discard the router's chatter
		}
		// Router chose to talk to the user (no route): print its reply now.
		if strings.TrimSpace(text) != "" {
			fmt.Fprint(cfg.Stdout, text)
		}
		return text, nil
	}
	notify := func(from, to, reason string) {
		if showTrace {
			fmt.Fprintf(cfg.Stderr, "\n\x1b[2m── routed to %s squad ──\x1b[0m\n", to)
		}
	}
	finalSquad, text, err := cfg.Manager.RunWithRouting(ctx, cfg.UserID, cfg.SessionID, startSquad, parts, routerParts, runHop, notify)
	if strings.TrimSpace(text) != "" {
		fmt.Fprintln(cfg.Stdout)
	}
	return finalSquad, err
}

// handleSlash dispatches REPL-only slash commands. Returns true when the
// REPL should exit.
func handleSlash(cfg Config, line string) bool {
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	switch cmd {
	case "quit", "exit", "q":
		return true
	case "help", "?":
		fmt.Fprintln(cfg.Stdout, "Slash commands:")
		fmt.Fprintln(cfg.Stdout, "  /quit, /exit, /q    Exit the REPL")
		fmt.Fprintln(cfg.Stdout, "  /help, /?           Show this help")
		fmt.Fprintln(cfg.Stdout, "  /init               Analyze the repo and write a starter AGENT.md")
		fmt.Fprintln(cfg.Stdout, "  #<text>             Append a one-line memory to the project AGENT.md")
		fmt.Fprintln(cfg.Stdout, "Tips:")
		fmt.Fprintln(cfg.Stdout, "  Ctrl-C cancels an in-flight turn; Ctrl-D exits the REPL.")
		return false
	default:
		fmt.Fprintf(cfg.Stderr, "Unknown command: /%s (try /help)\n", cmd)
		return false
	}
}
