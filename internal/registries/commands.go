package registries

import (
	"fmt"
	"path"
	"strings"

	"github.com/blouargant/omnis/internal/claudeformat"
)

// CommandsDir is the conventional directory name inside a remote registry
// that holds Claude Code-style slash-command markdown files. The browser
// discovers .md files anywhere in the tree, but the default registry layout
// keeps them under this prefix to mirror Anthropic's own ~/.claude/commands/
// convention.
const CommandsDir = "commands"

// BrowseCommands discovers all Claude Code slash-command markdown files in a
// remote registry. Each returned CommandInfo is annotated with Installed=true
// when a command of the same name already exists in the user commands set
// (provided via the installed set).
//
// A file is treated as a command when its path ends in .md and its leaf name
// (without the .md suffix) is a valid command identifier. Files named
// README.md / readme.md / NOTES.md are skipped.
func BrowseCommands(ref RepoRef, token string, installed map[string]bool) ([]CommandInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	var commands []CommandInfo
	for _, e := range entries {
		if e.Path == "__truncated__" {
			commands = append(commands, CommandInfo{Name: "__truncated__", DirPath: "__truncated__"})
			continue
		}
		if e.Type != "blob" || !strings.HasSuffix(e.Path, ".md") {
			continue
		}
		base := path.Base(e.Path)
		nameCandidate := strings.TrimSuffix(base, ".md")
		// Skip docs (README, NOTES, instruction, etc.)
		lc := strings.ToLower(nameCandidate)
		if lc == "readme" || lc == "notes" || lc == "instruction" || lc == "license" {
			continue
		}
		if !SkillNameRe.MatchString(nameCandidate) {
			continue
		}

		dir := path.Dir(e.Path)
		var group string
		if dir != "." && dir != "/" {
			group = dir
		}

		ci := CommandInfo{
			Name:    nameCandidate,
			DirPath: e.Path,
			Group:   group,
		}

		rawBody, status, err := ref.RawFile(e.Path, token)
		if err == nil && status == 200 {
			def, parseErr := claudeformat.ParseCommandMarkdown(rawBody)
			if parseErr == nil && def != nil {
				if def.Name != "" {
					ci.Name = def.Name
				}
				ci.Description = def.Description
				ci.ArgumentHint = def.ArgumentHint
			}
		}

		if installed != nil {
			if _, ok := installed[strings.ToLower(ci.Name)]; ok {
				ci.Installed = true
			}
		}

		commands = append(commands, ci)
	}

	if commands == nil {
		commands = []CommandInfo{}
	}
	return commands, nil
}

// FetchCommandMD returns the raw markdown content of a command at filePath
// inside the registry. filePath must already point at the .md file.
func FetchCommandMD(ref RepoRef, token, filePath string) ([]byte, error) {
	if !strings.HasSuffix(filePath, ".md") {
		return nil, fmt.Errorf("command path must end with .md")
	}
	rawBody, status, err := ref.RawFile(filePath, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, filePath)
	}
	return rawBody, nil
}
