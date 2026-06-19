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
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
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
	"google.golang.org/genai"

	toolkitagent "github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/core/llm"
	"github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/agentmd"
	"github.com/blouargant/yoke/internal/askuser"
	"github.com/blouargant/yoke/internal/bg"
	"github.com/blouargant/yoke/internal/fileref"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/sessions"
	"github.com/blouargant/yoke/internal/shellcomplete"
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

// colorizeFileRefs wraps valid "@path" file/directory references in a colour
// tag so they read as links in the echoed user turn; missing paths are left as
// plain text. References are resolved against cwd.
func colorizeFileRefs(prompt, cwd string) string {
	spans := fileref.Spans(prompt)
	if len(spans) == 0 {
		return prompt
	}
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(prompt[prev:s.Start])
		ref := prompt[s.Start:s.End]
		switch fileref.Classify(s.Token, cwd).Kind {
		case fileref.KindFile, fileref.KindDir:
			b.WriteString("[#4fc1ff]" + ref + "[-]")
		default:
			b.WriteString(ref)
		}
		prev = s.End
	}
	b.WriteString(prompt[prev:])
	return b.String()
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
	// Manager owns the live agent generations. The TUI picks the runner
	// for a session via Manager.LookupSquad(sessionID, squadName).
	Manager *toolkitagent.Manager
	// Sessions is the multi-session registry the sidebar lists.
	Sessions *sessions.Registry
	// Bus is the shared event bus; the trace pane subscribes to it when
	// non-nil.
	Bus *events.Bus
	// AskUserRegistry receives ask_user tool questions; the TUI renders a
	// modal per question filtered to the active session.
	AskUserRegistry *askuser.Registry
	// UserID is the default user identifier used for new sessions and for
	// mailbox watchers.
	UserID string
	// AppName is shown in the title bar.
	AppName string
	// SubAgentNames lists sub-agent names so the chat pane can render
	// sub-agent live activity with a distinct prefix.
	SubAgentNames []string
	// RegisterSession / UnregisterSession update the cross-session
	// mailbox registry when a session is created or deleted. Forwarded
	// from agent.Infrastructure. Nil-safe.
	RegisterSession   func(userID, sessionID, displayName string) error
	UnregisterSession func(displayName string) error
	// WatchMailbox starts a background goroutine that surfaces mailbox
	// pushes for one session. Nil-safe; when nil the TUI simply skips
	// mailbox notifications.
	WatchMailbox func(ctx context.Context, userID, sessionID string, onMessage func(from, body string))
	// BgQueues, when non-nil, is drained before each turn so completed
	// background-task notifications reach the model (the between-turn drain;
	// the TUI has no server-style push goroutine). Nil-safe.
	BgQueues *bg.SessionQueues
	// AgentOptions are the Options used to build the initial Instance.
	// The hot-reload key (Ctrl-R) hands them back to Manager.Reload so a
	// new generation is built from the latest on-disk config.
	AgentOptions toolkitagent.Options

	InputTokenPricePerMillion  float64
	OutputTokenPricePerMillion float64
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

// sessionState is the TUI's view of the active session. The pointer is
// swapped atomically when the user switches sessions so concurrent
// goroutines (event handlers, mailbox watchers) read a consistent snapshot.
type sessionState struct {
	ID    string
	Squad string
}

// Run starts the TUI event loop and blocks until the user quits or ctx
// is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Manager == nil {
		return fmt.Errorf("tui: Manager required")
	}
	if cfg.Sessions == nil {
		return fmt.Errorf("tui: Sessions registry required")
	}
	if cfg.UserID == "" {
		cfg.UserID = sessions.DefaultUserID
	}
	if cfg.AppName == "" {
		cfg.AppName = "yoke"
	}
	subAgentSet := make(map[string]struct{}, len(cfg.SubAgentNames))
	for _, name := range cfg.SubAgentNames {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" {
			continue
		}
		subAgentSet[n] = struct{}{}
	}

	// Pick the initial session: most-recently-created persisted session,
	// or a fresh one on the default squad when no sessions exist yet.
	var current atomic.Pointer[sessionState]
	initial := pickInitialSession(cfg)
	current.Store(initial)

	// Per-session working directory for the interactive "!" shell-escape, so
	// an embedded `cd` persists between commands. Defaults to the process CWD.
	var bashCwdMu sync.Mutex
	bashCwd := map[string]string{}
	getBashCwd := func(id string) string {
		bashCwdMu.Lock()
		defer bashCwdMu.Unlock()
		if d, ok := bashCwd[id]; ok && d != "" {
			return d
		}
		wd, _ := os.Getwd()
		return wd
	}
	setBashCwd := func(id, dir string) {
		if dir == "" {
			return
		}
		bashCwdMu.Lock()
		bashCwd[id] = dir
		bashCwdMu.Unlock()
	}
	if cfg.RegisterSession != nil {
		_ = cfg.RegisterSession(cfg.UserID, initial.ID, initial.ID)
	}
	cfg.Manager.Pin(initial.ID)

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
	slashCommands := []string{"/help", "/compress", "/create-skill", "/create-skill ", "/update-skill ", "/learn", "/learn-now", "/learn-now ", "/learn ", "/status", "/init", "/upload ", "/new"}
	slashCommandsDisplay := []string{
		"/help",
		"/compress",
		"/create-skill",
		"/create-skill <name>",
		"/update-skill <name>",
		"/learn",
		"/learn-now",
		"/learn-now <reason>",
		"/learn <reason>",
		"/status",
		"/init",
		"/upload <path>",
		"/new",
	}
	// Bang ("!") shell-escape completion state, recomputed on each keystroke.
	// The dropdown shows just the completed leaf; selecting one splices it
	// onto bangPrefix to rebuild the full line.
	var bangPrefix string
	var bangCands []string
	// "@file" reference completion state. When the token at the end of the
	// buffer is an "@" reference (start of line or after a space, so emails are
	// excluded), the dropdown shows path candidates; selecting one splices it
	// onto atPrefix (everything up to and including the "@").
	var atPrefix string
	var atCands []string
	curCwd := func() string {
		if cur := current.Load(); cur != nil {
			return getBashCwd(cur.ID)
		}
		return getBashCwd("")
	}
	input.SetAutocompleteFunc(func(currentText string) []string {
		if strings.HasPrefix(currentText, "!") {
			line := currentText[1:]
			if line == "" {
				bangCands = nil
				atCands = nil
				return nil // bare "!" — don't dump every command on PATH
			}
			start, cands := shellcomplete.Complete(line, curCwd())
			if len(cands) == 0 {
				bangCands = nil
				atCands = nil
				return nil
			}
			bangPrefix = "!" + line[:start]
			bangCands = cands
			atCands = nil
			return cands
		}
		bangCands = nil
		// "@file" reference: complete the path of the last whitespace-delimited
		// token when it is an "@" reference.
		ts := strings.LastIndexAny(currentText, " \t\n") + 1
		if last := currentText[ts:]; strings.HasPrefix(last, "@") {
			cands := shellcomplete.CompletePath(last[1:], curCwd())
			if len(cands) == 0 {
				atCands = nil
				return nil
			}
			atPrefix = currentText[:ts] + "@"
			atCands = cands
			return cands
		}
		atCands = nil
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
		// Bang completion: splice the chosen leaf onto the preserved prefix.
		if bangCands != nil && index >= 0 && index < len(bangCands) {
			input.SetText(bangPrefix + bangCands[index])
			return true
		}
		// "@file" completion: splice the chosen path onto the "@" prefix.
		if atCands != nil && index >= 0 && index < len(atCands) {
			input.SetText(atPrefix + atCands[index])
			return true
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

	// ── Sessions sidebar ────────────────────────────────────────────────
	sessionsPane := tview.NewList().
		ShowSecondaryText(true).
		SetHighlightFullLine(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	sessionsPane.SetBorder(true).SetTitle(" Sessions ").SetTitleAlign(tview.AlignLeft)

	// sessionIDsInPane mirrors what's currently rendered in sessionsPane so
	// we can map a list index back to a session ID (the List API doesn't
	// surface item user-data unless we set it explicitly, which would
	// require a tview version bump). Index aligns with sessionsPane.
	var sessionIDsInPane []string

	// ── Archived sessions pane (collapsible) ────────────────────────────
	// Sits below the active session list in the left column. Hidden by
	// default; toggled with Ctrl-A. Archived sessions are read-only.
	archivedPane := tview.NewList().
		ShowSecondaryText(true).
		SetHighlightFullLine(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	archivedPane.SetBorder(true).SetTitle(" Archived ").SetTitleAlign(tview.AlignLeft)
	var archivedIDsInPane []string

	// ── Left pane: trace ────────────────────────────────────────────────
	trace := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true)
	trace.SetBorder(true).SetTitle(" Trace ").SetTitleAlign(tview.AlignLeft)
	trace.SetChangedFunc(func() { app.Draw() })

	// Focus cycling: Tab / Shift-Tab rotates focus among input → chat →
	// sessions → trace so the user can scroll any panel with arrow keys /
	// Page Up / Page Down. Esc returns focus to the input field; Ctrl-C
	// always quits.
	focusList := []tview.Primitive{input, chat, sessionsPane, trace, archivedPane}
	focusIdx := 0
	setFocus := func(idx int) {
		focusIdx = idx
		chat.SetBorderColor(tcell.ColorDefault)
		trace.SetBorderColor(tcell.ColorDefault)
		sessionsPane.SetBorderColor(tcell.ColorDefault)
		archivedPane.SetBorderColor(tcell.ColorDefault)
		switch idx {
		case 1: // chat
			chat.SetBorderColor(tcell.ColorYellow)
		case 2: // sessions
			sessionsPane.SetBorderColor(tcell.ColorYellow)
		case 3: // trace
			trace.SetBorderColor(tcell.ColorYellow)
		case 4: // archived
			archivedPane.SetBorderColor(tcell.ColorYellow)
		}
		app.SetFocus(focusList[idx])
	}

	// ── Status bar + per-session token counters ─────────────────────────
	//
	// The web UI tracks token usage independently per chat tab; mirror that
	// here by keying counters off the session ID. EventAfterModel runs on
	// the event bus goroutine, so we guard the map with a mutex. The
	// running counters survive session switches (they live in memory for
	// the life of the TUI) so the user can hop between chats without
	// losing the totals.
	status := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	var appRunning atomic.Bool
	type sessionTokens struct {
		input, cached, cacheCreate, output int64
	}
	tokenStore := make(map[string]*sessionTokens)
	var tokenMu sync.Mutex
	// billingSession overrides the current-session lookup while a model
	// call is in flight, so token events that arrive after the user has
	// switched sessions still credit the session that issued the turn.
	var billingSession atomic.Pointer[string]
	tokensFor := func(id string) *sessionTokens {
		tokenMu.Lock()
		defer tokenMu.Unlock()
		t := tokenStore[id]
		if t == nil {
			t = &sessionTokens{}
			tokenStore[id] = t
		}
		return t
	}
	currentTokens := func() (in, cached, cacheCreate, out int64) {
		cur := current.Load()
		if cur == nil {
			return
		}
		t := tokensFor(cur.ID)
		tokenMu.Lock()
		defer tokenMu.Unlock()
		return t.input, t.cached, t.cacheCreate, t.output
	}
	addTokens := func(in, cached, cacheCreate, out int64) {
		var id string
		if bid := billingSession.Load(); bid != nil {
			id = *bid
		} else if cur := current.Load(); cur != nil {
			id = cur.ID
		} else {
			return
		}
		t := tokensFor(id)
		tokenMu.Lock()
		t.input += in
		t.cached += cached
		t.cacheCreate += cacheCreate
		t.output += out
		tokenMu.Unlock()
	}
	setStatus := func() {
		cur := current.Load()
		sid, squad := "", ""
		if cur != nil {
			sid = cur.ID
			squad = cur.Squad
		}
		in, cached, cacheCreate, out := currentTokens()
		status.SetText(buildStatusText(cfg, sid, squad, in, cached, cacheCreate, out))
	}
	setStatus()

	// ── Root layout ─────────────────────────────────────────────────────
	// Sessions sidebar | Chat+Input | Trace (hidden by default). The
	// trace pane is opt-in via Ctrl-T because most users don't need
	// per-tool-call diagnostics and the chat pane benefits from the
	// extra width.
	// Left column stacks the active session list over a collapsible archived
	// list. The archived pane is removed from the flex when hidden (mirrors
	// the trace-pane toggle) so it never steals vertical space when empty.
	leftCol := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(sessionsPane, 0, 1, false)
	archivedVisible := false
	setArchivedVisible := func(show bool) {
		if show == archivedVisible {
			return
		}
		archivedVisible = show
		if show {
			leftCol.AddItem(archivedPane, 0, 1, false)
		} else {
			leftCol.RemoveItem(archivedPane)
			if focusIdx == 4 {
				setFocus(0)
			}
		}
	}
	main := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(leftCol, 28, 0, false).
		AddItem(rightPane, 0, 1, true)
	traceVisible := false
	setTraceVisible := func(show bool) {
		if show == traceVisible {
			return
		}
		traceVisible = show
		if show {
			main.AddItem(trace, 40, 0, false)
		} else {
			main.RemoveItem(trace)
			// If trace had focus when hidden, fall back to the input pane.
			if focusIdx == 3 {
				setFocus(0)
			}
		}
	}

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
				addTokens(promptTok, cachedTok, cacheCreateTok, candTok)
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
			// Per-session counters live in tokenStore; the active session
			// starts at zero on first access. Nothing to reset globally.
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
			cur := current.Load()
			// Only respond to questions for the active session.
			if q.SessionID != "" && (cur == nil || q.SessionID != cur.ID) {
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

	// ── Session helpers ─────────────────────────────────────────────────
	//
	// All of these mutate UI state and must be called from the tview event
	// loop goroutine (typically from a key-binding callback or a
	// QueueUpdateDraw closure). The current session pointer is the single
	// source of truth: send(), the ask_user filter, and the mailbox
	// watcher all read it before acting.
	availableSquads := func() []string {
		inst := cfg.Manager.Current()
		if inst == nil {
			return []string{toolkitagent.DefaultSquadName}
		}
		names := inst.SquadNames()
		if len(names) == 0 {
			return []string{toolkitagent.DefaultSquadName}
		}
		return nameAvailableSquads(names)
	}

	refreshSessions := func() {
		// Capture the existing selection so we keep it pointed at the
		// current session after the list is rebuilt.
		cur := current.Load()
		sessionsPane.Clear()
		archivedPane.Clear()
		sessionIDsInPane = sessionIDsInPane[:0]
		archivedIDsInPane = archivedIDsInPane[:0]
		selectIdx := 0
		row := func(m *sessions.SessionMeta) (string, string) {
			label := m.ID
			if m.Title != "" {
				label = m.Title
			}
			squad := m.Squad
			if squad == "" {
				squad = toolkitagent.DefaultSquadName
			}
			secondary := fmt.Sprintf("[gray]%s · %d turn(s) · %s[-]",
				squad, m.Turns, m.LastUsedAt.Format("01-02 15:04"))
			return label, secondary
		}
		for _, m := range cfg.Sessions.List() {
			label, secondary := row(m)
			if m.Archived {
				archivedPane.AddItem(label, secondary, 0, nil)
				archivedIDsInPane = append(archivedIDsInPane, m.ID)
				continue
			}
			if cur != nil && m.ID == cur.ID {
				selectIdx = len(sessionIDsInPane)
			}
			sessionsPane.AddItem(label, secondary, 0, nil)
			sessionIDsInPane = append(sessionIDsInPane, m.ID)
		}
		if len(sessionIDsInPane) > 0 {
			sessionsPane.SetCurrentItem(selectIdx)
		}
		archivedPane.SetTitle(fmt.Sprintf(" Archived (%d) ", len(archivedIDsInPane)))
	}

	// chatANSIWriter renders one persisted turn into the chat pane using
	// the same markdown pipeline as live streaming. Keeps replay visually
	// consistent with fresh output.
	renderTurn := func(userText, assistantText string) {
		fmt.Fprintf(chat, "\n[::b]you[-]\n\n%s\n", tview.Escape(strings.TrimSpace(userText)))
		fmt.Fprint(chat, "[::b]assistant[-]\n")
		if strings.TrimSpace(assistantText) == "" {
			return
		}
		w, _, _, _ := chat.GetInnerRect()
		if w <= 0 {
			w = 80
		}
		out := markdown.render(assistantText, w)
		fmt.Fprint(chatANSI, out)
	}

	loadHistory := func(sessionID string) {
		turns, err := sessions.LoadConversationTurns(sessionID)
		if err != nil {
			fmt.Fprintf(chat, "\n[red]load history: %v[-]\n", err)
			return
		}
		for _, t := range turns {
			renderTurn(t.UserText, t.AssistantText)
		}
		chat.ScrollToEnd()
	}

	switchSession := func(id string) {
		if id == "" {
			return
		}
		meta, ok := cfg.Sessions.Get(id)
		if !ok {
			return
		}
		squad := meta.Squad
		if squad == "" {
			squad = toolkitagent.DefaultSquadName
		}
		cfg.Manager.Pin(id)
		current.Store(&sessionState{ID: id, Squad: squad})
		chat.Clear()
		fmt.Fprintf(chat, "[gray]── session %s · squad %s ──[-]\n", id, squad)
		loadHistory(id)
		setStatus()
		refreshSessions()
	}

	createSession := func(squad string) {
		if squad == "" {
			// New chats default to the Omnis router squad when routing is
			// enabled; otherwise the default squad.
			squad = toolkitagent.DefaultSquadName
			if rs := cfg.Manager.RouterSquad(); rs != "" {
				squad = rs
			}
		}
		meta := cfg.Sessions.New(squad)
		_ = sessions.SetConversationSquad(meta.ID, squad)
		if cfg.RegisterSession != nil {
			_ = cfg.RegisterSession(cfg.UserID, meta.ID, meta.ID)
		}
		cfg.Manager.Pin(meta.ID)
		switchSession(meta.ID)
	}

	deleteSession := func(id string) {
		if id == "" {
			return
		}
		meta, ok := cfg.Sessions.Get(id)
		if !ok {
			return
		}
		display := meta.ID
		if meta.Title != "" {
			display = meta.Title
		}
		userID := meta.UserID
		if userID == "" {
			userID = cfg.UserID
		}
		if !cfg.Sessions.Delete(id) {
			return
		}
		if cfg.UnregisterSession != nil {
			_ = cfg.UnregisterSession(display)
		}
		sessions.DeleteSessionLogs(userID, id)
		cfg.Manager.Release(id)
		// Pick a follow-up session: prefer the most recent remaining one;
		// fall back to creating a fresh default-squad session so the chat
		// pane is never left in a "no current session" state.
		remaining := cfg.Sessions.List()
		if len(remaining) > 0 {
			switchSession(remaining[0].ID)
		} else {
			createSession("") // "" → router squad when routing enabled
		}
	}

	archiveSession := func(id string) {
		if id == "" || !cfg.Sessions.SetArchived(id, true) {
			return
		}
		cfg.Manager.Release(id)
		// If the archived session was current, move to a remaining active one
		// (or a fresh session) so the chat pane is never left read-only-stuck.
		cur := current.Load()
		if cur != nil && cur.ID == id {
			var next string
			for _, m := range cfg.Sessions.List() {
				if !m.Archived {
					next = m.ID
					break
				}
			}
			if next != "" {
				switchSession(next)
			} else {
				createSession("") // "" → router squad when routing enabled
			}
		} else {
			refreshSessions()
		}
	}

	unarchiveSession := func(id string) {
		if id == "" || !cfg.Sessions.SetArchived(id, false) {
			return
		}
		cfg.Manager.Pin(id)
		switchSession(id)
	}

	chooseSquadModal := func(onPick func(squad string)) {
		squads := availableSquads()
		modal := tview.NewModal().SetText("Select squad for new session:")
		modal.AddButtons(append(append([]string{}, squads...), "Cancel"))
		modal.SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			pages.RemovePage("squad_picker")
			app.SetFocus(input)
			setFocus(0)
			if buttonLabel == "Cancel" || buttonIndex >= len(squads) {
				return
			}
			onPick(squads[buttonIndex])
		})
		pages.AddPage("squad_picker", modal, false, true)
		app.SetFocus(modal)
	}

	// Wire sidebar key bindings: Enter switches, n/N creates, d/D deletes.
	sessionsPane.SetSelectedFunc(func(idx int, _, _ string, _ rune) {
		if idx < 0 || idx >= len(sessionIDsInPane) {
			return
		}
		id := sessionIDsInPane[idx]
		switchSession(id)
		setFocus(0)
	})
	sessionsPane.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'n', 'N':
			chooseSquadModal(func(squad string) { createSession(squad) })
			return nil
		case 'a', 'A':
			idx := sessionsPane.GetCurrentItem()
			if idx >= 0 && idx < len(sessionIDsInPane) {
				archiveSession(sessionIDsInPane[idx])
			}
			return nil
		case 'd', 'D':
			idx := sessionsPane.GetCurrentItem()
			if idx >= 0 && idx < len(sessionIDsInPane) {
				deleteSession(sessionIDsInPane[idx])
			}
			return nil
		}
		return ev
	})

	// Archived pane: Enter views read-only, u/U unarchives, d/D deletes.
	archivedPane.SetSelectedFunc(func(idx int, _, _ string, _ rune) {
		if idx < 0 || idx >= len(archivedIDsInPane) {
			return
		}
		switchSession(archivedIDsInPane[idx])
		setFocus(1) // focus chat so the user can read the history
	})
	archivedPane.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		idx := archivedPane.GetCurrentItem()
		if idx < 0 || idx >= len(archivedIDsInPane) {
			return ev
		}
		switch ev.Rune() {
		case 'u', 'U':
			unarchiveSession(archivedIDsInPane[idx])
			return nil
		case 'd', 'D':
			deleteSession(archivedIDsInPane[idx])
			return nil
		}
		return ev
	})

	// ── Mailbox push watcher ────────────────────────────────────────────
	//
	// One watcher per active session. We restart it on every session
	// switch by cancelling the previous context. The infrastructure's
	// poll loop already handles backoff and termination.
	var watcherCancel context.CancelFunc
	startWatcher := func(sessionID string) {
		if watcherCancel != nil {
			watcherCancel()
			watcherCancel = nil
		}
		if cfg.WatchMailbox == nil || sessionID == "" {
			return
		}
		wctx, cancel := context.WithCancel(ctx)
		watcherCancel = cancel
		go cfg.WatchMailbox(wctx, cfg.UserID, sessionID, func(from, body string) {
			appendChat("\n[magenta]✉ from %s[-] %s\n", tview.Escape(from), tview.Escape(body))
		})
	}
	startWatcher(initial.ID)
	// Augment switchSession to restart the watcher when the user moves
	// between sessions. We rebind the closure rather than wrap it because
	// the sidebar callbacks captured the original pointer.
	innerSwitch := switchSession
	switchSession = func(id string) {
		innerSwitch(id)
		startWatcher(id)
	}
	// Build the Settings overlay (Skills / Agents tabs) once and reuse
	// across opens. open() refreshes its data from disk each time so the
	// list reflects any installs/edits that happened in the background.
	settings := newSettingsView(app, pages, input, markdown)

	// Populate the sidebar and render the initial session's history.
	refreshSessions()
	fmt.Fprintf(chat, "[gray]── session %s · squad %s ──[-]\n", initial.ID, initial.Squad)
	loadHistory(initial.ID)

	// busy flag prevents overlapping submissions.
	busy := false
	// send is declared before handleShortcut so the shortcut handler can call it.
	var send func(string)
	handleShortcut := func(raw string) {
		line := strings.TrimSpace(raw)
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return
		}
		cmd := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
		switch cmd {
		case "create-skill":
			name := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			if name == "" {
				appendChat("\n[::b]assistant[-]\n")
				appendChat("[yellow]Usage:[-] /create-skill <name>\n")
				appendChat("  Example: /create-skill k8s-triage\n")
				return
			}
			go send(fmt.Sprintf(
				"Create a new skill called %q. Load the skill-creator skill and guide me through defining it interactively.",
				name,
			))
		case "update-skill":
			name := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			if name == "" {
				appendChat("\n[::b]assistant[-]\n")
				appendChat("[yellow]Usage:[-] /update-skill <name>\n")
				return
			}
			go send(fmt.Sprintf(
				"Update the skill %q. Load the skill-creator skill and help me revise it.",
				name,
			))
		case "compress":
			appendChat("\n[::b]assistant[-]\n")
			if cfg.Bus == nil {
				appendChat("[yellow]Context compression unavailable:[-] event bus not configured.\n")
				return
			}
			cur := current.Load()
			if cur == nil {
				appendChat("[yellow]No active session.[-]\n")
				return
			}
			go cfg.Bus.Emit(events.EventCompressNow, map[string]any{
				"user_id":    cfg.UserID,
				"session_id": cur.ID,
			})
			appendChat("[green]Context compression triggered.[-] It will run before the next model call.\n")
		case "learn":
			reason := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			if reason == "" {
				reason = "manual /learn request from TUI"
			}
			appendChat("\n[::b]assistant[-]\n")
			cur := current.Load()
			if cur == nil {
				appendChat("[red]/learn failed:[-] no active session\n")
				return
			}
			msg, err := toolkitagent.RequestCurateSession(cfg.UserID, cur.ID, reason)
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
			cur := current.Load()
			if cur == nil {
				appendChat("[red]/learn-now failed:[-] no active session\n")
				return
			}
			msg, err := toolkitagent.RequestCurateSession(cfg.UserID, cur.ID, reason)
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
				"session_id": cur.ID,
			})
			appendChat("[green]%s[-]\n", msg)
			appendChat("[green]Triggered curation now.[-] Check trace/logs for curator completion.\n")
		case "status":
			cur := current.Load()
			appendChat("\n[::b]assistant[-]\n")
			appendChat("Session status:\n")
			appendChat("  app: [aqua]%s[-]\n", cfg.AppName)
			appendChat("  user: [aqua]%s[-]\n", cfg.UserID)
			if cur == nil {
				appendChat("  session: [yellow]none[-]\n")
				appendChat("  event_bus_configured: [aqua]%t[-]\n", cfg.Bus != nil)
				return
			}
			requested := toolkitagent.CurateSessionRequestedByIDs(cfg.UserID, cur.ID)
			squad := cur.Squad
			if squad == "" {
				squad = toolkitagent.DefaultSquadName
			}
			appendChat("  session: [aqua]%s[-]  squad: [aqua]%s[-]\n", cur.ID, squad)
			appendChat("  curation_requested: [aqua]%t[-]\n", requested)
			appendChat("  event_bus_configured: [aqua]%t[-]\n", cfg.Bus != nil)
		case "upload":
			arg := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			appendChat("\n[::b]assistant[-]\n")
			if arg == "" {
				appendChat("[yellow]Usage:[-] /upload <path>\n")
				return
			}
			cur := current.Load()
			if cur == nil {
				appendChat("[red]/upload failed:[-] no active session\n")
				return
			}
			staged, err := stageUploadFile(cur.ID, arg)
			if err != nil {
				appendChat("[red]/upload failed:[-] %v\n", err)
				return
			}
			appendChat("[green]uploaded[-] %s\n", staged)
			appendChat("[gray]Reference it in your next prompt — the agent's fs tools can read it directly.[-]\n")
		case "init":
			appendChat("\n[::b]assistant[-]\n")
			appendChat("[green]Initializing AGENT.md[-] — the agent will analyze the repo and write the file.\n")
			go send(agentmd.InitPrompt())
		case "new":
			chooseSquadModal(func(squad string) { createSession(squad) })
		case "help":
			appendChat("\n[::b]assistant[-]\n")
			appendChat("Available shortcuts:\n")
			appendChat("  [aqua]/compress[-]              Trigger context compression before the next model call.\n")
			appendChat("  [aqua]/create-skill <name>[-]   Create a new skill playbook with agent guidance.\n")
			appendChat("  [aqua]/update-skill <name>[-]   Update an existing skill playbook with agent guidance.\n")
			appendChat("  [aqua]/learn [reason][-]        Mark this session for soft-skill curation.\n")
			appendChat("  [aqua]/learn-now [reason][-]  Mark and trigger soft-skill curation immediately.\n")
			appendChat("  [aqua]/status[-]              Show current session and curation status.\n")
			appendChat("  [aqua]/init[-]                Analyze the repo and write a starter AGENT.md.\n")
			appendChat("  [aqua]/upload <path>[-]        Stage a file under the current session's uploads dir.\n")
			appendChat("  [aqua]#<text>[-]              Append a one-line memory to the project AGENT.md.\n")
			appendChat("  [aqua]/new[-]                  Create a new session (squad picker).\n")
			appendChat("  [aqua]/help[-]                Show this help.\n")
			appendChat("\nSession sidebar (Tab to focus): [aqua]n[-] new, [aqua]a[-] archive, [aqua]d[-] delete, [aqua]Enter[-] switch.\n")
			appendChat("Archived pane (Ctrl-A): [aqua]Enter[-] view (read-only), [aqua]u[-] unarchive, [aqua]d[-] delete.\n")
			appendChat("Global: [aqua]Ctrl-S[-] settings · [aqua]Ctrl-T[-] toggle trace · [aqua]Ctrl-A[-] toggle archived · [aqua]Ctrl-R[-] hot-reload · [aqua]Ctrl-L[-] clear chat · [aqua]Ctrl-C[-] quit.\n")
		default:
			appendChat("\n[::b]assistant[-]\n")
			appendChat("[yellow]Unknown shortcut:[-] /%s (try /help)\n", cmd)
		}
	}

	send = func(prompt string) {
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
		// Bang shell-escape: "!<cmd>" runs <cmd> directly (bypassing the agent,
		// keeping the hard safety floor) in the session's persistent CWD. Like
		// the slash path, the body runs in its own goroutine so appendChat's
		// QueueUpdateDraw doesn't deadlock the Enter handler.
		if strings.HasPrefix(strings.TrimSpace(prompt), "!") {
			command := strings.TrimSpace(strings.TrimSpace(prompt)[1:])
			input.SetText("")
			if command == "" {
				return
			}
			sid := ""
			if cur := current.Load(); cur != nil {
				sid = cur.ID
			}
			go func() {
				appendChat("\n[::b]you[-]\n\n[teal]![-]%s\n", tview.Escape(command))
				out, newCwd, _ := tools.RunBashInteractive(ctx, command, getBashCwd(sid), 0)
				setBashCwd(sid, newCwd)
				appendChat("[gray]── %s ──[-]\n%s\n", tview.Escape(newCwd), tview.Escape(out))
			}()
			return
		}
		// Hash memory: "#<text>" appends a one-line memory to the project
		// AGENT.md (resolved from the session CWD) instead of being sent to the
		// agent — symmetric with the "!" shell-escape.
		if strings.HasPrefix(strings.TrimSpace(prompt), "#") {
			text := strings.TrimSpace(strings.TrimSpace(prompt)[1:])
			input.SetText("")
			if text == "" {
				return
			}
			sid := ""
			if cur := current.Load(); cur != nil {
				sid = cur.ID
			}
			go func() {
				appendChat("\n[::b]you[-]\n\n[teal]#[-]%s\n", tview.Escape(text))
				path, err := agentmd.AppendMemory(getBashCwd(sid), text)
				if err != nil {
					appendChat("[red]%s[-]\n", tview.Escape(err.Error()))
					return
				}
				appendChat("[gray]── saved to %s ──[-]\n", tview.Escape(path))
			}()
			return
		}
		if busy {
			return
		}
		cur := current.Load()
		if cur == nil {
			appendChat("\n[red]No active session — press 'n' on the Sessions pane to create one.[-]\n")
			return
		}
		// Archived sessions are read-only: block new turns until unarchived.
		if meta, ok := cfg.Sessions.Get(cur.ID); ok && meta.Archived {
			appendChat("\n[yellow]Session is archived — open the Archived pane (Ctrl-A) and press 'u' to unarchive before continuing.[-]\n")
			return
		}
		squadInst := cfg.Manager.LookupSquad(cur.ID, cur.Squad)
		if squadInst == nil || squadInst.Runner == nil {
			appendChat("\n[red]No runner for session %s (squad %s).[-]\n", cur.ID, cur.Squad)
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
			sessionID := cur.ID
			billingSession.Store(&sessionID)
			defer billingSession.Store(nil)
			// Echo the user turn immediately as plain text so the first
			// submission is visible right away even if markdown rendering
			// is still warming up.
			appendChat("\n[::b]you[-]\n\n%s\n", colorizeFileRefs(sanitizeInputText(prompt), getBashCwd(sessionID)))
			appendChat("[::b]assistant[-]\n")
			// Snapshot the per-session counters now so the turn-usage line
			// reports only the delta this turn produced. We snapshot the
			// session we're about to address, not whichever is "current"
			// at the moment the deferred fires (matters if the user
			// switches sessions mid-turn).
			startTok := tokensFor(sessionID)
			tokenMu.Lock()
			turnInputStart := startTok.input
			turnCachedStart := startTok.cached
			turnCacheCreateStart := startTok.cacheCreate
			turnOutputStart := startTok.output
			tokenMu.Unlock()
			defer func() {
				endTok := tokensFor(sessionID)
				tokenMu.Lock()
				turnInput := endTok.input - turnInputStart
				turnCached := endTok.cached - turnCachedStart
				turnCacheCreate := endTok.cacheCreate - turnCacheCreateStart
				turnOutput := endTok.output - turnOutputStart
				tokenMu.Unlock()
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

			parts := []*genai.Part{{Text: prompt}}
			// Inline the content of any "@path" file references, resolved
			// against the session's interactive working directory.
			if note := fileref.Context(prompt, getBashCwd(sessionID)); note != "" {
				parts = append(parts, &genai.Part{Text: note})
			}
			// Surface completed background-task notifications by draining the
			// per-session queue between turns (no push goroutine in the TUI).
			if cfg.BgQueues != nil {
				if pending := cfg.BgQueues.For(cfg.UserID, sessionID).Drain(); len(pending) > 0 {
					parts = append(parts, &genai.Part{Text: bg.FormatBatch(pending)})
				}
			}
			routerSquad := cfg.Manager.RouterSquad()
			// runHop streams one squad turn (one Runner.Run), rendering as it
			// arrives, and returns the assistant text for that hop. The Omnis
			// dispatch loop calls it once per hop; control returns here between
			// hops so a topic switch can route to a different squad seamlessly.
			//
			// The router hop is suppressed: the model often narrates ("Routed to
			// the default squad…") and emits routing tool calls despite the
			// instruction. We render nothing for it during the stream, accumulate
			// its text, and after the hop show that text ONLY if the router did not
			// route (a clarifying question); a route discards it. The "── routed to
			// X squad ──" line (notify) is the only visible routing signal.
			runHop := func(rctx context.Context, hopSq *toolkitagent.SquadInstance, squadName string, hopParts []*genai.Part) (string, error) {
				seq := hopSq.Runner.Run(rctx, cfg.UserID, sessionID,
					&genai.Content{Role: "user", Parts: hopParts},
					adkagent.RunConfig{})

				isRouter := routerSquad != "" && squadName == routerSquad

				// Buffer assistant text per turn; flush as rendered markdown
				// either when a non-text part arrives (tool call/response)
				// or when the stream completes. Also accumulate the plain
				// assistant text so we can persist the turn at the end.
				var mdBuf strings.Builder
				var assistantText strings.Builder
				var subAgentStack []string
				currentSubAgent := func() string {
					if len(subAgentStack) == 0 {
						return ""
					}
					return subAgentStack[len(subAgentStack)-1]
				}
				// On the router hop, mid-stream rendering is suppressed (mdBuf still
				// accumulates so we can decide what to show afterwards).
				flush := func() {
					if isRouter {
						return
					}
					flushMarkdown(&mdBuf, currentSubAgent())
				}
				chat := func(format string, args ...any) {
					if isRouter {
						return
					}
					appendChat(format, args...)
				}
				for ev, err := range seq {
					if err != nil {
						flush()
						chat("\n[red]error: %v[-]\n", err)
						return assistantText.String(), err
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
							clean := stripTerminalControlSequences(p.Text)
							mdBuf.WriteString(clean)
							assistantText.WriteString(clean)
						case p.FunctionCall != nil:
							flush()
							if _, ok := subAgentSet[strings.ToLower(strings.TrimSpace(p.FunctionCall.Name))]; ok {
								subAgentStack = append(subAgentStack, p.FunctionCall.Name)
								chat("[yellow][::b]--- entering sub-agent: %s ---[-]\n", p.FunctionCall.Name)
							}
							chat("[aqua]⚙ %s[-] %s\n",
								p.FunctionCall.Name, shortArgs(p.FunctionCall.Args))
						case p.FunctionResponse != nil:
							flush()
							chat("[gray]↳ %s[-]\n", p.FunctionResponse.Name)
							if len(subAgentStack) > 0 && subAgentStack[len(subAgentStack)-1] == p.FunctionResponse.Name {
								chat("[yellow][::b]--- leaving sub-agent: %s ---[-]\n", p.FunctionResponse.Name)
								subAgentStack = subAgentStack[:len(subAgentStack)-1]
							}
						}
					}
				}
				for i := len(subAgentStack) - 1; i >= 0; i-- {
					chat("[yellow][::b]--- leaving sub-agent: %s ---[-]\n", subAgentStack[i])
				}
				flush()

				if isRouter {
					if cfg.Manager.PendingRoute(sessionID) {
						return "", nil // routed → discard the router's chatter
					}
					// Router chose to talk to the user (no route): render its reply.
					flushMarkdown(&mdBuf, "")
					return assistantText.String(), nil
				}
				return assistantText.String(), nil
			}
			// notify prints the routing transition and re-points the session at
			// the new squad so the next turn resumes there.
			notify := func(from, to, reason string) {
				cfg.Sessions.SetSquad(sessionID, to)
				if c := current.Load(); c != nil && c.ID == sessionID {
					current.Store(&sessionState{ID: sessionID, Squad: to})
				}
				appendChat("[gray]── routed to %s squad ──[-]\n", to)
			}

			// TUI turns are text-only, so nil routerParts feeds the router the
			// same (clean) text parts as the answering squad.
			_, fullText, _ := cfg.Manager.RunWithRouting(ctx, cfg.UserID, sessionID, cur.Squad, parts, nil, runHop, notify)

			// Persist the turn to the conversation file and refresh the
			// sidebar so the LastUsedAt / turn count update.
			cfg.Sessions.Touch(sessionID)
			if err := sessions.AppendConversationTurn(sessionID, prompt, fullText); err != nil {
				appendChat("[red]persist turn: %v[-]\n", err)
			}
			app.QueueUpdateDraw(func() {
				refreshSessions()
				setStatus()
			})
		}()
	}

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			send(input.GetText())
		}
	})

	// cycleFocus advances focus by delta (±1) and skips the trace pane
	// when it's hidden so Tab never lands the user on an invisible pane.
	cycleFocus := func(delta int) {
		n := len(focusList)
		idx := focusIdx
		for i := 0; i < n; i++ {
			idx = (idx + delta + n) % n
			if idx == 3 && !traceVisible {
				continue
			}
			if idx == 4 && !archivedVisible {
				continue
			}
			setFocus(idx)
			return
		}
	}

	// Global key bindings.
	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// When the Settings overlay is in front, delegate Tab cycling
		// and Esc to it so the chat-mode bindings don't steal those
		// keys. Ctrl-C still falls through so the user can always quit.
		if pages.HasPage("settings") {
			if name, _ := pages.GetFrontPage(); name == "settings" && ev.Key() != tcell.KeyCtrlC {
				if next := settings.handleKey(ev); next == nil {
					return nil
				}
				return ev
			}
		}
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
			cycleFocus(1)
			return nil
		case tcell.KeyBacktab:
			cycleFocus(-1)
			return nil
		case tcell.KeyCtrlL:
			chat.Clear()
			return nil
		case tcell.KeyCtrlT:
			setTraceVisible(!traceVisible)
			return nil
		case tcell.KeyCtrlA:
			setArchivedVisible(!archivedVisible)
			if archivedVisible && len(archivedIDsInPane) > 0 {
				setFocus(4) // jump into the archived list once revealed
			}
			return nil
		case tcell.KeyCtrlS:
			// Open the Settings overlay. The overlay installs its own
			// per-pane key captures plus a top-level Tab/Esc handler;
			// the global capture below routes Tab/Esc to it while it's
			// the front page.
			if !pages.HasPage("settings") {
				settings.open()
			}
			return nil
		case tcell.KeyCtrlR:
			go func() {
				appendChat("\n[gray]hot-reloading agent generation…[-]\n")
				inst, err := cfg.Manager.Reload(ctx, cfg.AgentOptions)
				if err != nil {
					appendChat("[red]reload failed:[-] %v\n", err)
					return
				}
				appendChat("[green]reload complete[-] (generation=%d)\n", inst.Generation)
			}()
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
	fmt.Fprintf(chat, "[gray]Welcome to %s. Type a message and press Enter.\nTab cycles Input → Chat → Sessions (and Trace when shown). Sessions pane: n=new, d=delete, Enter=switch.\nCtrl-S opens Settings · Ctrl-T toggles Trace · Ctrl-R hot-reloads · Ctrl-L clears chat · Ctrl-C quits.[-]\n", cfg.AppName)

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
		startID := initial.ID
		go cfg.Bus.Emit(events.EventSessionStart, map[string]any{
			"user_id":    cfg.UserID,
			"session_id": startID,
		})
		defer func() {
			cur := current.Load()
			endID := startID
			if cur != nil {
				endID = cur.ID
			}
			cfg.Bus.Emit(events.EventSessionEnd, map[string]any{
				"user_id":    cfg.UserID,
				"session_id": endID,
			})
		}()
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

func buildStatusText(cfg Config, sessionID, squad string, inputTokens, cachedInputTokens, cacheCreationTokens, outputTokens int64) string {
	if squad == "" {
		squad = toolkitagent.DefaultSquadName
	}
	text := fmt.Sprintf(" [::b]%s[-]   session: [yellow]%s[-] ([aqua]%s[-])   user: [yellow]%s[-]   tokens in/out: [yellow]%d[-]/[yellow]%d[-]",
		cfg.AppName, sessionID, squad, cfg.UserID, inputTokens, outputTokens)
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

// pickInitialSession returns the session the TUI should land on at
// startup: the most recently used persisted one, or a brand new
// default-squad session when the registry is empty. The new-session
// path also persists the squad to disk so the conversation file is
// usable on the next launch.
func pickInitialSession(cfg Config) *sessionState {
	list := cfg.Sessions.List()
	if len(list) > 0 {
		// List is sorted newest-first by CreatedAt; bump LastUsedAt by
		// preferring whichever entry has the latest activity to align
		// with the web UI's "resume your last chat" behaviour.
		latest := list[0]
		for _, m := range list[1:] {
			if m.LastUsedAt.After(latest.LastUsedAt) {
				latest = m
			}
		}
		squad := latest.Squad
		if squad == "" {
			squad = toolkitagent.DefaultSquadName
		}
		return &sessionState{ID: latest.ID, Squad: squad}
	}
	squad := toolkitagent.DefaultSquadName
	if rs := cfg.Manager.RouterSquad(); rs != "" {
		squad = rs
	}
	meta := cfg.Sessions.New(squad)
	_ = sessions.SetConversationSquad(meta.ID, squad)
	return &sessionState{ID: meta.ID, Squad: squad}
}

// stageUploadFile copies the user-supplied file into the session's
// uploads directory under $YOKE_HOME/logs/uploads/<sessionID>/ so the
// agent (or its tools) can reach it from a stable path. Returns the
// staged absolute path on success.
func stageUploadFile(sessionID, src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", abs)
	}
	uploadsDir := filepath.Join(paths.LogsDir(), "uploads", sessionID)
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(uploadsDir, filepath.Base(abs))
	in, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return "", err
	}
	return dst, nil
}

// nameAvailableSquads sorts the squad list with the default squad first
// so the picker dialog always offers a familiar baseline.
func nameAvailableSquads(in []string) []string {
	out := append([]string{}, in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == toolkitagent.DefaultSquadName {
			return true
		}
		if out[j] == toolkitagent.DefaultSquadName {
			return false
		}
		return out[i] < out[j]
	})
	return out
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
