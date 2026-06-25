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

	"github.com/blouargant/omnis/internal/paths"
)

// configTab is one Settings tab backed by a single JSON config file on
// disk. It renders the current contents in a scrollable read-only pane
// and exposes a single "edit" action that hands off to $EDITOR.
// Validation runs on return; the previous content is restored when the
// edit produces invalid JSON so the live config never drifts into a
// broken state.
type configTab struct {
	name     string // tab label (matches s.tabNames entry)
	filename string // base filename, e.g. "agents.json"
	help     string // one-line description shown above the body
	view     *tview.TextView
	root     *tview.Flex
}

// configFileExists reports whether at least one layer of the config
// search chain has the requested file. Used to gate the "(file does not
// exist yet)" placeholder in the view.
func configFileExists(filename string) bool {
	return paths.FindConfig(filename) != ""
}

// resolveConfigWritePath chooses the path to read + write. If any layer
// of the chain already holds the file, we honour that path (edits in
// place — matches the behaviour of the server's layer-aware editor).
// Otherwise the file lands at ConfigWriteDir/<filename>.
func resolveConfigWritePath(filename string) string {
	if p := paths.FindConfig(filename); p != "" {
		return p
	}
	return filepath.Join(paths.ConfigWriteDir(), filename)
}

// newConfigTab constructs the primitives for one config-backed tab.
// The returned tab still needs its `e` keybinding wired against the
// owning settingsView (newSettingsView does this so the closure can
// call s.editConfigFile).
func newConfigTab(name, filename, help string) *configTab {
	view := tview.NewTextView().
		SetDynamicColors(false).
		SetScrollable(true).
		SetWrap(true)
	view.SetBorder(true).SetTitle(fmt.Sprintf(" %s · %s ", name, filename)).SetTitleAlign(tview.AlignLeft)

	hint := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	hint.SetText("[gray]" + help + "  ·  [aqua]e[-][gray] edit via $EDITOR · validates on save[-]")

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(hint, 1, 0, false).
		AddItem(view, 0, 1, true)

	return &configTab{
		name:     name,
		filename: filename,
		help:     help,
		view:     view,
		root:     root,
	}
}

// refresh re-reads the file from disk and re-renders the view. Safe to
// call any time — missing files render as a placeholder pointing at
// where the editor will create the file.
func (t *configTab) refresh() {
	if !configFileExists(t.filename) {
		t.view.SetText(fmt.Sprintf("(no %s anywhere in the config chain — press 'e' to create at %s)",
			t.filename, resolveConfigWritePath(t.filename)))
		return
	}
	path := resolveConfigWritePath(t.filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.view.SetText(fmt.Sprintf("error reading %s: %v", path, err))
		return
	}
	// Pretty-print if valid JSON so the user always sees a consistent
	// layout (the file on disk may have been hand-edited).
	var parsed any
	if err := json.Unmarshal(data, &parsed); err == nil {
		if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
			t.view.SetText(string(pretty))
			t.view.ScrollToBeginning()
			return
		}
	}
	t.view.SetText(string(data))
	t.view.ScrollToBeginning()
}

// editConfigFile suspends the tview app, runs $EDITOR (or vi as a
// fallback) on the file, then validates the result. On invalid JSON
// the previous content is restored before the function returns.
func (s *settingsView) editConfigFile(t *configTab) {
	path := resolveConfigWritePath(t.filename)
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.flashStatus("[red]mkdir %s: %v[-]", filepath.Dir(path), err)
		return
	}

	// Capture the current bytes so we can roll back on parse failure.
	backup, _ := os.ReadFile(path)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Seed the file with an empty object so the editor opens onto
		// a valid starting point.
		_ = os.WriteFile(path, []byte("{}\n"), 0o644)
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

	after, err := os.ReadFile(path)
	if err != nil {
		s.flashStatus("[red]read after edit: %v[-]", err)
		t.refresh()
		return
	}
	var parsed any
	if err := json.Unmarshal(after, &parsed); err != nil {
		// Restore the previous content so the live runtime is not
		// affected by a half-finished edit. The user can re-press 'e'
		// to fix the JSON.
		if backup != nil {
			_ = os.WriteFile(path, backup, 0o644)
		} else {
			_ = os.Remove(path)
		}
		s.flashStatus("[red]invalid JSON in %s, changes reverted: %v[-]", t.filename, err)
		t.refresh()
		return
	}
	t.refresh()
	s.flashStatus("[green]saved %s (reload via Ctrl-R)[-]", t.filename)
}

// installConfigTabKeys wires the 'e' (edit) key on a config tab's view.
// Called from newSettingsView once the settingsView pointer is known.
func (s *settingsView) installConfigTabKeys(t *configTab) {
	t.view.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'e', 'E':
			s.editConfigFile(t)
			return nil
		}
		return ev
	})
}
