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
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
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
		md = strings.TrimSpace(md)
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
		cfg.Bus.On(events.EventAfterModel, func(_ string, _ map[string]any) {
			appendTrace("[blue]✓ model[-]")
		})
		cfg.Bus.On(events.EventSessionStart, func(_ string, _ map[string]any) {
			modelCallMu.Lock()
			modelCallCount = 0
			modelCallMu.Unlock()
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

			// Render the user turn as markdown too (so code blocks /
			// lists in the question look right).
			userMD := fmt.Sprintf("**you**\n\n%s", prompt)
			appendChat("\n")
			app.QueueUpdateDraw(func() {
				fmt.Fprint(chatANSI, renderMarkdown(userMD, chatWidth()))
			})
			appendChat("[::b]assistant[-]\n")

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
						mdBuf.WriteString(p.Text)
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

	return app.SetRoot(root, true).EnableMouse(true).Run()
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
