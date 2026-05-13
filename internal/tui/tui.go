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
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	"github.com/blouargant/agent-toolkit/core/llm"
	"github.com/blouargant/agent-toolkit/internal/askuser"
)

var oscColorResponseRE = regexp.MustCompile(`(?:^|\s)(?:1|10|11);rgb:[0-9A-Fa-f]+/[0-9A-Fa-f]+/[0-9A-Fa-f]+(?:\s|$)`)
var oscColorResponseAtStartRE = regexp.MustCompile(`^(?:1|10|11);rgb:[0-9A-Fa-f]+/[0-9A-Fa-f]+/[0-9A-Fa-f]+\s*`)

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

// markdownRenderer caches Glamour renderers by width. The chat TextView keeps
// basic wrapping enabled as a display guard, but word wrapping belongs here so
// Markdown tables and headings are wrapped before ANSI styling reaches tview.
type markdownRenderer struct {
	mu       sync.Mutex
	width    int
	renderer *glamour.TermRenderer
}

func (r *markdownRenderer) rendererFor(width int) *glamour.TermRenderer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if width < 20 {
		width = 80
	}
	if r.renderer != nil && r.width == width {
		return r.renderer
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	r.renderer = renderer
	r.width = width
	return renderer
}

func (r *markdownRenderer) render(markdown string, width int) string {
	markdown = strings.TrimSpace(stripTerminalControlSequences(markdown))
	if markdown == "" {
		return ""
	}
	renderer := r.rendererFor(width)
	if renderer == nil {
		return markdown + "\n"
	}
	out, err := renderer.Render(markdown)
	if err != nil {
		return markdown + "\n"
	}
	return out
}

func newChatTextView(changed func()) *tview.TextView {
	chat := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetWordWrap(false)
	chat.SetBorder(true).SetTitle(" Chat ").SetTitleAlign(tview.AlignLeft)
	if changed != nil {
		chat.SetChangedFunc(changed)
	}
	return chat
}

// Config bundles everything the TUI needs to run.
type Config struct {
	Runner                            *runner.Runner
	Bus                               *events.Bus // optional; if non-nil, trace pane subscribes
	AskUserRegistry                   *askuser.Registry // optional; if non-nil, renders ask_user modals
	UserID                            string
	SessionID                         string
	AppName                           string // shown in title bar
	SubAgentNames                     []string
	InputTokenPricePerMillion         float64
	OutputTokenPricePerMillion        float64
	// CachedInputTokenPricePerMillion is applied to prompt tokens served
	// from the provider's prompt cache. Defaults to
	// InputTokenPricePerMillion (i.e. no discount) when zero.
	CachedInputTokenPricePerMillion float64
	// CacheCreationTokenPricePerMillion is applied to prompt tokens that
	// populate the provider's prompt cache for the first time (Anthropic
	// cache_creation_input_tokens). Defaults to InputTokenPricePerMillion
	// when zero.
	CacheCreationTokenPricePerMillion float64
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
	subAgentSet := make(map[string]struct{}, len(cfg.SubAgentNames))
	for _, name := range cfg.SubAgentNames {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" {
			continue
		}
		subAgentSet[n] = struct{}{}
	}

	app := tview.NewApplication()
	markdown := &markdownRenderer{}

	// ── Right pane: chat history + input ────────────────────────────────
	chat := newChatTextView(func() { app.Draw() })
	// ANSIWriter translates glamour's ANSI escape sequences into tview
	// color tags so styled markdown actually renders inside the TextView.
	chatANSI := tview.ANSIWriter(chat)

	input := tview.NewInputField().
		SetLabel(" > ").
		SetFieldBackgroundColor(tcell.ColorDefault)
	input.SetBorder(true).SetTitle(" Type a message — Enter to send, Ctrl-C to quit ").
		SetTitleAlign(tview.AlignLeft)
	// Reject non-printable characters at the field level so control sequences
	// injected by the terminal (e.g. OSC color responses) never enter the
	// buffer and corrupt the cursor/offset state. Residual multi-char sequences
	// that slip through are stripped by sanitizeInputText() inside send().
	input.SetAcceptanceFunc(func(_ string, lastChar rune) bool {
		return lastChar >= 0x20
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

	// Focus cycling: Tab / Shift-Tab rotates focus among input → chat → trace
	// so the user can scroll either panel with arrow keys / Page Up / Page Down.
	// Esc returns focus to the input field; Ctrl-C always quits.
	focusList := []tview.Primitive{input, chat, trace}
	focusIdx := 0
	setFocus := func(idx int) {
		focusIdx = idx
		chat.SetBorderColor(tcell.ColorDefault)
		trace.SetBorderColor(tcell.ColorDefault)
		switch idx {
		case 1: // chat
			chat.SetBorderColor(tcell.ColorYellow)
		case 2: // trace
			trace.SetBorderColor(tcell.ColorYellow)
		}
		app.SetFocus(focusList[idx])
	}

	// ── Status bar ──────────────────────────────────────────────────────
	status := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	var appRunning atomic.Bool
	var inputTokensTotal atomic.Int64
	var cachedInputTokensTotal atomic.Int64
	var cacheCreationTokensTotal atomic.Int64
	var outputTokensTotal atomic.Int64
	setStatus := func() {
		status.SetText(buildStatusText(cfg,
			inputTokensTotal.Load(),
			cachedInputTokensTotal.Load(),
			cacheCreationTokensTotal.Load(),
			outputTokensTotal.Load()))
	}
	setStatus()

	// ── Root layout ─────────────────────────────────────────────────────
	main := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(trace, 36, 0, false).
		AddItem(rightPane, 0, 1, true)

	baseFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(status, 1, 0, false).
		AddItem(main, 0, 1, true)

	// Pages wraps baseFlex and lets us overlay ask_user modals.
	pages := tview.NewPages().AddPage("main", baseFlex, true, true)
	root := pages

	// Helpers: thread-safe UI updates.
	// Guard: QueueUpdateDraw in tview v0.42+ is synchronous — it blocks the
	// caller until the event loop executes the closure. After app.Run()
	// returns the event loop is gone, so any call would block forever.
	// appRunning is set to false before the deferred Bus.Emit(SessionEnd)
	// fires, so handlers that call appendChat/appendTrace bail out early.
	appendChat := func(format string, args ...any) {
		if !appRunning.Load() {
			return
		}
		text := fmt.Sprintf(format, args...)
		app.QueueUpdateDraw(func() {
			fmt.Fprint(chat, text)
			if app.GetFocus() != chat {
				chat.ScrollToEnd()
			}
		})
	}
	appendTrace := func(format string, args ...any) {
		if !appRunning.Load() {
			return
		}
		ts := time.Now().Format("15:04:05")
		text := fmt.Sprintf("[gray]%s[-] %s\n", ts, fmt.Sprintf(format, args...))
		app.QueueUpdateDraw(func() {
			fmt.Fprint(trace, text)
			if app.GetFocus() != trace {
				trace.ScrollToEnd()
			}
		})
	}

	// subAgentLabel returns the sub-agent name from a payload "agent" field
	// when it matches a known sub-agent, else "". Lead-agent events thus stay
	// in their existing rendering path.
	subAgentLabel := func(p map[string]any) string {
		name, _ := p["agent"].(string)
		if name == "" {
			return ""
		}
		if _, ok := subAgentSet[strings.ToLower(strings.TrimSpace(name))]; ok {
			return name
		}
		return ""
	}

	// subAgentDepth tracks nested sub-agent tool calls for trace indentation.
	var subAgentDepth atomic.Int32
	traceIndent := func(name string) string {
		if name == "" {
			return ""
		}
		d := int(subAgentDepth.Load())
		if d < 1 {
			d = 1
		}
		return strings.Repeat("  ", d) + "[gray]└[-] "
	}

	// appendSubAgentChat renders a live sub-agent activity line into the
	// main Chat pane, indented under the parent with the existing yellow
	// │ prefix used by entry/exit banners and per-line text rendering.
	appendSubAgentChat := func(format string, args ...any) {
		appendChat("[yellow]│ %s[-]\n", fmt.Sprintf(format, args...))
	}

	// ── Wire event bus → trace pane + chat (sub-agent live steps) ───────
	if cfg.Bus != nil {
		cfg.Bus.On(events.EventBeforeTool, func(_ string, p map[string]any) {
			sub := subAgentLabel(p)
			if sub != "" {
				subAgentDepth.Add(1)
			}
			if p["tool"] == "load_skill" {
				skillName := ""
				if input, ok := p["input"].(map[string]any); ok {
					if n, ok := input["name"].(string); ok {
						skillName = n
					}
				}
				if skillName != "" {
					appendTrace("%s[magenta]★ skill[-] [::b]%s[-]", traceIndent(sub), skillName)
					if sub != "" {
						appendSubAgentChat("%s ★ skill [::b]%s[-]", sub, skillName)
					}
					return
				}
			}
			appendTrace("%s[aqua]→ tool[-] %v", traceIndent(sub), p["tool"])
			if sub != "" {
				args, _ := p["input"].(map[string]any)
				appendSubAgentChat("%s [aqua]⚙ %v[-] %s", sub, p["tool"], shortArgs(args))
			}
		})
		cfg.Bus.On(events.EventAfterTool, func(_ string, p map[string]any) {
			sub := subAgentLabel(p)
			appendTrace("%s[green]✓ tool[-] %v  [gray](%v)[-]", traceIndent(sub), p["tool"], p["duration"])
			if sub != "" {
				appendSubAgentChat("%s [green]✓ %v[-]  [gray](%v)[-]", sub, p["tool"], p["duration"])
				if d := subAgentDepth.Add(-1); d < 0 {
					subAgentDepth.Store(0)
				}
			}
		})
		cfg.Bus.On(events.EventToolError, func(_ string, p map[string]any) {
			sub := subAgentLabel(p)
			appendTrace("%s[red]✗ tool[-] %v: %v", traceIndent(sub), p["tool"], p["error"])
			if sub != "" {
				appendSubAgentChat("%s [red]✗ %v:[-] %v", sub, p["tool"], p["error"])
				if d := subAgentDepth.Add(-1); d < 0 {
					subAgentDepth.Store(0)
				}
			}
		})
		var modelCallCount int
		var modelCallMu sync.Mutex
		cfg.Bus.On(events.EventBeforeModel, func(_ string, p map[string]any) {
			modelCallMu.Lock()
			modelCallCount++
			n := modelCallCount
			modelCallMu.Unlock()
			sub := subAgentLabel(p)
			modelName, _ := p["model"].(string)
			if modelName == "" {
				modelName = "model"
			}
			appendTrace("%s[blue]→ %s[-] [gray](#%d)[-]", traceIndent(sub), modelName, n)
		})
		cfg.Bus.On(events.EventAfterModel, func(_ string, p map[string]any) {
			sub := subAgentLabel(p)
			var promptTok, candTok, cachedTok, cacheCreateTok int64
			if resp, ok := p["response"].(*model.LLMResponse); ok && resp != nil && resp.UsageMetadata != nil {
				u := resp.UsageMetadata
				promptTok = int64(u.PromptTokenCount)
				candTok = int64(u.CandidatesTokenCount)
				cachedTok = int64(u.CachedContentTokenCount)
				for _, d := range u.CacheTokensDetails {
					if d != nil && d.Modality == llm.CacheCreationModality {
						cacheCreateTok += int64(d.TokenCount)
					}
				}
				inputTokensTotal.Add(promptTok)
				cachedInputTokensTotal.Add(cachedTok)
				cacheCreationTokensTotal.Add(cacheCreateTok)
				outputTokensTotal.Add(candTok)
				app.QueueUpdateDraw(setStatus)
			}
			appendTrace("%s[blue]✓ model[-]", traceIndent(sub))
			if sub != "" {
				modelName, _ := p["model"].(string)
				if modelName == "" {
					modelName = "model"
				}
				dur, _ := p["duration"].(time.Duration)
				appendSubAgentChat("%s [blue]▸ %s[-]  [gray](%v, %d/%d tok)[-]",
					sub, modelName, dur, promptTok, candTok)
			}
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
			cachedInputTokensTotal.Store(0)
			cacheCreationTokensTotal.Store(0)
			outputTokensTotal.Store(0)
			appendTrace("[yellow]session start[-]")
		})
		cfg.Bus.On(events.EventSessionEnd, func(_ string, _ map[string]any) {
			appendTrace("[yellow]session end[-]")
		})
	}

	// ── ask_user modal ───────────────────────────────────────────────────
	// When the agent calls ask_user, we receive EventAskUser on the bus
	// and render an interactive modal. The user's answer is fed back to
	// the registry, which unblocks the tool call.
	if cfg.AskUserRegistry != nil {
		cfg.AskUserRegistry.SetNotify(func(q askuser.Question) {
			if !appRunning.Load() {
				return
			}
			// Only respond to questions for this TUI's session.
			if q.SessionID != "" && q.SessionID != cfg.SessionID {
				return
			}
			app.QueueUpdateDraw(func() {
				showAskUserModal(app, pages, input, cfg.AskUserRegistry, q)
			})
		})
	}

	// chatWidth returns the current inner width of the chat pane, used
	// to constrain markdown word-wrapping. Safe to call from any
	// goroutine — GetInnerRect just reads cached dimensions.
	chatWidth := func() int {
		_, _, w, _ := chat.GetInnerRect()
		return w
	}

	// flushMarkdown renders `buf` (markdown source) into the chat pane.
	// For normal assistant output it uses glamour + ANSIWriter; for
	// sub-agent spans it prints a per-line prefixed block so delegated
	// content is visually grouped.
	flushMarkdown := func(buf *strings.Builder, subAgentName string) {
		if buf.Len() == 0 {
			return
		}
		text := buf.String()
		buf.Reset()
		if subAgentName != "" {
			lines := strings.Split(strings.TrimSpace(stripTerminalControlSequences(text)), "\n")
			app.QueueUpdateDraw(func() {
				for _, line := range lines {
					line = strings.TrimRight(line, "\r")
					if line == "" {
						fmt.Fprint(chat, "[yellow]│[-]\n")
						continue
					}
					fmt.Fprintf(chat, "[yellow]│ %s[-] %s\n", subAgentName, tview.Escape(line))
				}
				if app.GetFocus() != chat {
					chat.ScrollToEnd()
				}
			})
			return
		}
		w := chatWidth()
		out := markdown.render(text, w)
		app.QueueUpdateDraw(func() {
			fmt.Fprint(chatANSI, out)
			if app.GetFocus() != chat {
				chat.ScrollToEnd()
			}
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
				app.QueueUpdateDraw(func() { setFocus(0) })
			}()
			// Echo the user turn immediately as plain text so the first
			// submission is visible right away even if markdown rendering
			// is still warming up.
			appendChat("\n[::b]you[-]\n\n%s\n", sanitizeInputText(prompt))
			appendChat("[::b]assistant[-]\n")
			turnInputStart := inputTokensTotal.Load()
			turnCachedStart := cachedInputTokensTotal.Load()
			turnCacheCreateStart := cacheCreationTokensTotal.Load()
			turnOutputStart := outputTokensTotal.Load()
			defer func() {
				turnInput := inputTokensTotal.Load() - turnInputStart
				turnCached := cachedInputTokensTotal.Load() - turnCachedStart
				turnCacheCreate := cacheCreationTokensTotal.Load() - turnCacheCreateStart
				turnOutput := outputTokensTotal.Load() - turnOutputStart
				if turnInput < 0 {
					turnInput = 0
				}
				if turnCached < 0 {
					turnCached = 0
				}
				if turnCacheCreate < 0 {
					turnCacheCreate = 0
				}
				if turnOutput < 0 {
					turnOutput = 0
				}
				appendChat("%s\n", buildTurnUsageText(cfg, turnInput, turnCached, turnCacheCreate, turnOutput))
			}()

			seq := cfg.Runner.Run(ctx, cfg.UserID, cfg.SessionID,
				&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
				adkagent.RunConfig{})

			// Buffer assistant text per turn; flush as rendered markdown
			// either when a non-text part arrives (tool call/response)
			// or when the stream completes.
			var mdBuf strings.Builder
			var subAgentStack []string
			currentSubAgent := func() string {
				if len(subAgentStack) == 0 {
					return ""
				}
				return subAgentStack[len(subAgentStack)-1]
			}
			for ev, err := range seq {
				if err != nil {
					flushMarkdown(&mdBuf, currentSubAgent())
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
						flushMarkdown(&mdBuf, currentSubAgent())
						if _, ok := subAgentSet[strings.ToLower(strings.TrimSpace(p.FunctionCall.Name))]; ok {
							subAgentStack = append(subAgentStack, p.FunctionCall.Name)
							appendChat("[yellow][::b]--- entering sub-agent: %s ---[-]\n", p.FunctionCall.Name)
						}
						appendChat("[aqua]⚙ %s[-] %s\n",
							p.FunctionCall.Name, shortArgs(p.FunctionCall.Args))
					case p.FunctionResponse != nil:
						flushMarkdown(&mdBuf, currentSubAgent())
						appendChat("[gray]↳ %s[-]\n", p.FunctionResponse.Name)
						if len(subAgentStack) > 0 && subAgentStack[len(subAgentStack)-1] == p.FunctionResponse.Name {
							appendChat("[yellow][::b]--- leaving sub-agent: %s ---[-]\n", p.FunctionResponse.Name)
							subAgentStack = subAgentStack[:len(subAgentStack)-1]
						}
					}
				}
			}
			for i := len(subAgentStack) - 1; i >= 0; i-- {
				appendChat("[yellow][::b]--- leaving sub-agent: %s ---[-]\n", subAgentStack[i])
			}
			flushMarkdown(&mdBuf, currentSubAgent())
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
		case tcell.KeyCtrlC:
			app.Stop()
			return nil
		case tcell.KeyEsc:
			if focusIdx == 0 {
				app.Stop()
			} else {
				setFocus(0)
			}
			return nil
		case tcell.KeyTab:
			setFocus((focusIdx + 1) % len(focusList))
			return nil
		case tcell.KeyBacktab:
			setFocus((focusIdx + len(focusList) - 1) % len(focusList))
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

	// Catch OS-level SIGINT/SIGTERM so that a second Ctrl+C (which arrives
	// as a real signal once the terminal begins transitioning out of raw
	// mode) routes through app.Stop() and lets tcell finish screen.Fini()
	// before the process exits. Without this the terminal is left in a
	// broken state for subsequent commands in the same shell session.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			app.Stop()
		}
	}()

	// Welcome banner. Write directly: app.Run() hasn't started yet, so
	// QueueUpdateDraw would deadlock (its queue is only drained by Run).
	fmt.Fprintf(chat, "[gray]Welcome to %s. Type a message and press Enter.\nTab / Shift-Tab to scroll Chat or Trace panes. Esc returns focus to input. Ctrl-L clears the chat. Ctrl-C to quit.[-]\n", cfg.AppName)

	// Pre-warm markdown rendering so the first assistant turn avoids
	// renderer initialization cost on the critical path.
	go func() {
		r := markdown.rendererFor(80)
		if r == nil {
			return
		}
		_, _ = r.Render("warmup")
	}()

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
	var initialChatWidth int
	var resizeWarmupDone bool
	app.SetBeforeDrawFunc(func(tcell.Screen) bool {
		w := chatWidth()
		if w <= 0 {
			return false
		}
		if initialChatWidth == 0 {
			initialChatWidth = w
			return false
		}
		if !resizeWarmupDone && w != initialChatWidth {
			resizeWarmupDone = true
			go func(width int) {
				r := markdown.rendererFor(width)
				if r == nil {
					return
				}
				_, _ = r.Render("warmup")
			}(w)
		}
		return false
	})

	appRunning.Store(true)
	err := app.SetRoot(root, true).Run()
	// Mark the app as no longer running BEFORE deferred calls execute.
	// This prevents the deferred Bus.Emit(EventSessionEnd) handler from
	// calling appendTrace/appendChat → QueueUpdateDraw, which would block
	// forever because the event loop has already exited.
	appRunning.Store(false)
	return err
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

func buildStatusText(cfg Config, inputTokens, cachedInputTokens, cacheCreationTokens, outputTokens int64) string {
	text := fmt.Sprintf(" [::b]%s[-]   session: [yellow]%s[-]   user: [yellow]%s[-]   tokens in/out: [yellow]%d[-]/[yellow]%d[-]",
		cfg.AppName, cfg.SessionID, cfg.UserID, inputTokens, outputTokens)
	if cachedInputTokens > 0 || cacheCreationTokens > 0 {
		text += fmt.Sprintf(" [gray](cache r/w: %d/%d)[-]", cachedInputTokens, cacheCreationTokens)
	}
	if dollars, ok := totalCostDollars(inputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, cfg); ok {
		text += fmt.Sprintf("   total: [green]$%.6f[-]", dollars)
	}
	return text
}

func buildTurnUsageText(cfg Config, inputTokens, cachedInputTokens, cacheCreationTokens, outputTokens int64) string {
	totalTokens := inputTokens + outputTokens
	text := fmt.Sprintf("[gray]turn usage[-] in/out/total: [yellow]%d[-]/[yellow]%d[-]/[yellow]%d[-]",
		inputTokens, outputTokens, totalTokens)
	if cachedInputTokens > 0 || cacheCreationTokens > 0 {
		text += fmt.Sprintf(" [gray](cache r/w: %d/%d)[-]", cachedInputTokens, cacheCreationTokens)
	}
	if dollars, ok := totalCostDollars(inputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, cfg); ok {
		text += fmt.Sprintf("   [green]$%.6f[-]", dollars)
	}
	return text
}

// totalCostDollars splits the prompt into three buckets — fresh, cache-read
// and cache-creation — and applies a separate $/1M rate to each, then adds
// the output cost. Returns ok=false when the input/output prices are not
// configured. Cached/creation prices default to the input price (i.e. no
// discount) when unset, which preserves the legacy single-rate behaviour.
func totalCostDollars(inputTokens, cachedInputTokens, cacheCreationTokens, outputTokens int64, cfg Config) (float64, bool) {
	if cfg.InputTokenPricePerMillion <= 0 || cfg.OutputTokenPricePerMillion <= 0 {
		return 0, false
	}
	cachedPrice := cfg.CachedInputTokenPricePerMillion
	if cachedPrice <= 0 {
		cachedPrice = cfg.InputTokenPricePerMillion
	}
	creationPrice := cfg.CacheCreationTokenPricePerMillion
	if creationPrice <= 0 {
		creationPrice = cfg.InputTokenPricePerMillion
	}
	// Adapters normalise PromptTokenCount to the *total* prompt size,
	// which already includes both cache-read and cache-creation tokens.
	freshTokens := inputTokens - cachedInputTokens - cacheCreationTokens
	if freshTokens < 0 {
		freshTokens = 0
	}
	freshCost := float64(freshTokens) * cfg.InputTokenPricePerMillion / 1_000_000
	cachedCost := float64(cachedInputTokens) * cachedPrice / 1_000_000
	creationCost := float64(cacheCreationTokens) * creationPrice / 1_000_000
	outputCost := float64(outputTokens) * cfg.OutputTokenPricePerMillion / 1_000_000
	return freshCost + cachedCost + creationCost + outputCost, true
}

// showAskUserModal renders an interactive modal for the given question and
// wires the result back to the registry. It must be called from within an
// app.QueueUpdateDraw closure.
//
// For "single" and "confirm" kinds: a tview.Modal with one button per choice
// + a Cancel button.
// For "multi" and "text" kinds: a tview.Form with the relevant inputs.
func showAskUserModal(
	app *tview.Application,
	pages *tview.Pages,
	returnFocus tview.Primitive,
	reg *askuser.Registry,
	q askuser.Question,
) {
	pageName := "ask_user:" + q.ID

	resolve := func(ans askuser.Answer) {
		pages.RemovePage(pageName)
		app.SetFocus(returnFocus)
		_ = reg.Resolve(q.SessionID, q.ID, ans)
	}
	cancel := func() { resolve(askuser.Answer{Cancelled: true}) }

	title := " Question "
	switch q.Kind {
	case askuser.KindSingle, askuser.KindConfirm:
		modal := tview.NewModal().SetText(q.Prompt)
		buttons := append([]string{}, q.Choices...)
		buttons = append(buttons, "Cancel")
		modal.AddButtons(buttons)
		modal.SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Cancel" || buttonIndex >= len(q.Choices) {
				cancel()
				return
			}
			resolve(askuser.Answer{Selected: []string{buttonLabel}})
		})
		pages.AddPage(pageName, modal, false, true)
		app.SetFocus(modal)

	case askuser.KindMulti:
		form := tview.NewForm()
		form.SetTitle(title).SetBorder(true)
		form.AddTextView("Question", q.Prompt, 0, 2, true, false)
		checked := make([]bool, len(q.Choices))
		for i, c := range q.Choices {
			i := i // capture
			form.AddCheckbox(c, false, func(ch bool) { checked[i] = ch })
		}
		if q.AllowText {
			form.AddInputField("Custom answer", q.Default, 0, nil, nil)
		}
		form.AddButton("Submit", func() {
			// Check for custom text first.
			if q.AllowText {
				if field, ok := form.GetFormItemByLabel("Custom answer").(*tview.InputField); ok {
					if t := strings.TrimSpace(field.GetText()); t != "" {
						resolve(askuser.Answer{Text: t})
						return
					}
				}
			}
			var sel []string
			for i, c := range q.Choices {
				if checked[i] {
					sel = append(sel, c)
				}
			}
			if len(sel) == 0 {
				cancel()
				return
			}
			resolve(askuser.Answer{Selected: sel})
		})
		form.AddButton("Cancel", cancel)
		form.SetCancelFunc(cancel)
		pages.AddPage(pageName, centered(form, 60, 3+len(q.Choices)+4), true, true)
		app.SetFocus(form)

	default: // KindText
		form := tview.NewForm()
		form.SetTitle(title).SetBorder(true)
		form.AddTextView("Question", q.Prompt, 0, 3, true, false)
		form.AddInputField("Answer", q.Default, 0, nil, nil)
		form.AddButton("Submit", func() {
			field, ok := form.GetFormItemByLabel("Answer").(*tview.InputField)
			if !ok {
				cancel()
				return
			}
			t := strings.TrimSpace(field.GetText())
			if t == "" {
				cancel()
				return
			}
			resolve(askuser.Answer{Text: t})
		})
		form.AddButton("Cancel", cancel)
		form.SetCancelFunc(cancel)
		pages.AddPage(pageName, centered(form, 60, 10), true, true)
		app.SetFocus(form)
	}
}

// centered wraps a primitive in a flex that centres it with the given width
// and height. Used to position ask_user forms in the middle of the screen.
func centered(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)
}
