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
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/blouargant/agent-toolkit/core/events"
)

// Config bundles everything the TUI needs to run.
type Config struct {
	Runner    *runner.Runner
	Bus       *events.Bus // optional; if non-nil, trace pane subscribes
	UserID    string
	SessionID string
	AppName   string // shown in title bar
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

	// ── Right pane: chat history + input ────────────────────────────────
	chat := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetWordWrap(true)
	chat.SetBorder(true).SetTitle(" Chat ").SetTitleAlign(tview.AlignLeft)
	chat.SetChangedFunc(func() { app.Draw() })

	input := tview.NewInputField().
		SetLabel(" > ").
		SetFieldBackgroundColor(tcell.ColorDefault)
	input.SetBorder(true).SetTitle(" Type a message — Enter to send, Ctrl-C to quit ").
		SetTitleAlign(tview.AlignLeft)

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
	status.SetText(fmt.Sprintf(" [::b]%s[-]   session: [yellow]%s[-]   user: [yellow]%s[-]",
		cfg.AppName, cfg.SessionID, cfg.UserID))

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
			appendTrace("[aqua]→ tool[-] %v", p["tool"])
		})
		cfg.Bus.On(events.EventAfterTool, func(_ string, p map[string]any) {
			appendTrace("[green]✓ tool[-] %v  [gray](%v)[-]", p["tool"], p["duration"])
		})
		cfg.Bus.On(events.EventToolError, func(_ string, p map[string]any) {
			appendTrace("[red]✗ tool[-] %v: %v", p["tool"], p["error"])
		})
		cfg.Bus.On(events.EventBeforeModel, func(_ string, _ map[string]any) {
			appendTrace("[blue]→ model[-]")
		})
		cfg.Bus.On(events.EventAfterModel, func(_ string, _ map[string]any) {
			appendTrace("[blue]✓ model[-]")
		})
		cfg.Bus.On(events.EventSessionStart, func(_ string, _ map[string]any) {
			appendTrace("[yellow]session start[-]")
		})
		cfg.Bus.On(events.EventSessionEnd, func(_ string, _ map[string]any) {
			appendTrace("[yellow]session end[-]")
		})
	}

	// busy flag prevents overlapping submissions.
	busy := false

	send := func(prompt string) {
		if busy || strings.TrimSpace(prompt) == "" {
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
			appendChat("\n[::b]you[-]\n%s\n", prompt)
			appendChat("\n[::b]assistant[-]\n")
			seq := cfg.Runner.Run(ctx, cfg.UserID, cfg.SessionID,
				&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
				adkagent.RunConfig{})
			for ev, err := range seq {
				if err != nil {
					appendChat("\n[red]error: %v[-]\n", err)
					return
				}
				if ev == nil || ev.Content == nil {
					continue
				}
				renderEvent(ev, appendChat)
			}
			appendChat("\n")
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

	return app.SetRoot(root, true).EnableMouse(true).Run()
}

// renderEvent extracts text / tool-call / tool-response parts from a
// session event and writes them to the chat pane via append.
func renderEvent(ev *session.Event, append func(string, ...any)) {
	for _, p := range ev.Content.Parts {
		if p == nil {
			continue
		}
		if p.Text != "" {
			append("%s", p.Text)
		}
		if p.FunctionCall != nil {
			append("\n[aqua]⚙ %s[-] %s\n", p.FunctionCall.Name, shortArgs(p.FunctionCall.Args))
		}
		if p.FunctionResponse != nil {
			append("[gray]↳ %s[-]\n", p.FunctionResponse.Name)
		}
	}
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
