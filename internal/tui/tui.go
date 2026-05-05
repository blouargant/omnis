// Package tui implements a tview-based chat interface for the agent
// toolkit. The layout mirrors the ADK web UI:
//
//	┌──────────────┬───────────────────────────────────┐
//	│  Trace       │  Chat history                     │
//	│  (events,    │  (user / assistant turns,         │
//	│   tool calls)│   tool calls inline)              │
//	│              ├───────────────────────────────────┤
//	│              │  Input box (press Enter to send)  │
//	└──────────────┴───────────────────────────────────┘
//
// It plugs into the existing core/events bus for the trace pane and
// drives an *runner.Runner directly for the chat pane. Press Ctrl-C or
// Esc to quit.
package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"

	toolkitagent "github.com/blouargant/agent-toolkit/agent"
	"github.com/blouargant/agent-toolkit/core/events"
)

var oscColorResponseRE = regexp.MustCompile(`(?:^|\s)(?:10|11);rgb:[0-9A-Fa-f]+/[0-9A-Fa-f]+/[0-9A-Fa-f]+(?:\s|$)`)
var oscColorResponseAtStartRE = regexp.MustCompile(`^(?:10|11);rgb:[0-9A-Fa-f]+/[0-9A-Fa-f]+/[0-9A-Fa-f]+\s*`)

// stripTerminalControlSequences removes ANSI/OSC/DCS control sequences and
// non-printable C0 controls from terminal text while keeping line breaks.
func stripTerminalControlSequences(s string) string {
	if s == "" {
		return ""
	}
	b := []byte(s)
	var out strings.Builder
	out.Grow(len(b))

	for i := 0; i < len(b); {
		c := b[i]
		if c == 0x1b { // ESC
			if i+1 >= len(b) {
				break
			}
			n := b[i+1]
			switch n {
			case '[': // CSI ... final-byte
				i += 2
				for i < len(b) {
					if b[i] >= 0x40 && b[i] <= 0x7e {
						i++
						break
					}
					i++
				}
			case ']': // OSC ... BEL or ST
				i += 2
				for i < len(b) {
					if b[i] == 0x07 {
						i++
						break
					}
					if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			case 'P', 'X', '^', '_': // DCS/SOS/PM/APC ... ST
				i += 2
				for i < len(b) {
					if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			default:
				// Short ESC sequence (e.g. ESC c), drop both bytes.
				i += 2
			}
			continue
		}

		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			i++
			continue
		}
		out.WriteByte(c)
		i++
	}

	return out.String()
}

// sanitizeInputText strips terminal control sequences and known OSC color
// response artifacts that can leak into the input line on some terminals.
func sanitizeInputText(s string) string {
	s = stripTerminalControlSequences(s)
	s = oscColorResponseAtStartRE.ReplaceAllString(s, "")
	return oscColorResponseRE.ReplaceAllString(s, " ")
}

// Config bundles everything the TUI needs to run.
type Config struct {
	Runner                     *runner.Runner
	Bus                        *events.Bus // optional; if non-nil, trace pane subscribes
	UserID                     string
	SessionID                  string
	AppName                    string // shown in title bar
	InputTokenPricePerMillion  float64
	OutputTokenPricePerMillion float64
}

// Run starts the TUI event loop and blocks until the user quits or ctx
// is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Runner == nil {
		return fmt.Errorf("tui: Runner required")
	}
	if cfg.UserID == "" {
		cfg.UserID = "user"
	}
	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("tui-%d", time.Now().Unix())
	}
	if cfg.AppName == "" {
		cfg.AppName = "agent-toolkit"
	}

	app := tview.NewApplication()

	// Markdown renderer (lazy init: width depends on terminal). Falls
	// back to raw text if glamour fails.
	var (
		mdMu     sync.Mutex
		mdWidth  int
		renderer *glamour.TermRenderer
	)
	getRenderer := func(w int) *glamour.TermRenderer {
		mdMu.Lock()
		defer mdMu.Unlock()
		if w < 20 {
			w = 80
		}
		if renderer != nil && mdWidth == w {
			return renderer
		}
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(w),
		)
		if err != nil {
			return nil
		}
		renderer = r
		mdWidth = w
		return renderer
	}
	renderMarkdown := func(md string, width int) string {
		md = strings.TrimSpace(stripTerminalControlSequences(md))
		if md == "" {
			return ""
		}
		r := getRenderer(width)
		if r == nil {
			return md + "\n"
		}
		out, err := r.Render(md)
		if err != nil {
			return md + "\n"
		}
		return out
	}

	// ── Right pane: chat history + input ────────────────────────────────
	chat := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetWordWrap(true)
	chat.SetBorder(true).SetTitle(" Chat ").SetTitleAlign(tview.AlignLeft)
	chat.SetChangedFunc(func() { app.Draw() })
	// ANSIWriter translates glamour's ANSI escape sequences into tview
	// color tags so styled markdown actually renders inside the TextView.
	chatANSI := tview.ANSIWriter(chat)

	input := tview.NewInputField().
		SetLabel(" > ").
		SetFieldBackgroundColor(tcell.ColorDefault)
	input.SetBorder(true).SetTitle(" Type a message — Enter to send, Ctrl-C to quit ").
		SetTitleAlign(tview.AlignLeft)
	cleaningInput := false
	input.SetChangedFunc(func(text string) {
		if cleaningInput {
			return
		}
		cleaned := sanitizeInputText(text)
		if cleaned == text {
			return
		}
		cleaningInput = true
		input.SetText(cleaned)
		cleaningInput = false
	})

	// Slash-command autocomplete: suggest matching commands when the
	// user starts typing "/".
	slashCommands := []string{"/help", "/learn", "/learn-now", "/learn-now ", "/learn ", "/status"}
	slashCommandsDisplay := []string{
		"/help",
		"/learn",
		"/learn-now",
		"/learn-now <reason>",
		"/learn <reason>",
		"/status",
	}
	input.SetAutocompleteFunc(func(currentText string) []string {
		trimmed := strings.TrimLeft(currentText, " ")
		if !strings.HasPrefix(trimmed, "/") {
			return nil
		}
		var matches []string
		for _, label := range slashCommandsDisplay {
			if strings.HasPrefix(label, trimmed) {
				matches = append(matches, label)
			}
		}
		return matches
	})
	input.SetAutocompletedFunc(func(text string, index int, source int) bool {
		if source == tview.AutocompletedNavigate {
			return false // keep dropdown open while navigating
		}
		// Find the corresponding raw command (without the display hint).
		raw := text
		for i, label := range slashCommandsDisplay {
			if label == text && i < len(slashCommands) {
				raw = slashCommands[i]
				break
			}
		}
		input.SetText(raw)
		return true // close dropdown
	})

	rightPane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(chat, 0, 1, false).
		AddItem(input, 3, 0, true)

	// ── Left pane: trace ────────────────────────────────────────────────
	trace := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true)
	trace.SetBorder(true).SetTitle(" Trace ").SetTitleAlign(tview.AlignLeft)
	trace.SetChangedFunc(func() { app.Draw() })

	// ── Status bar ──────────────────────────────────────────────────────
	status := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	var inputTokensTotal atomic.Int64
	var outputTokensTotal atomic.Int64
	setStatus := func() {
		status.SetText(buildStatusText(cfg, inputTokensTotal.Load(), outputTokensTotal.Load()))
	}
	setStatus()

	// ── Root layout ─────────────────────────────────────────────────────
	main := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(trace, 36, 0, false).
		AddItem(rightPane, 0, 1, true)

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(status, 1, 0, false).
		AddItem(main, 0, 1, true)

	// Helpers: thread-safe UI updates.
	appendChat := func(format string, args ...any) {
		text := fmt.Sprintf(format, args...)
		app.QueueUpdateDraw(func() {
			fmt.Fprint(chat, text)
			chat.ScrollToEnd()
		})
	}
	appendTrace := func(format string, args ...any) {
		ts := time.Now().Format("15:04:05")
		text := fmt.Sprintf("[gray]%s[-] %s\n", ts, fmt.Sprintf(format, args...))
		app.QueueUpdateDraw(func() {
			fmt.Fprint(trace, text)
			trace.ScrollToEnd()
		})
	}

	// ── Wire event bus → trace pane ─────────────────────────────────────
	if cfg.Bus != nil {
		cfg.Bus.On(events.EventBeforeTool, func(_ string, p map[string]any) {
			if p["tool"] == "load_skill" {
				skillName := ""
				if input, ok := p["input"].(map[string]any); ok {
					if n, ok := input["name"].(string); ok {
						skillName = n
					}
				}
				if skillName != "" {
					appendTrace("[magenta]★ skill[-] [::b]%s[-]", skillName)
					return
				}
			}
			appendTrace("[aqua]→ tool[-] %v", p["tool"])
		})
		cfg.Bus.On(events.EventAfterTool, func(_ string, p map[string]any) {
			appendTrace("[green]✓ tool[-] %v  [gray](%v)[-]", p["tool"], p["duration"])
		})
		cfg.Bus.On(events.EventToolError, func(_ string, p map[string]any) {
			appendTrace("[red]✗ tool[-] %v: %v", p["tool"], p["error"])
		})
		var modelCallCount int
		var modelCallMu sync.Mutex
		cfg.Bus.On(events.EventBeforeModel, func(_ string, p map[string]any) {
			modelCallMu.Lock()
			modelCallCount++
			n := modelCallCount
			modelCallMu.Unlock()
			modelName, _ := p["model"].(string)
			if modelName == "" {
				modelName = "model"
			}
			appendTrace("[blue]→ %s[-] [gray](#%d)[-]", modelName, n)
		})
		cfg.Bus.On(events.EventAfterModel, func(_ string, p map[string]any) {
			if resp, ok := p["response"].(*model.LLMResponse); ok && resp != nil && resp.UsageMetadata != nil {
				inputTokensTotal.Add(int64(resp.UsageMetadata.PromptTokenCount))
				outputTokensTotal.Add(int64(resp.UsageMetadata.CandidatesTokenCount))
				app.QueueUpdateDraw(setStatus)
			}
			appendTrace("[blue]✓ model[-]")
		})
		cfg.Bus.On(events.EventRunStart, func(_ string, _ map[string]any) {
			modelCallMu.Lock()
			modelCallCount = 0
			modelCallMu.Unlock()
			appendTrace("[yellow]run start[-]")
		})
		cfg.Bus.On(events.EventRunEnd, func(_ string, _ map[string]any) {
			appendTrace("[yellow]run end[-]")
		})
		cfg.Bus.On(events.EventCurateNow, func(_ string, _ map[string]any) {
			appendTrace("[yellow]curate now[-]")
		})
		cfg.Bus.On(events.EventSessionStart, func(_ string, _ map[string]any) {
			// Reset cumulative token counters at the start of a real
			// session. We deliberately DO NOT call QueueUpdateDraw here:
			// EventSessionStart is emitted before app.Run() begins
			// draining its update queue, so a draw call would deadlock
			// the TUI (counters are already zero on launch anyway).
			inputTokensTotal.Store(0)
			outputTokensTotal.Store(0)
			appendTrace("[yellow]session start[-]")
		})
		cfg.Bus.On(events.EventSessionEnd, func(_ string, _ map[string]any) {
			appendTrace("[yellow]session end[-]")
		})
	}

	// chatWidth returns the current inner width of the chat pane, used
	// to constrain markdown word-wrapping. Safe to call from any
	// goroutine — GetInnerRect just reads cached dimensions.
	chatWidth := func() int {
		_, _, w, _ := chat.GetInnerRect()
		return w
	}

	// flushMarkdown renders `buf` (markdown source) into the chat pane
	// via glamour + ANSIWriter, then clears the buffer.
	flushMarkdown := func(buf *strings.Builder) {
		if buf.Len() == 0 {
			return
		}
		text := buf.String()
		buf.Reset()
		w := chatWidth()
		out := renderMarkdown(text, w)
		app.QueueUpdateDraw(func() {
			fmt.Fprint(chatANSI, out)
			chat.ScrollToEnd()
		})
	}

	// busy flag prevents overlapping submissions.
	busy := false
	handleShortcut := func(raw string) {
		line := strings.TrimSpace(raw)
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return
		}
		cmd := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
		switch cmd {
		case "learn":
			reason := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			if reason == "" {
				reason = "manual /learn request from TUI"
			}
			appendChat("\n[::b]assistant[-]\n")
			msg, err := toolkitagent.RequestCurateSession(cfg.UserID, cfg.SessionID, reason)
			if err != nil {
				appendChat("[red]/learn failed:[-] %v\n", err)
				return
			}
			appendChat("[green]%s[-]\n", msg)
			appendChat("[gray]Tip:[-] curation runs on session end. Exit TUI with Ctrl-C when ready.\n")
		case "learn-now":
			reason := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			if reason == "" {
				reason = "manual /learn-now request from TUI"
			}
			appendChat("\n[::b]assistant[-]\n")
			msg, err := toolkitagent.RequestCurateSession(cfg.UserID, cfg.SessionID, reason)
			if err != nil {
				appendChat("[red]/learn-now failed:[-] %v\n", err)
				return
			}
			if cfg.Bus == nil {
				appendChat("[green]%s[-]\n", msg)
				appendChat("[yellow]Immediate curation unavailable:[-] event bus not configured.\n")
				return
			}
			go cfg.Bus.Emit(events.EventCurateNow, map[string]any{
				"user_id":    cfg.UserID,
				"session_id": cfg.SessionID,
			})
			appendChat("[green]%s[-]\n", msg)
			appendChat("[green]Triggered curation now.[-] Check trace/logs for curator completion.\n")
		case "status":
			requested := toolkitagent.CurateSessionRequestedByIDs(cfg.UserID, cfg.SessionID)
			appendChat("\n[::b]assistant[-]\n")
			appendChat("Session status:\n")
			appendChat("  app: [aqua]%s[-]\n", cfg.AppName)
			appendChat("  user: [aqua]%s[-]\n", cfg.UserID)
			appendChat("  session: [aqua]%s[-]\n", cfg.SessionID)
			appendChat("  curation_requested: [aqua]%t[-]\n", requested)
			appendChat("  event_bus_configured: [aqua]%t[-]\n", cfg.Bus != nil)
		case "help":
			appendChat("\n[::b]assistant[-]\n")
			appendChat("Available shortcuts:\n")
			appendChat("  [aqua]/learn [reason][-]      Mark this session for soft-skill curation.\n")
			appendChat("  [aqua]/learn-now [reason][-]  Mark and trigger soft-skill curation immediately.\n")
			appendChat("  [aqua]/status[-]              Show current session and curation status.\n")
			appendChat("  [aqua]/help[-]                Show this help.\n")
		default:
			appendChat("\n[::b]assistant[-]\n")
			appendChat("[yellow]Unknown shortcut:[-] /%s (try /help)\n", cmd)
		}
	}

	send := func(prompt string) {
		prompt = sanitizeInputText(prompt)
		if strings.TrimSpace(prompt) == "" {
			return
		}
		// Slash-command detection runs on the event-loop goroutine (it's
		// just a string prefix check). The body of the shortcut MUST run
		// in its own goroutine: appendChat → QueueUpdateDraw spawns a
		// goroutine that calls Draw(), which tries to acquire the tview
		// application mutex — but tview holds that mutex while dispatching
		// key events, so calling QueueUpdateDraw synchronously here
		// deadlocks the UI.
		if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
			input.SetText("")
			go handleShortcut(prompt)
			return
		}
		if busy {
			return
		}
		busy = true
		input.SetText("")
		// All UI mutations happen off the main goroutine — calling
		// QueueUpdateDraw here (the Enter handler runs ON the main
		// goroutine) would deadlock the app.
		go func() {
			defer func() {
				busy = false
				app.QueueUpdateDraw(func() { app.SetFocus(input) })
			}()

			// Render the user turn as markdown too (so code blocks /
			// lists in the question look right).
			userMD := fmt.Sprintf("**you**\n\n%s", sanitizeInputText(prompt))
			appendChat("\n")
			app.QueueUpdateDraw(func() {
				fmt.Fprint(chatANSI, renderMarkdown(userMD, chatWidth()))
			})
			appendChat("[::b]assistant[-]\n")
			turnInputStart := inputTokensTotal.Load()
			turnOutputStart := outputTokensTotal.Load()
			defer func() {
				turnInput := inputTokensTotal.Load() - turnInputStart
				turnOutput := outputTokensTotal.Load() - turnOutputStart
				if turnInput < 0 {
					turnInput = 0
				}
				if turnOutput < 0 {
					turnOutput = 0
				}
				appendChat("%s\n", buildTurnUsageText(cfg, turnInput, turnOutput))
			}()

			seq := cfg.Runner.Run(ctx, cfg.UserID, cfg.SessionID,
				&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
				adkagent.RunConfig{})

			// Buffer assistant text per turn; flush as rendered markdown
			// either when a non-text part arrives (tool call/response)
			// or when the stream completes.
			var mdBuf strings.Builder
			for ev, err := range seq {
				if err != nil {
					flushMarkdown(&mdBuf)
					appendChat("\n[red]error: %v[-]\n", err)
					return
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
						mdBuf.WriteString(stripTerminalControlSequences(p.Text))
					case p.FunctionCall != nil:
						flushMarkdown(&mdBuf)
						appendChat("[aqua]⚙ %s[-] %s\n",
							p.FunctionCall.Name, shortArgs(p.FunctionCall.Args))
					case p.FunctionResponse != nil:
						flushMarkdown(&mdBuf)
						appendChat("[gray]↳ %s[-]\n", p.FunctionResponse.Name)
					}
				}
			}
			flushMarkdown(&mdBuf)
		}()
	}

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			send(input.GetText())
		}
	})

	// Global key bindings.
	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlC, tcell.KeyEsc:
			app.Stop()
			return nil
		case tcell.KeyCtrlL:
			chat.Clear()
			return nil
		}
		return ev
	})

	// Cancel handling: stop the app when ctx is cancelled.
	go func() {
		<-ctx.Done()
		app.Stop()
	}()

	// Welcome banner. Write directly: app.Run() hasn't started yet, so
	// QueueUpdateDraw would deadlock (its queue is only drained by Run).
	fmt.Fprintf(chat, "[gray]Welcome to %s. Type a message and press Enter.\nCtrl-L clears the chat. Ctrl-C / Esc to quit.[-]\n", cfg.AppName)

	// Emit real session lifecycle events around the TUI run. These are
	// distinct from the per-turn EventRunStart/EventRunEnd: subscribers
	// like the soft-skills curator should fire ONCE here, not on every
	// user turn.
	//
	// EventSessionStart is fired from a goroutine so its handlers (which
	// call appendTrace → QueueUpdateDraw) execute once app.Run() is
	// draining the update queue. Emitting it inline before app.Run()
	// would deadlock.
	if cfg.Bus != nil {
		go cfg.Bus.Emit(events.EventSessionStart, map[string]any{
			"user_id":    cfg.UserID,
			"session_id": cfg.SessionID,
		})
		defer cfg.Bus.Emit(events.EventSessionEnd, map[string]any{
			"user_id":    cfg.UserID,
			"session_id": cfg.SessionID,
		})
	}

	// Keep terminal mouse reporting disabled so users can use native
	// mouse selection in the chat pane and paste into the input field.
	return app.SetRoot(root, true).Run()
}

// shortArgs renders tool args compactly for the inline chat display.
func shortArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for k, v := range args {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, s))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func buildStatusText(cfg Config, inputTokens, outputTokens int64) string {
	text := fmt.Sprintf(" [::b]%s[-]   session: [yellow]%s[-]   user: [yellow]%s[-]   tokens in/out: [yellow]%d[-]/[yellow]%d[-]",
		cfg.AppName, cfg.SessionID, cfg.UserID, inputTokens, outputTokens)
	if dollars, ok := totalCostDollars(inputTokens, outputTokens, cfg.InputTokenPricePerMillion, cfg.OutputTokenPricePerMillion); ok {
		text += fmt.Sprintf("   total: [green]$%.6f[-]", dollars)
	}
	return text
}

func buildTurnUsageText(cfg Config, inputTokens, outputTokens int64) string {
	totalTokens := inputTokens + outputTokens
	text := fmt.Sprintf("[gray]turn usage[-] in/out/total: [yellow]%d[-]/[yellow]%d[-]/[yellow]%d[-]",
		inputTokens, outputTokens, totalTokens)
	if dollars, ok := totalCostDollars(inputTokens, outputTokens, cfg.InputTokenPricePerMillion, cfg.OutputTokenPricePerMillion); ok {
		text += fmt.Sprintf("   [green]$%.6f[-]", dollars)
	}
	return text
}

func totalCostDollars(inputTokens, outputTokens int64, inputPricePerMillion, outputPricePerMillion float64) (float64, bool) {
	if inputPricePerMillion <= 0 || outputPricePerMillion <= 0 {
		return 0, false
	}
	inputCost := float64(inputTokens) * inputPricePerMillion / 1_000_000
	outputCost := float64(outputTokens) * outputPricePerMillion / 1_000_000
	return inputCost + outputCost, true
}
