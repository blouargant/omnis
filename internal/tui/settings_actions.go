package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/blouargant/yoke/internal/claudeformat"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
	"github.com/blouargant/yoke/internal/registrymeta"
)

// flashStatus writes a one-line status message to the overlay footer.
// Callers use it to confirm a write or surface a failure inline rather
// than blocking on a modal.
func (s *settingsView) flashStatus(format string, args ...any) {
	s.footer.SetText(fmt.Sprintf(format, args...))
}

// reloadAll re-reads both lists from disk. Use after any disk-touching
// operation so the views reflect the new state.
func (s *settingsView) reloadAll() {
	s.skillsData = loadSkills()
	s.agentsData = loadAgents()
	s.refreshSkills()
	s.refreshAgents()
}

// closeAndFocus removes the named overlay page and restores focus.
func (s *settingsView) closeAndFocus(pageName string, focus tview.Primitive) {
	s.pages.RemovePage(pageName)
	s.app.SetFocus(focus)
}

// containsString / removeString are tiny helpers used by attach/detach.
func containsString(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

func removeString(list []string, name string) []string {
	out := list[:0]
	for _, s := range list {
		if s != name {
			out = append(out, s)
		}
	}
	return out
}

// ── Skills actions ────────────────────────────────────────────────────────

func (s *settingsView) deleteSelectedSkill() {
	idx := s.skillsList.GetCurrentItem()
	if idx < 0 || idx >= len(s.skillsData) {
		return
	}
	sk := s.skillsData[idx]
	if sk.Source == "system" {
		s.flashStatus("[red]cannot delete system skill (read-only layer)[-]")
		return
	}
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Delete skill %q from layer %s?\n\n%s", sk.Name, sk.Source, sk.Dir)).
		AddButtons([]string{"Delete", "Cancel"})
	modal.SetDoneFunc(func(_ int, label string) {
		s.closeAndFocus("confirm-delete-skill", s.skillsList)
		if label != "Delete" {
			return
		}
		if err := os.RemoveAll(sk.Dir); err != nil {
			s.flashStatus("[red]delete failed: %v[-]", err)
			return
		}
		s.flashStatus("[green]deleted skill %s[-]", sk.Name)
		s.reloadAll()
	})
	s.pages.AddPage("confirm-delete-skill", modal, false, true)
	s.app.SetFocus(modal)
}

// attachSelectedSkillModal opens a checkbox form listing all installed
// agents. Pre-fills the checkboxes with each agent's current link state
// for the selected skill. Submit writes the diff to every changed
// agent.json.
func (s *settingsView) attachSelectedSkillModal() {
	idx := s.skillsList.GetCurrentItem()
	if idx < 0 || idx >= len(s.skillsData) {
		return
	}
	sk := s.skillsData[idx]
	if len(s.agentsData) == 0 {
		s.flashStatus("[yellow]no agents installed to attach to[-]")
		return
	}

	form := tview.NewForm()
	form.SetTitle(fmt.Sprintf(" Skill %q · attach to agents ", sk.Name)).SetBorder(true)

	states := make([]bool, len(s.agentsData))
	for i, a := range s.agentsData {
		states[i] = containsString(a.Entry.Skills, sk.Name)
	}
	for i, a := range s.agentsData {
		i := i
		label := a.Name()
		if a.IsBuiltin() {
			label += " (builtin)"
		}
		label += fmt.Sprintf(" [%s]", a.Source)
		form.AddCheckbox(label, states[i], func(checked bool) { states[i] = checked })
	}

	form.AddButton("Save", func() {
		var changed []string
		for i, want := range states {
			cur := containsString(s.agentsData[i].Entry.Skills, sk.Name)
			if want == cur {
				continue
			}
			entry := s.agentsData[i].Entry
			if want {
				entry.Skills = append(append([]string{}, entry.Skills...), sk.Name)
			} else {
				entry.Skills = removeString(append([]string{}, entry.Skills...), sk.Name)
			}
			if err := registrymeta.WriteAgentEntry(s.agentsData[i].Dir, entry); err != nil {
				s.flashStatus("[red]save %s: %v[-]", s.agentsData[i].Name(), err)
				return
			}
			changed = append(changed, s.agentsData[i].Name())
		}
		s.closeAndFocus("attach-skill", s.skillsList)
		if len(changed) > 0 {
			s.flashStatus("[green]updated %d agent(s): %s[-]", len(changed), strings.Join(changed, ", "))
		} else {
			s.flashStatus("[gray]no changes[-]")
		}
		s.reloadAll()
	})
	form.AddButton("Cancel", func() { s.closeAndFocus("attach-skill", s.skillsList) })
	form.SetCancelFunc(func() { s.closeAndFocus("attach-skill", s.skillsList) })

	height := 5 + len(s.agentsData) + 2
	if height > 20 {
		height = 20
	}
	s.pages.AddPage("attach-skill", centered(form, 60, height), true, true)
	s.app.SetFocus(form)
}

// installSkillModal walks the user through a two-step pick: first the
// remote registry (filtered to those that serve skills), then the
// remote skill to install. InstallSkill writes into the per-user
// registry write dir.
func (s *settingsView) installSkillModal() {
	regs, err := loadSkillRegistries()
	if err != nil {
		s.flashStatus("[red]load remote registries: %v[-]", err)
		return
	}
	if len(regs) == 0 {
		s.flashStatus("[yellow]no remote skill registries configured[-]")
		return
	}
	list := tview.NewList().SetHighlightFullLine(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	list.SetBorder(true).SetTitle(" Pick a remote registry · Esc to cancel ")
	for _, r := range regs {
		r := r
		title := r.Name
		if title == "" {
			title = r.URL
		}
		list.AddItem(title, fmt.Sprintf("[gray]%s · %s[-]", r.NormalizedKind(), r.URL), 0,
			func() { s.browseRemoteSkills(r) })
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			s.closeAndFocus("install-skill-pick-registry", s.skillsList)
			return nil
		}
		return ev
	})

	s.pages.AddPage("install-skill-pick-registry", centered(list, 70, 12), true, true)
	s.app.SetFocus(list)
}

// browseRemoteSkills opens the remote, fetches the skill listing, and
// shows it. Each Enter triggers InstallSkill and closes both modals.
func (s *settingsView) browseRemoteSkills(reg registries.Registry) {
	ref, err := registries.ParseRepoRef(reg.URL, reg.Provider)
	if err != nil {
		s.flashStatus("[red]parse %s: %v[-]", reg.URL, err)
		return
	}
	writeDir := paths.SkillsRegistryWriteDir()
	items, err := registries.BrowseSkills(ref, reg.Token, writeDir)
	if err != nil {
		s.flashStatus("[red]browse %s: %v[-]", reg.Name, err)
		return
	}
	if len(items) == 0 {
		s.flashStatus("[yellow]%s: no skills found[-]", reg.Name)
		return
	}

	list := tview.NewList().SetHighlightFullLine(true).
		ShowSecondaryText(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	list.SetBorder(true).SetTitle(fmt.Sprintf(" %s · pick a skill (Esc to cancel) ", reg.Name))

	for _, it := range items {
		it := it
		title := it.Name
		if it.Installed {
			title += " [aqua](installed)[-]"
		}
		desc := it.Description
		if desc == "" {
			desc = "(no description)"
		}
		list.AddItem(title, truncateLine(desc, 80), 0, func() {
			s.installRemoteSkill(reg, ref, it)
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			s.closeAndFocus("install-skill-browse", list)
			return nil
		}
		return ev
	})
	s.pages.AddPage("install-skill-browse", centered(list, 80, 20), true, true)
	s.app.SetFocus(list)
}

func (s *settingsView) installRemoteSkill(reg registries.Registry, ref registries.RepoRef, it registries.SkillInfo) {
	writeDir := paths.SkillsRegistryWriteDir()
	if err := os.MkdirAll(writeDir, 0o755); err != nil {
		s.flashStatus("[red]create write dir: %v[-]", err)
		return
	}
	go func() {
		s.app.QueueUpdateDraw(func() {
			s.flashStatus("[gray]installing %s from %s…[-]", it.Name, reg.Name)
		})
		name, err := registries.InstallSkill(ref, reg.Token, it.DirPath, writeDir)
		s.app.QueueUpdateDraw(func() {
			if err != nil {
				s.flashStatus("[red]install failed: %v[-]", err)
				return
			}
			s.closeAndFocus("install-skill-browse", s.skillsList)
			s.closeAndFocus("install-skill-pick-registry", s.skillsList)
			s.flashStatus("[green]installed skill %s[-]", name)
			s.reloadAll()
		})
	}()
}

// ── Agents actions ────────────────────────────────────────────────────────

// toggleAgentEnabled flips the selected agent's presence in the
// `agents` list of agents.json. Adds if absent, removes if present.
// Writes go to the existing file's layer when possible; otherwise to
// ConfigWriteDir.
func (s *settingsView) toggleAgentEnabled() {
	idx := s.agentsList.GetCurrentItem()
	if idx < 0 || idx >= len(s.agentsData) {
		return
	}
	a := s.agentsData[idx]
	configPath := paths.FindConfig("agents.json")
	writePath := configPath
	if writePath == "" {
		writePath = filepath.Join(paths.ConfigWriteDir(), "agents.json")
	}

	cfg := map[string]any{}
	if data, err := os.ReadFile(writePath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	rawAgents, _ := cfg["agents"].([]any)
	var names []string
	for _, item := range rawAgents {
		if s, ok := item.(string); ok {
			names = append(names, s)
		}
	}
	already := containsString(names, a.Name())
	if already {
		names = removeString(names, a.Name())
	} else {
		names = append(names, a.Name())
	}
	cfg["agents"] = stringsToAny(names)

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		s.flashStatus("[red]marshal agents.json: %v[-]", err)
		return
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		s.flashStatus("[red]mkdir: %v[-]", err)
		return
	}
	if err := os.WriteFile(writePath, out, 0o644); err != nil {
		s.flashStatus("[red]write %s: %v[-]", writePath, err)
		return
	}
	if already {
		s.flashStatus("[green]disabled %s in agents.json[-]", a.Name())
	} else {
		s.flashStatus("[green]enabled %s in agents.json (reload via Ctrl-R)[-]", a.Name())
	}
}

// editAgentEntryForm renders a form bound to the selected agent's
// AgentEntry. Save writes back to that agent's agent.json.
func (s *settingsView) editAgentEntryForm() {
	idx := s.agentsList.GetCurrentItem()
	if idx < 0 || idx >= len(s.agentsData) {
		return
	}
	a := s.agentsData[idx]
	entry := a.Entry

	form := tview.NewForm()
	form.SetTitle(fmt.Sprintf(" Edit %s · agent.json (%s) ", a.Name(), a.Source)).SetBorder(true)
	form.AddInputField("name", entry.Name, 40, nil, func(v string) { entry.Name = v })
	form.AddInputField("description", entry.Description, 60, nil, func(v string) { entry.Description = v })
	form.AddInputField("model_ref", entry.ModelRef, 30, nil, func(v string) { entry.ModelRef = v })
	form.AddInputField("tools (comma-separated)", strings.Join(entry.Tools, ", "), 60, nil, func(v string) {
		entry.Tools = parseCSV(v)
	})
	form.AddInputField("skills (comma-separated)", strings.Join(entry.Skills, ", "), 60, nil, func(v string) {
		entry.Skills = parseCSV(v)
	})
	form.AddInputField("a2a_agents (comma-separated)", strings.Join(entry.A2AAgents, ", "), 60, nil, func(v string) {
		entry.A2AAgents = parseCSV(v)
	})
	form.AddInputField("mcp_servers (comma-separated)", strings.Join(entry.MCPServers, ", "), 60, nil, func(v string) {
		entry.MCPServers = parseCSV(v)
	})

	form.AddButton("Save", func() {
		if err := registrymeta.WriteAgentEntry(a.Dir, entry); err != nil {
			s.flashStatus("[red]save %s: %v[-]", a.Name(), err)
			return
		}
		s.closeAndFocus("edit-agent", s.agentsDetail)
		s.flashStatus("[green]saved %s (reload via Ctrl-R)[-]", a.Name())
		s.reloadAll()
	})
	form.AddButton("Cancel", func() { s.closeAndFocus("edit-agent", s.agentsDetail) })
	form.SetCancelFunc(func() { s.closeAndFocus("edit-agent", s.agentsDetail) })

	s.pages.AddPage("edit-agent", centered(form, 80, 18), true, true)
	s.app.SetFocus(form)
}

// editInstructionMd asks the user how they want to edit (or create) the
// agent's instruction.md, then routes to either $EDITOR or an in-TUI
// textarea. Both write back to the same file on save.
func (s *settingsView) editInstructionMd() {
	idx := s.agentsList.GetCurrentItem()
	if idx < 0 || idx >= len(s.agentsData) {
		return
	}
	a := s.agentsData[idx]
	path := a.InstructionPath()

	modal := tview.NewModal().
		SetText(fmt.Sprintf("Edit %s\n\nChoose editor:", path)).
		AddButtons([]string{"$EDITOR", "In-TUI", "Cancel"})
	modal.SetDoneFunc(func(_ int, label string) {
		s.closeAndFocus("edit-instr-pick", s.agentsDetail)
		switch label {
		case "$EDITOR":
			s.editInstructionExternal(path)
		case "In-TUI":
			s.editInstructionInline(path, a.Name())
		}
	})
	s.pages.AddPage("edit-instr-pick", modal, false, true)
	s.app.SetFocus(modal)
}

// editInstructionExternal suspends the tview app, spawns $EDITOR on the
// file, then resumes. The screen is restored automatically on return.
func (s *settingsView) editInstructionExternal(path string) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.flashStatus("[red]mkdir: %v[-]", err)
		return
	}
	// Ensure the file exists so editors that refuse non-existent files
	// (rare but possible) succeed on first edit.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, nil, 0o644)
	}
	s.app.Suspend(func() {
		cmd := exec.Command(editor, path)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "editor exited with error: %v\n", err)
		}
	})
	s.flashStatus("[green]edited %s[-]", path)
	s.reloadAll()
}

// editInstructionInline opens a tview.TextArea pre-filled with the
// file's contents. Ctrl-S saves; Esc cancels.
func (s *settingsView) editInstructionInline(path, agentName string) {
	data, _ := os.ReadFile(path) // missing file → empty editor
	area := tview.NewTextArea().SetText(string(data), false).SetWrap(false)
	area.SetBorder(true).SetTitle(fmt.Sprintf(" %s · instruction.md (Ctrl-S save · Esc cancel) ", agentName))

	area.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlS:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				s.flashStatus("[red]mkdir: %v[-]", err)
				return nil
			}
			if err := os.WriteFile(path, []byte(area.GetText()), 0o644); err != nil {
				s.flashStatus("[red]write %s: %v[-]", path, err)
				return nil
			}
			s.closeAndFocus("edit-instr-inline", s.agentsDetail)
			s.flashStatus("[green]saved %s[-]", path)
			s.reloadAll()
			return nil
		case tcell.KeyEsc:
			s.closeAndFocus("edit-instr-inline", s.agentsDetail)
			return nil
		}
		return ev
	})

	s.pages.AddPage("edit-instr-inline", area, true, true)
	s.app.SetFocus(area)
}

// installAgentModal mirrors installSkillModal but for agents.
func (s *settingsView) installAgentModal() {
	regs, err := loadAgentRegistries()
	if err != nil {
		s.flashStatus("[red]load remote registries: %v[-]", err)
		return
	}
	if len(regs) == 0 {
		s.flashStatus("[yellow]no remote agent registries configured[-]")
		return
	}
	list := tview.NewList().SetHighlightFullLine(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	list.SetBorder(true).SetTitle(" Pick a remote registry · Esc to cancel ")
	for _, r := range regs {
		r := r
		title := r.Name
		if title == "" {
			title = r.URL
		}
		list.AddItem(title, fmt.Sprintf("[gray]%s · %s[-]", r.NormalizedKind(), r.URL), 0,
			func() { s.browseRemoteAgents(r) })
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			s.closeAndFocus("install-agent-pick-registry", s.agentsList)
			return nil
		}
		return ev
	})
	s.pages.AddPage("install-agent-pick-registry", centered(list, 70, 12), true, true)
	s.app.SetFocus(list)
}

func (s *settingsView) browseRemoteAgents(reg registries.Registry) {
	ref, err := registries.ParseRepoRef(reg.URL, reg.Provider)
	if err != nil {
		s.flashStatus("[red]parse %s: %v[-]", reg.URL, err)
		return
	}
	writeDir := paths.AgentsRegistryWriteDir()
	items, err := registries.BrowseAgents(ref, reg.Token, writeDir)
	if err != nil {
		s.flashStatus("[red]browse %s: %v[-]", reg.Name, err)
		return
	}
	if len(items) == 0 {
		s.flashStatus("[yellow]%s: no agents found[-]", reg.Name)
		return
	}

	list := tview.NewList().SetHighlightFullLine(true).
		ShowSecondaryText(true).
		SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)
	list.SetBorder(true).SetTitle(fmt.Sprintf(" %s · pick an agent (Esc to cancel) ", reg.Name))
	for _, it := range items {
		it := it
		title := it.Name
		if it.Installed {
			title += " [aqua](installed)[-]"
		}
		if it.Format == "claude" {
			title += " [magenta](claude)[-]"
		}
		desc := it.Description
		if desc == "" {
			desc = "(no description)"
		}
		list.AddItem(title, truncateLine(desc, 80), 0, func() {
			s.installRemoteAgent(reg, ref, it)
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			s.closeAndFocus("install-agent-browse", list)
			return nil
		}
		return ev
	})
	s.pages.AddPage("install-agent-browse", centered(list, 80, 20), true, true)
	s.app.SetFocus(list)
}

func (s *settingsView) installRemoteAgent(reg registries.Registry, ref registries.RepoRef, it registries.AgentInfo) {
	writeDir := paths.AgentsRegistryWriteDir()
	if err := os.MkdirAll(writeDir, 0o755); err != nil {
		s.flashStatus("[red]create write dir: %v[-]", err)
		return
	}
	go func() {
		s.app.QueueUpdateDraw(func() {
			s.flashStatus("[gray]installing %s from %s…[-]", it.Name, reg.Name)
		})
		name, err := registries.InstallAgent(ref, reg.Token, it.DirPath, writeDir)
		s.app.QueueUpdateDraw(func() {
			if err != nil {
				s.flashStatus("[red]install failed: %v[-]", err)
				return
			}
			s.closeAndFocus("install-agent-browse", s.agentsList)
			s.closeAndFocus("install-agent-pick-registry", s.agentsList)
			s.flashStatus("[green]installed agent %s[-]", name)
			s.reloadAll()
		})
	}()
}

// importClaudeAgentModal asks for a local file path, runs the
// Claude-format parser, and installs each parsed agent into the local
// registry. Mirrors `yoke import-agent <file>` from the CLI.
func (s *settingsView) importClaudeAgentModal() {
	form := tview.NewForm()
	form.SetTitle(" Import Claude-format agent ").SetBorder(true)
	pathField := tview.NewInputField().SetLabel("Path: ").SetFieldWidth(60)
	form.AddFormItem(pathField)
	form.AddButton("Import", func() {
		path := strings.TrimSpace(pathField.GetText())
		if path == "" {
			s.flashStatus("[yellow]path required[-]")
			return
		}
		content, err := os.ReadFile(path)
		if err != nil {
			s.flashStatus("[red]read %s: %v[-]", path, err)
			return
		}
		defs, err := claudeformat.Parse(content)
		if err != nil {
			s.flashStatus("[red]parse: %v[-]", err)
			return
		}
		writeDir := paths.AgentsRegistryWriteDir()
		var installed []string
		for _, def := range defs {
			if err := claudeformat.InstallAgent(def, writeDir); err != nil {
				s.flashStatus("[red]install %s: %v[-]", def.Name, err)
				return
			}
			installed = append(installed, def.Name)
		}
		s.closeAndFocus("import-claude", s.agentsList)
		s.flashStatus("[green]imported %d agent(s): %s[-]", len(installed), strings.Join(installed, ", "))
		s.reloadAll()
	})
	form.AddButton("Cancel", func() { s.closeAndFocus("import-claude", s.agentsList) })
	form.SetCancelFunc(func() { s.closeAndFocus("import-claude", s.agentsList) })

	s.pages.AddPage("import-claude", centered(form, 80, 7), true, true)
	s.app.SetFocus(form)
}

// ── Helpers ───────────────────────────────────────────────────────────────

// loadSkillRegistries returns the remote-registries entries that serve
// skills, filtered out of the full list in remote_registries.json.
func loadSkillRegistries() ([]registries.Registry, error) {
	all, err := registries.LoadRegistries(registries.ReadConfigPath())
	if err != nil {
		return nil, err
	}
	var out []registries.Registry
	for _, r := range all {
		if r.Serves(registries.KindSkills) {
			out = append(out, r)
		}
	}
	return out, nil
}

func loadAgentRegistries() ([]registries.Registry, error) {
	all, err := registries.LoadRegistries(registries.ReadConfigPath())
	if err != nil {
		return nil, err
	}
	var out []registries.Registry
	for _, r := range all {
		if r.Serves(registries.KindAgents) {
			out = append(out, r)
		}
	}
	return out, nil
}

// parseCSV splits a comma-separated string into trimmed non-empty items.
func parseCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stringsToAny converts []string → []any for JSON re-marshalling so the
// resulting agents.json keeps its array shape.
func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
