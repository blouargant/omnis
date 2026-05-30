// Package usercommands owns the on-disk format, validation, and persistence
// of user-defined slash commands (the per-user user_commands.json file). It is
// shared between the HTTP server's web-UI routes and the agent-side
// Helper command install, so both surfaces agree on the schema,
// the name rules, and the set of reserved built-in command names.
package usercommands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/blouargant/yoke/internal/paths"
)

// Command is a single user-defined slash command. Name is the lookup key
// (without the leading slash); Prompt is the template body sent to the agent
// when the command is invoked, with $1..$N (positional) and $* (all remaining
// args) substituted client-side.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Args        string `json:"args,omitempty"`
	Prompt      string `json:"prompt"`
}

// File is the JSON envelope persisted to user_commands.json.
type File struct {
	Commands []Command `json:"commands"`
}

// ErrNotFound is returned by Delete when no command matches the name.
var ErrNotFound = errors.New("not found")

// DefaultPath returns the per-user user_commands.json path under the config
// write dir ($YOKE_HOME). The web UI editor and the crawler both target it.
func DefaultPath() string {
	return filepath.Join(paths.ConfigWriteDir(), "user_commands.json")
}

// Load reads the commands from path. A missing or unparseable file yields a
// nil slice (treated as empty), never an error — callers start from scratch.
func Load(path string) []Command {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	return f.Commands
}

// Save writes cmds to path (sorted by name), creating parent dirs as needed.
func Save(path string, cmds []Command) error {
	if cmds == nil {
		cmds = []Command{}
	}
	sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	data, err := json.MarshalIndent(File{Commands: cmds}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Upsert inserts or replaces cmd (matched by normalised name) in the file at
// path. originalName, when set and different from cmd's name, drops the old
// entry first (a rename). Returns the resulting list, whether the command was
// newly added (vs. replaced), and any error. Callers should Validate first.
func Upsert(path string, cmd Command, originalName string) (cmds []Command, added bool, err error) {
	cmds = Load(path)
	target := NormName(cmd.Name)
	cmd.Name = target
	orig := NormName(originalName)
	if orig != "" && orig != target {
		cmds = removeByName(cmds, orig)
	}
	replaced := false
	for i := range cmds {
		if NormName(cmds[i].Name) == target {
			cmds[i] = cmd
			replaced = true
			break
		}
	}
	if !replaced {
		cmds = append(cmds, cmd)
	}
	if err := Save(path, cmds); err != nil {
		return nil, false, err
	}
	return cmds, !replaced, nil
}

// Delete removes the command with the given name from the file at path.
// Returns the resulting list or ErrNotFound when nothing matched.
func Delete(path, name string) ([]Command, error) {
	cmds := Load(path)
	target := NormName(name)
	out := removeByName(cmds, target)
	if len(out) == len(cmds) {
		return cmds, ErrNotFound
	}
	if err := Save(path, out); err != nil {
		return nil, err
	}
	return out, nil
}

func removeByName(cmds []Command, name string) []Command {
	out := cmds[:0]
	for _, c := range cmds {
		if NormName(c.Name) != name {
			out = append(out, c)
		}
	}
	return out
}

// NormName normalises a command name: trims space, drops a leading slash, and
// lowercases. It is the canonical lookup key.
func NormName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	return strings.ToLower(s)
}

// nameRe matches the allowed shape for a command name: alphanumerics, hyphens,
// and underscores; 1..40 chars. Keeps the slash menu sane and avoids ambiguity
// with prompt content.
var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

// ReservedNames are the built-in commands handled in app.js. User commands
// cannot shadow them.
var ReservedNames = map[string]struct{}{
	"help":         {},
	"compress":     {},
	"create-skill": {},
	"update-skill": {},
	"status":       {},
	"learn":        {},
	"learn-now":    {},
}

// Validate normalises and checks c in place, returning an error describing the
// first problem. Mirrors the rules enforced by the web-UI command editor.
func Validate(c *Command) error {
	c.Name = NormName(c.Name)
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("name %q must be 1-40 chars of letters, digits, '-' or '_'", c.Name)
	}
	if _, ok := ReservedNames[c.Name]; ok {
		return fmt.Errorf("name %q is reserved by a built-in command", c.Name)
	}
	c.Description = strings.TrimSpace(c.Description)
	c.Args = strings.TrimSpace(c.Args)
	c.Prompt = strings.TrimRight(c.Prompt, "\n")
	if c.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	return nil
}
