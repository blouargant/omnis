package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/registrymeta"
)

// settingsView is the modal-style overlay reached via Ctrl-S. It mirrors
// the web UI's Settings tabs (currently Skills + Agents) and runs against
// the same on-disk layout — no HTTP roundtrip, no separate cache.
type settingsView struct {
	app   *tview.Application
	pages *tview.Pages
	// returnFocus is the primitive that should regain focus once the
	// overlay is dismissed (typically the chat input field).
	returnFocus tview.Primitive
	// markdown is borrowed from Run() so skill content renders with the
	// same width-aware Glamour pipeline as live chat output.
	markdown *markdownRenderer

	root   *tview.Flex
	tabs   *tview.TextView
	footer *tview.TextView

	// Tab content — each tab is a horizontal split of list (left) +
	// detail (right). The list selection drives what shows in detail.
	skillsList   *tview.List
	skillsDetail *tview.TextView
	skillsPane   tview.Primitive
	skillsData   []registrymeta.SkillInfo

	agentsList   *tview.List
	agentsDetail *tview.TextView
	agentsPane   tview.Primitive
	agentsData   []registrymeta.AgentInfo

	// configTabs holds the $EDITOR-driven tabs (Models, MCP, A2A,
	// Permissions, Squads, Remotes). Order in this slice matches the
	// trailing entries in tabNames.
	configTabs []*configTab

	pages2     *tview.Pages // body pages (one per tab)
	tabNames   []string
	activeTab  int
	focusInTab int // 0 = list, 1 = detail
}

// loadSkills delegates to registrymeta so the TUI and server share one
// definition. ListSkills sorts by encounter order across the layers;
// we re-sort alphabetically here to match the existing Settings layout.
func loadSkills() []registrymeta.SkillInfo {
	out, _ := registrymeta.ListSkills(paths.SkillsAllSearchDirs()...)
	// Sort alphabetically; the lister returns encounter order.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func loadAgents() []registrymeta.AgentInfo {
	out, _ := registrymeta.ListAgents(paths.AgentsRegistrySearchDirs()...)
	return out
}

// newSettingsView builds the overlay. It does not render anything until
// open() is called; the resulting view can be reused (open / close as
// many times as needed) and its lists are refreshed on every open.
func newSettingsView(app *tview.Application, pages *tview.Pages, returnFocus tview.Primitive, md *markdownRenderer) *settingsView {
	s := &settingsView{
		app:         app,
		pages:       pages,
		returnFocus: returnFocus,
		markdown:    md,
		tabNames:    []string{"Skills", "Agents", "Models", "MCP", "A2A", "Permissions", "Squads", "Remotes"},
	}

	// Top tab bar — rendered as a single TextView with bracketed tags
	// for the active tab. We avoid tview.Tab* primitives so the bar
	// stays simple to repaint when activeTab changes.
	s.tabs = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	s.tabs.SetBorder(true).SetTitle(" Settings (Tab cycles · Esc closes) ").SetTitleAlign(tview.AlignLeft)

	// Skills tab content
	s.skillsList = tview.NewList().ShowSecondaryText(true).SetHighlightFullLine(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	s.skillsList.SetBorder(true).SetTitle(" Installed skills ").SetTitleAlign(tview.AlignLeft)
	s.skillsDetail = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	s.skillsDetail.SetBorder(true).SetTitle(" Detail ").SetTitleAlign(tview.AlignLeft)
	s.skillsList.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		s.renderSkillDetail(idx)
	})
	s.skillsPane = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(s.skillsList, 36, 0, true).
		AddItem(s.skillsDetail, 0, 1, false)

	// Agents tab content
	s.agentsList = tview.NewList().ShowSecondaryText(true).SetHighlightFullLine(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	s.agentsList.SetBorder(true).SetTitle(" Installed agents ").SetTitleAlign(tview.AlignLeft)
	s.agentsDetail = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	s.agentsDetail.SetBorder(true).SetTitle(" Detail ").SetTitleAlign(tview.AlignLeft)
	s.agentsList.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		s.renderAgentDetail(idx)
	})
	s.agentsPane = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(s.agentsList, 36, 0, true).
		AddItem(s.agentsDetail, 0, 1, false)

	s.pages2 = tview.NewPages().
		AddPage("Skills", s.skillsPane, true, true).
		AddPage("Agents", s.agentsPane, true, false)

	// Phase D: six $EDITOR-driven config tabs. Each binds to one JSON
	// file in the config search chain; validation runs on every save.
	configDefs := []struct{ name, file, help string }{
		{"Models", "models.json", "Edit providers + models (provider/model/prices)."},
		{"MCP", "mcp_config.json", "MCP server definitions (name, command, args, env)."},
		{"A2A", "a2a_config.json", "Remote A2A peer endpoints + per-call inputs."},
		{"Permissions", "permissions.json", "Tool permission rules (Claude Code nomenclature): permissions.{allow, ask, deny} + defaultMode."},
		{"Squads", "agents.json", "Squad composition (leader + members)."},
		{"Remotes", "remote_registries.json", "Remote skill / agent / MCP / A2A registries."},
	}
	for _, def := range configDefs {
		t := newConfigTab(def.name, def.file, def.help)
		s.configTabs = append(s.configTabs, t)
		s.installConfigTabKeys(t)
		s.pages2.AddPage(def.name, t.root, true, false)
	}

	// One-line footer for transient status messages (delete confirmed,
	// attach saved, install in progress, etc.).
	s.footer = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	s.footer.SetText("")

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.tabs, 4, 0, false).
		AddItem(s.pages2, 0, 1, true).
		AddItem(s.footer, 1, 0, false)

	// Per-pane key handlers: l switches to detail, h returns to list,
	// Esc closes the overlay. The tab cycling is handled by the
	// overlay-level capture below.
	captureForList := func(list, detail tview.Primitive) func(*tcell.EventKey) *tcell.EventKey {
		return func(ev *tcell.EventKey) *tcell.EventKey {
			switch ev.Key() {
			case tcell.KeyEnter, tcell.KeyRight:
				s.focusInTab = 1
				s.app.SetFocus(detail)
				return nil
			}
			return ev
		}
	}
	captureForDetail := func(list tview.Primitive) func(*tcell.EventKey) *tcell.EventKey {
		return func(ev *tcell.EventKey) *tcell.EventKey {
			switch ev.Key() {
			case tcell.KeyLeft, tcell.KeyEsc:
				s.focusInTab = 0
				s.app.SetFocus(list)
				return nil
			}
			return ev
		}
	}
	// Wrap the generic captures with tab-specific shortcuts. Skills list
	// handles d (delete), a (attach/detach). Agents list handles e
	// (toggle enabled in agents.json) — and later C-phase keys.
	skillsGeneric := captureForList(s.skillsList, s.skillsDetail)
	s.skillsList.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'd', 'D':
			s.deleteSelectedSkill()
			return nil
		case 'a', 'A':
			s.attachSelectedSkillModal()
			return nil
		case 'i', 'I':
			s.installSkillModal()
			return nil
		}
		return skillsGeneric(ev)
	})
	s.skillsDetail.SetInputCapture(captureForDetail(s.skillsList))

	agentsGeneric := captureForList(s.agentsList, s.agentsDetail)
	s.agentsList.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'e', 'E':
			s.toggleAgentEnabled()
			return nil
		case 'i':
			s.installAgentModal()
			return nil
		case 'I':
			s.importClaudeAgentModal()
			return nil
		}
		return agentsGeneric(ev)
	})
	s.agentsDetail.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'e':
			s.editAgentEntryForm()
			return nil
		case 'i':
			s.editInstructionMd()
			return nil
		}
		return captureForDetail(s.agentsList)(ev)
	})

	return s
}

// paintTabs repaints the tab bar reflecting the active tab.
func (s *settingsView) paintTabs() {
	var sb strings.Builder
	for i, name := range s.tabNames {
		if i > 0 {
			sb.WriteString("  ")
		}
		if i == s.activeTab {
			sb.WriteString(fmt.Sprintf("[black:yellow] %s [-:-]", name))
		} else {
			sb.WriteString(fmt.Sprintf(" [aqua]%s[-] ", name))
		}
	}
	sb.WriteString("   [gray]Tab=switch tabs · Esc=close[-]")
	sb.WriteString("\n")
	switch s.tabNames[s.activeTab] {
	case "Skills":
		sb.WriteString("[gray]Skills: [aqua]Enter[-] view · [aqua]a[-] attach to agents · [aqua]d[-] delete · [aqua]i[-] install from remote[gray][-]")
	case "Agents":
		sb.WriteString("[gray]Agents: [aqua]Enter[-] view · [aqua]e[-] toggle enabled · [aqua]i[-] install remote · [aqua]I[-] import Claude · [gray]detail: [aqua]e[-] edit json · [aqua]i[-] edit instruction[-]")
	default:
		sb.WriteString("[gray]Press [aqua]e[-][gray] to edit this file via $EDITOR. Invalid JSON on save is reverted automatically.[-]")
	}
	s.tabs.SetText(sb.String())
}

func (s *settingsView) setActiveTab(i int) {
	if i < 0 || i >= len(s.tabNames) {
		return
	}
	s.activeTab = i
	s.pages2.SwitchToPage(s.tabNames[i])
	s.paintTabs()
	// Focus the primary primitive of the new tab.
	s.focusInTab = 0
	switch s.tabNames[i] {
	case "Skills":
		s.app.SetFocus(s.skillsList)
	case "Agents":
		s.app.SetFocus(s.agentsList)
	default:
		// One of the configTabs — focus its TextView so 'e' is captured.
		for _, t := range s.configTabs {
			if t.name == s.tabNames[i] {
				t.refresh()
				s.app.SetFocus(t.view)
				return
			}
		}
	}
}

// open mounts the overlay on the pages stack, refreshes its data, and
// shifts focus to the first tab's list.
func (s *settingsView) open() {
	s.skillsData = loadSkills()
	s.agentsData = loadAgents()
	s.refreshSkills()
	s.refreshAgents()
	for _, t := range s.configTabs {
		t.refresh()
	}
	s.activeTab = 0
	s.paintTabs()
	s.pages2.SwitchToPage("Skills")
	s.pages.AddPage("settings", s.root, true, true)
	s.app.SetFocus(s.skillsList)
}

func (s *settingsView) close() {
	s.pages.RemovePage("settings")
	if s.returnFocus != nil {
		s.app.SetFocus(s.returnFocus)
	}
}

// handleKey processes overlay-level shortcuts. Returns nil when the
// event has been consumed; otherwise passes it through.
func (s *settingsView) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEsc:
		// Esc on a detail pane is handled by per-primitive captures
		// above (goes back to the list). At the overlay level, Esc
		// only fires when focus is on a list, which means "close".
		s.close()
		return nil
	case tcell.KeyTab:
		s.setActiveTab((s.activeTab + 1) % len(s.tabNames))
		return nil
	case tcell.KeyBacktab:
		s.setActiveTab((s.activeTab + len(s.tabNames) - 1) % len(s.tabNames))
		return nil
	}
	return ev
}

func (s *settingsView) refreshSkills() {
	s.skillsList.Clear()
	for _, sk := range s.skillsData {
		desc := sk.Description
		if desc == "" {
			desc = "(no description)"
		}
		s.skillsList.AddItem(
			fmt.Sprintf("%s [gray](%s)[-]", sk.Name, sk.Source),
			truncateLine(desc, 80),
			0, nil,
		)
	}
	if len(s.skillsData) > 0 {
		s.skillsList.SetCurrentItem(0)
		s.renderSkillDetail(0)
	} else {
		s.skillsDetail.SetText("[gray]No skills installed.[-]\n\nSkills live under:\n  • " + strings.Join(paths.SkillsAllSearchDirs(), "\n  • "))
	}
}

func (s *settingsView) renderSkillDetail(idx int) {
	if idx < 0 || idx >= len(s.skillsData) {
		s.skillsDetail.SetText("")
		return
	}
	sk := s.skillsData[idx]
	data, err := os.ReadFile(sk.Path)
	if err != nil {
		s.skillsDetail.SetText(fmt.Sprintf("[red]read SKILL.md: %v[-]", err))
		return
	}
	header := fmt.Sprintf("[::b]%s[-]  [gray]%s[-]\n%s\n\n[gray]Path:[-] %s\n",
		sk.Name, sk.Source, sk.Description, sk.Path)
	if sk.Author != "" {
		header += fmt.Sprintf("[gray]Author:[-] %s\n", sk.Author)
	}
	if len(sk.Tags) > 0 {
		header += fmt.Sprintf("[gray]Tags:[-] %s\n", strings.Join(sk.Tags, ", "))
	}
	header += strings.Repeat("─", 40) + "\n\n"

	// Render the SKILL.md body via the shared markdown pipeline so the
	// formatting matches the live chat pane.
	_, _, w, _ := s.skillsDetail.GetInnerRect()
	if w <= 0 {
		w = 80
	}
	body := s.markdown.render(string(data), w)
	s.skillsDetail.SetText("")
	fmt.Fprint(s.skillsDetail, header)
	fmt.Fprint(tview.ANSIWriter(s.skillsDetail), body)
	s.skillsDetail.ScrollToBeginning()
}

func (s *settingsView) refreshAgents() {
	s.agentsList.Clear()
	for _, a := range s.agentsData {
		desc := a.Entry.Description
		if desc == "" {
			desc = "(no description)"
		}
		tag := ""
		if a.IsBuiltin() {
			tag = " [aqua]builtin[-]"
		}
		s.agentsList.AddItem(
			fmt.Sprintf("%s%s [gray](%s)[-]", a.Name(), tag, a.Source),
			truncateLine(desc, 80),
			0, nil,
		)
	}
	if len(s.agentsData) > 0 {
		s.agentsList.SetCurrentItem(0)
		s.renderAgentDetail(0)
	} else {
		s.agentsDetail.SetText("[gray]No agents installed.[-]\n\nAgents live under:\n  • " + strings.Join(paths.AgentsRegistrySearchDirs(), "\n  • "))
	}
}

func (s *settingsView) renderAgentDetail(idx int) {
	if idx < 0 || idx >= len(s.agentsData) {
		s.agentsDetail.SetText("")
		return
	}
	a := s.agentsData[idx]

	// agent.json (pretty-printed)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[::b]%s[-]  [gray]%s[-]\n", a.Name(), a.Source))
	if a.IsBuiltin() {
		sb.WriteString("[aqua]builtin[-]\n")
	}
	if a.Entry.Description != "" {
		sb.WriteString(a.Entry.Description + "\n")
	}
	sb.WriteString("\n[gray]Path:[-] " + a.Dir + "\n\n")

	if len(a.Entry.Tools) > 0 {
		sb.WriteString("[::b]Tools[-]\n")
		for _, t := range a.Entry.Tools {
			sb.WriteString("  • " + t + "\n")
		}
		sb.WriteString("\n")
	}
	if len(a.Entry.Skills) > 0 {
		sb.WriteString("[::b]Skills[-]\n")
		for _, sk := range a.Entry.Skills {
			sb.WriteString("  • " + sk + "\n")
		}
		sb.WriteString("\n")
	}

	instr := a.InstructionPath()
	if data, err := os.ReadFile(instr); err == nil {
		sb.WriteString(strings.Repeat("─", 40) + "\n[::b]instruction.md[-]\n\n")
		s.agentsDetail.SetText("")
		fmt.Fprint(s.agentsDetail, sb.String())
		_, _, w, _ := s.agentsDetail.GetInnerRect()
		if w <= 0 {
			w = 80
		}
		body := s.markdown.render(string(data), w)
		fmt.Fprint(tview.ANSIWriter(s.agentsDetail), body)
	} else {
		sb.WriteString("[gray](no instruction.md — uses registry/agents/default.md)[-]\n")
		s.agentsDetail.SetText(sb.String())
	}
	s.agentsDetail.ScrollToBeginning()
}

// truncateLine clips a single-line string at n chars and appends an
// ellipsis when truncated. Used to keep list secondary text from
// overflowing the visible width.
func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
