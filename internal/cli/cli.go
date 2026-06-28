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
	"github.com/blouargant/omnis/internal/goal"
	"github.com/blouargant/omnis/internal/scheduler"
	"github.com/blouargant/omnis/internal/steer"
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
	// SteerStore, when non-nil, enables mid-turn steering in the REPL: a line
	// typed while a turn is running is queued (picked up by the steering plugin
	// at the next model boundary, or run as a follow-up turn). Nil-safe — when
	// nil the REPL ignores input typed during a turn (the old behaviour).
	SteerStore *steer.Store
	// Scheduler, when non-nil, enables /loop and /schedule in the REPL. Due jobs
	// fire into the current session between idle prompts (the single-session
	// fallback; the server spawns a fresh session per scheduled run instead).
	// Nil-safe — when nil the commands report that scheduling is unavailable.
	Scheduler *scheduler.Scheduler
	// GoalStore, when non-nil (with Manager set), enables /goal in the REPL: the
	// agent keeps taking turns until the evaluator judges the condition met.
	// Nil-safe — when nil /goal reports that goals are unavailable.
	GoalStore *goal.Store
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
	_, _, err := runTurn(turnCtx, cfg, prompt, false /*showTrace*/, cfg.Squad)
	return err
}

// runRepl is the interactive read-eval-print loop. Each turn gets its own
// SIGINT-cancellable context so Ctrl-C cancels the in-flight turn without
// killing the REPL; Ctrl-D (EOF) or `/quit` exits cleanly.
func runRepl(ctx context.Context, cfg Config) error {
	fmt.Fprintf(cfg.Stdout, "%s — interactive mode. Type /help, /quit or press Ctrl-D to exit.\n", cfg.AppName)
	if cfg.SteerStore != nil {
		fmt.Fprintln(cfg.Stdout, "(Tip: type extra notes while the agent is working — they steer the running turn.)")
	}

	// currentSquad tracks which squad the session is on across turns so the
	// Omnis router's routing persists (and a squad keeps its context when the
	// conversation returns to it). Starts on the router squad when routing is on.
	currentSquad := cfg.Squad

	// Persistent stdin reader: always reading, so the user can type WHILE a turn
	// is running (mid-turn steering). Each line flows on `lines`; the loop routes
	// it as a new prompt when idle or as a steering note when a turn is in
	// flight. Read errors (incl. EOF) flow on `readErr`.
	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(cfg.Stdin)
		for {
			s, err := reader.ReadString('\n')
			if s != "" {
				select {
				case lines <- s:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				select {
				case readErr <- err:
				case <-ctx.Done():
				}
				return
			}
		}
	}()

	// Scheduler ticker: due /loop and /schedule jobs are delivered on `fired` and
	// run as a turn when the REPL is idle. fire blocks until the loop consumes the
	// job, so the scheduler's in-flight guard prevents a slow turn from stacking.
	fired := make(chan scheduler.Job)
	if cfg.Scheduler != nil {
		go cfg.Scheduler.Run(ctx, func(_ context.Context, job scheduler.Job) {
			select {
			case fired <- job:
			case <-ctx.Done():
			}
		})
	}

	type turnResult struct {
		squad string
		err   error
	}
	var (
		turnDone   chan turnResult // non-nil while a turn is running
		turnCancel context.CancelFunc
		exitAfter  bool // EOF arrived mid-turn: exit once it finishes
	)
	startTurn := func(prompt string) {
		turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
		turnCancel = cancel
		turnDone = make(chan turnResult, 1)
		go func(p, sq string, tctx context.Context, done chan turnResult) {
			squad, err := runTurnSteering(tctx, cfg, p, sq)
			done <- turnResult{squad: squad, err: err}
		}(prompt, currentSquad, turnCtx, turnDone)
	}
	showPrompt := func() {
		if turnDone == nil {
			fmt.Fprint(cfg.Stdout, "> ")
		}
	}
	showPrompt()

	for {
		select {
		case <-ctx.Done():
			return nil

		case err := <-readErr:
			if err == io.EOF {
				if turnDone != nil {
					exitAfter = true // let the running turn finish first
					continue
				}
				fmt.Fprintln(cfg.Stdout)
				return nil
			}
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("read input: %w", err)

		case res := <-turnDone:
			turnDone = nil
			if turnCancel != nil {
				turnCancel()
				turnCancel = nil
			}
			if res.err != nil && !errors.Is(res.err, context.Canceled) {
				return res.err
			}
			currentSquad = res.squad
			if exitAfter {
				fmt.Fprintln(cfg.Stdout)
				return nil
			}
			showPrompt()

		case line := <-lines:
			prompt := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
			if prompt == "" {
				showPrompt()
				continue
			}
			// A turn is running → this line is a mid-turn steering note.
			if turnDone != nil {
				if cfg.SteerStore != nil {
					cfg.SteerStore.Enqueue(cfg.SessionID, prompt)
					fmt.Fprintf(cfg.Stdout, "  \x1b[2m↳ steering queued:\x1b[0m %s\n", prompt)
				}
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
				showPrompt()
				continue
			}
			// "/init" expands to the AGENT.md bootstrap prompt and runs as a normal
			// agent turn; other slash commands are REPL-only.
			if strings.EqualFold(prompt, "/init") {
				prompt = agentmd.InitPrompt()
			} else if lc := strings.ToLower(prompt); lc == "/goal" || strings.HasPrefix(lc, "/goal ") {
				// "/goal ..." sets / inspects / clears a session completion goal.
				// Setting one starts a turn with the condition as the directive; the
				// autonomous loop in runTurnSteering then continues it after each turn.
				arg := strings.TrimSpace(prompt[len("/goal"):])
				if start, run := handleGoalCLI(cfg, arg); run {
					startTurn(start)
				} else {
					showPrompt()
				}
				continue
			} else if strings.HasPrefix(prompt, "/") {
				if quit := handleSlash(cfg, prompt); quit {
					return nil
				}
				showPrompt()
				continue
			}
			// Run the turn in a goroutine so the reader keeps accepting steering
			// notes while it runs. Ctrl-C cancels just this turn.
			startTurn(prompt)

		case job := <-fired:
			// A scheduled /loop or /schedule fired. Skip it while a turn is in
			// flight (it recurs); otherwise run its prompt in the current session.
			if turnDone != nil {
				continue
			}
			fmt.Fprintf(cfg.Stdout, "\n\x1b[2m⏰ %s (%s):\x1b[0m %s\n", job.Kind, job.Spec, scheduler.FirstLine(job.Prompt))
			startTurn(job.Prompt)
		}
	}
}

// handleGoalCLI implements the REPL /goal command. It returns (startPrompt,
// true) when the caller should start a turn — a goal was just set, with the
// condition as the directive; otherwise ("", false): status and clear are
// handled inline here.
func handleGoalCLI(cfg Config, arg string) (string, bool) {
	if cfg.GoalStore == nil || cfg.Manager == nil {
		fmt.Fprintln(cfg.Stdout, "goals are not available in this mode")
		return "", false
	}
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		g, ok := cfg.GoalStore.Get(cfg.SessionID)
		if !ok || g.Condition == "" {
			fmt.Fprintln(cfg.Stdout, "no active goal — set one with /goal <condition>")
			return "", false
		}
		if g.Achieved {
			fmt.Fprintf(cfg.Stdout, "◎ goal achieved in %d turn(s), %s: %s\n", g.Turns, g.Duration().Round(time.Second), g.Condition)
		} else {
			fmt.Fprintf(cfg.Stdout, "◎ goal active — %d/%d turns, %s elapsed\n  condition: %s\n", g.Turns, goal.MaxTurns(), g.Duration().Round(time.Second), g.Condition)
			if g.LastReason != "" {
				fmt.Fprintf(cfg.Stdout, "  latest: %s\n", g.LastReason)
			}
		}
		return "", false
	case goal.IsClearAlias(arg):
		if cfg.GoalStore.Clear(cfg.SessionID) {
			fmt.Fprintln(cfg.Stdout, "goal cleared")
		} else {
			fmt.Fprintln(cfg.Stdout, "no active goal")
		}
		return "", false
	default:
		cond := goal.CleanCondition(arg)
		cfg.GoalStore.Set(cfg.SessionID, cond)
		fmt.Fprintf(cfg.Stdout, "◎ goal set — working until: %s\n", cond)
		return cond, true
	}
}

// runTurnSteering runs one user turn, then any mid-turn steering the model never
// reached as follow-up turns — mirroring the server/TUI loop. The steering
// plugin injects queued notes into the running turn at its model boundaries;
// TakePending here delivers whatever it missed. With no steering it runs exactly
// one turn.
func runTurnSteering(ctx context.Context, cfg Config, prompt, startSquad string) (string, error) {
	squad := startSquad
	turnPrompt := prompt
	const maxSteerFollowups = 16
	for i := 0; ; i++ {
		var text string
		var err error
		squad, text, err = runTurn(ctx, cfg, turnPrompt, true /*showTrace*/, squad)
		if cfg.SteerStore != nil {
			cfg.SteerStore.TakeConsumed(cfg.SessionID) // clear; the CLI keeps no transcript to fold into
		}
		if err != nil {
			return squad, err
		}
		if ctx.Err() != nil {
			break
		}
		// 1) Steering the model never reached becomes the next turn.
		if cfg.SteerStore != nil {
			if pending := cfg.SteerStore.TakePending(cfg.SessionID); len(pending) > 0 {
				if i >= maxSteerFollowups {
					break
				}
				turnPrompt = strings.Join(pending, "\n")
				fmt.Fprintf(cfg.Stdout, "\n> %s\n", turnPrompt)
				continue
			}
		}
		// 2) /goal: keep working until the evaluator says the condition holds (or
		//    a hard turn cap / eval failure stops the autonomous loop). Requires a
		//    Manager (the evaluator resolves the session's model via it).
		if cfg.GoalStore != nil && cfg.Manager != nil {
			if g, ok := cfg.GoalStore.Get(cfg.SessionID); ok && g.Active() {
				if cfg.GoalStore.CapReached(cfg.SessionID) {
					fmt.Fprintf(cfg.Stdout, "\x1b[2m◎ goal: reached the maximum number of turns — clear it with /goal clear\x1b[0m\n")
					break
				}
				evalCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				transcript := strings.TrimSpace(turnPrompt + "\n\n[assistant]\n" + text)
				met, reason, evalOK := cfg.Manager.EvaluateGoal(evalCtx, cfg.SessionID, g.Condition, transcript)
				cancel()
				if !evalOK {
					fmt.Fprintf(cfg.Stdout, "\x1b[2m◎ goal: could not evaluate the condition — clear it with /goal clear\x1b[0m\n")
					break
				}
				if met {
					cfg.GoalStore.MarkAchieved(cfg.SessionID, reason)
					fmt.Fprintf(cfg.Stdout, "\x1b[32m◎ goal achieved\x1b[0m %s\n", reason)
					break
				}
				turns := cfg.GoalStore.RecordTurn(cfg.SessionID, reason, 0)
				fmt.Fprintf(cfg.Stdout, "\x1b[2m◎ goal (turn %d/%d): %s\x1b[0m\n", turns, goal.MaxTurns(), reason)
				turnPrompt = goal.Directive(g.Condition, reason)
				continue
			}
		}
		break
	}
	return squad, nil
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
func runTurn(ctx context.Context, cfg Config, prompt string, showTrace bool, startSquad string) (squadOut string, assistantText string, err error) {
	// Tag the run with the session id so mid-turn steering reaches sub-agents
	// (which run under an ephemeral agenttool session id).
	ctx = toolkitagent.WithSteerSession(ctx, cfg.SessionID)
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
		return startSquad, text, err
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
	return finalSquad, text, err
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
		fmt.Fprintln(cfg.Stdout, "  /loop <spec> <prompt>   Re-run a prompt in this session on a timer (/loop stop, /loop list)")
		fmt.Fprintln(cfg.Stdout, "  /schedule <spec> <prompt>  Durable routine (/schedule list|remove <id>|run <id>)")
		fmt.Fprintln(cfg.Stdout, "  /goal <condition>   Keep working until a condition is met (/goal shows status, /goal clear stops)")
		fmt.Fprintln(cfg.Stdout, "  #<text>             Append a one-line memory to the project AGENT.md")
		fmt.Fprintln(cfg.Stdout, "Spec: \"30m\", \"every 2h\", \"in 90m\", \"at 09:00\", or a cron expr like \"0 9 * * 1-5\".")
		fmt.Fprintln(cfg.Stdout, "Tips:")
		fmt.Fprintln(cfg.Stdout, "  Ctrl-C cancels an in-flight turn; Ctrl-D exits the REPL.")
		return false
	case "loop", "schedule":
		rest := strings.TrimSpace(line[len(fields[0]):])
		handleSchedulerSlash(cfg, cmd, rest)
		return false
	default:
		fmt.Fprintf(cfg.Stderr, "Unknown command: /%s (try /help)\n", cmd)
		return false
	}
}

// handleSchedulerSlash implements /loop and /schedule in the REPL: create,
// list, and remove jobs on the shared scheduler. Jobs fire into the current
// session (see the `fired` case in runRepl).
func handleSchedulerSlash(cfg Config, cmd, rest string) {
	if cfg.Scheduler == nil {
		fmt.Fprintln(cfg.Stderr, "scheduling is unavailable in this mode")
		return
	}
	first := strings.ToLower(strings.Fields(rest + " ")[0])

	// Subcommands and bare-list.
	switch {
	case cmd == "loop" && (rest == "" || first == "list"):
		printJobs(cfg, "loop", cfg.SessionID)
		return
	case cmd == "loop" && (first == "stop" || first == "off"):
		n := cfg.Scheduler.RemoveLoopsForSession(cfg.SessionID)
		fmt.Fprintf(cfg.Stdout, "stopped %d loop(s)\n", n)
		return
	case cmd == "schedule" && (rest == "" || first == "list"):
		printJobs(cfg, "", "")
		return
	case cmd == "schedule" && first == "remove":
		id := strings.TrimSpace(strings.TrimPrefix(rest, strings.Fields(rest)[0]))
		if cfg.Scheduler.Remove(id) {
			fmt.Fprintf(cfg.Stdout, "removed %s\n", id)
		} else {
			fmt.Fprintf(cfg.Stderr, "no schedule %q\n", id)
		}
		return
	case cmd == "schedule" && first == "run":
		id := strings.TrimSpace(strings.TrimPrefix(rest, strings.Fields(rest)[0]))
		if cfg.Scheduler.RunNow(id) {
			fmt.Fprintf(cfg.Stdout, "running %s now\n", id)
		} else {
			fmt.Fprintf(cfg.Stderr, "no schedule %q (or already running)\n", id)
		}
		return
	}

	spec, prompt := scheduler.SplitSpecPrompt(rest)
	if spec == "" || prompt == "" {
		fmt.Fprintf(cfg.Stderr, "usage: /%s <spec> <prompt>  (spec: 30m, \"in 90m\", \"at 09:00\", \"0 9 * * 1-5\")\n", cmd)
		return
	}
	parsed, err := scheduler.ParseSpec(spec, time.Now())
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "%v\n", err)
		return
	}
	kind := scheduler.KindSchedule
	sid := ""
	if cmd == "loop" {
		kind = scheduler.KindLoop
		sid = cfg.SessionID
	}
	job, err := cfg.Scheduler.Add(scheduler.Job{
		Kind: kind, Prompt: prompt, Spec: spec,
		Interval: parsed.Interval, Cron: parsed.Cron, At: parsed.At,
		SessionID: sid, UserID: cfg.UserID,
	})
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "%v\n", err)
		return
	}
	fmt.Fprintf(cfg.Stdout, "%s scheduled (id=%s, next %s)\n", cmd, job.ID, job.NextRun.Format("15:04:05"))
}

// printJobs lists scheduler jobs, optionally filtered by kind and session.
func printJobs(cfg Config, kind, sessionID string) {
	jobs := cfg.Scheduler.List()
	n := 0
	for _, j := range jobs {
		if kind != "" && j.Kind != kind {
			continue
		}
		if sessionID != "" && j.SessionID != sessionID {
			continue
		}
		state := "on"
		if !j.Enabled {
			state = "off"
		}
		fmt.Fprintf(cfg.Stdout, "  %s  %-8s %-14s [%s] next %s — %s\n",
			j.ID, j.Kind, j.Spec, state, j.NextRun.Format("15:04:05"), scheduler.FirstLine(j.Prompt))
		n++
	}
	if n == 0 {
		fmt.Fprintln(cfg.Stdout, "  (none)")
	}
}
