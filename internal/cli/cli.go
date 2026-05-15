// Package cli implements yoke's stdio interface: an interactive REPL when
// stdin is a terminal, or a one-shot turn when invoked with a prompt
// argument or piped input.
//
// The CLI deliberately stays plain-text: ANSI dim is used for the trace of
// tool calls in interactive mode, but markdown is not pre-rendered so output
// remains stable when piped to files or other tools. For a styled experience,
// use `yoke tui`; for a web UI, run the server.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"

	"golang.org/x/term"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/askuser"
)

// Config bundles everything the CLI needs to run.
type Config struct {
	// Runner is the ADK runner driving the lead agent.
	Runner *runner.Runner
	// Bus is the optional shared event bus. Currently unused inside the
	// CLI, but reserved so callers can wire trace overlays in the future
	// without changing the constructor signature.
	Bus *events.Bus
	// AskUserRegistry, when non-nil, gets a stdin-backed asker installed
	// so the agent's ask_user tool prompts the user on stderr.
	AskUserRegistry *askuser.Registry
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
		cfg.AppName = "yoke"
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
	turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runTurn(turnCtx, cfg, prompt, false /*showTrace*/)
}

// runRepl is the interactive read-eval-print loop. Each turn gets its own
// SIGINT-cancellable context so Ctrl-C cancels the in-flight turn without
// killing the REPL; Ctrl-D (EOF) or `/quit` exits cleanly.
func runRepl(ctx context.Context, cfg Config) error {
	fmt.Fprintf(cfg.Stdout, "%s — interactive mode. Type /help, /quit or press Ctrl-D to exit.\n", cfg.AppName)

	reader := bufio.NewReader(cfg.Stdin)
	var readMu sync.Mutex // serializes goroutine reads across iterations

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
		if strings.HasPrefix(prompt, "/") {
			if quit := handleSlash(cfg, prompt); quit {
				return nil
			}
			continue
		}

		turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
		err = runTurn(turnCtx, cfg, prompt, true /*showTrace*/)
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

// runTurn streams one runner.Run invocation to cfg.Stdout. When showTrace is
// true, tool calls and responses are echoed dimly to cfg.Stderr so the user
// sees the agent's activity in interactive mode. In one-shot mode the trace
// is suppressed so piped output stays clean.
func runTurn(ctx context.Context, cfg Config, prompt string, showTrace bool) error {
	seq := cfg.Runner.Run(ctx, cfg.UserID, cfg.SessionID,
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		adkagent.RunConfig{})

	wroteText := false
	for ev, err := range seq {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(cfg.Stderr, "\n(cancelled)")
				return nil
			}
			return fmt.Errorf("run: %w", err)
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
				fmt.Fprint(cfg.Stdout, p.Text)
				wroteText = true
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
	if wroteText {
		fmt.Fprintln(cfg.Stdout)
	}
	return nil
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
		fmt.Fprintln(cfg.Stdout, "Tips:")
		fmt.Fprintln(cfg.Stdout, "  Ctrl-C cancels an in-flight turn; Ctrl-D exits the REPL.")
		return false
	default:
		fmt.Fprintf(cfg.Stderr, "Unknown command: /%s (try /help)\n", cmd)
		return false
	}
}
