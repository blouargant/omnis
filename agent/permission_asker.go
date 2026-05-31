package agent

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"google.golang.org/adk/tool"

	"github.com/blouargant/yoke/core/permissions"
	"github.com/blouargant/yoke/internal/askuser"
)

const askUserCmdSnippetMax = 1200

// Choice labels the askuser widget renders. The order matters — the
// safest option (Deny) is first to discourage accidental approvals, and
// options widen in blast radius from top to bottom. The "Allow all
// <Tool> this session" label is built per-call (see toolSessionChoice)
// because it embeds the tool name.
const (
	choiceDeny    = "Deny"
	choiceOnce    = "Allow once (this call)"
	choiceProject = "Allow in this project"
	choiceAlways  = "Allow always"
)

// toolSessionChoice is the per-call label for the tool-scoped session
// grant, e.g. "Allow all Write this session".
func toolSessionChoice(toolName string) string {
	return "Allow all " + toolName + " this session"
}

// NewAskUserPermissionAsker returns a permissions.Asker that routes
// confirmations through the given askuser.Registry. The user picks one
// of four scopes: deny, allow-once (session cache), allow-project
// (persisted with CWD), allow-always (persisted globally).
//
// Falls back to denying the call when the registry is nil or no
// session id is available on the tool context.
func NewAskUserPermissionAsker(reg *askuser.Registry) permissions.Asker {
	return &askUserPermissionAsker{reg: reg}
}

type askUserPermissionAsker struct {
	reg *askuser.Registry
}

func (a *askUserPermissionAsker) Ask(tc tool.Context, toolName, input, reason string) permissions.AskOutcome {
	if a.reg == nil {
		return permissions.OutcomeDeny
	}
	sid := tc.SessionID()
	if sid == "" {
		return permissions.OutcomeDeny
	}

	choiceToolSession := toolSessionChoice(toolName)
	q := askuser.Question{
		Kind:    askuser.KindSingle,
		Prompt:  buildPermissionPrompt(toolName, input, reason),
		Choices: []string{choiceDeny, choiceOnce, choiceToolSession, choiceProject, choiceAlways},
		Default: choiceOnce,
	}
	// Tag registry-install prompts with a shared group + structured item
	// metadata so a surface can coalesce a burst of installs into a single
	// "what will be installed" widget instead of a stack of identical cards.
	if item := installItemMeta(toolName, input); item != nil {
		q.Group = "install"
		q.Item = item
	}
	ans, err := a.reg.Ask(context.Background(), sid, q)
	if err != nil || ans.Cancelled {
		return permissions.OutcomeDeny
	}
	if len(ans.Selected) == 0 {
		return permissions.OutcomeDeny
	}
	switch ans.Selected[0] {
	case choiceOnce:
		return permissions.OutcomeAllowOnce
	case choiceToolSession:
		return permissions.OutcomeAllowToolSession
	case choiceProject:
		return permissions.OutcomeAllowProject
	case choiceAlways:
		return permissions.OutcomeAllowAlways
	}
	return permissions.OutcomeDeny
}

// buildPermissionPrompt formats a concise, markdown-rendered prompt for
// the popup. Bash calls are unwrapped to just the shell command line;
// other tools display the most relevant arg field.
func buildPermissionPrompt(toolName, input, reason string) string {
	title, lang, payload := summariseToolCall(toolName, input)

	var sb strings.Builder
	sb.WriteString("**")
	sb.WriteString(title)
	sb.WriteString("**")
	if payload != "" {
		if len(payload) > askUserCmdSnippetMax {
			payload = payload[:askUserCmdSnippetMax] + "…"
		}
		sb.WriteString("\n\n```")
		sb.WriteString(lang)
		sb.WriteString("\n")
		sb.WriteString(payload)
		sb.WriteString("\n```")
	}
	if reason != "" {
		sb.WriteString("\n\n_")
		sb.WriteString(reason)
		sb.WriteString("_")
	}
	return sb.String()
}

func summariseToolCall(toolName, input string) (string, string, string) {
	args := map[string]any{}
	if input != "" {
		_ = json.Unmarshal([]byte(input), &args)
	}
	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}

	switch toolName {
	case "Bash":
		return "Run shell command?", "sh", str("command")
	case "Read":
		return "Read file?", "", str("file_path")
	case "Write":
		return "Write file " + str("file_path") + "?", "", strings.TrimRight(str("content"), "\n")
	case "Edit":
		return "Edit file " + str("file_path") + "?", "diff", "- " + str("old_string") + "\n+ " + str("new_string")
	case "revert":
		return "Revert file " + str("file_path") + "?", "", ""
	case "Grep":
		return "Search with grep?", "", str("pattern")
	case "Glob":
		return "List files matching glob?", "", str("pattern")
	}
	return "Allow `" + toolName + "` call?", "json", input
}

// installItemMeta derives a friendly "what is being installed" summary for the
// registry install tools so a grouped permission widget can list items by kind.
// Returns nil for any other tool. The kind for install_remote_item is inferred
// from the dir_path manifest filename (display-only — the authoritative kind is
// the registry's, resolved inside the tool).
func installItemMeta(toolName, input string) *askuser.QuestionItem {
	var kind string
	switch toolName {
	case "install_remote_skill":
		kind = "skill"
	case "install_remote_item":
		// inferred below from dir_path
	default:
		return nil
	}
	args := map[string]any{}
	if input != "" {
		_ = json.Unmarshal([]byte(input), &args)
	}
	dirPath, _ := args["dir_path"].(string)
	source, _ := args["registry_id"].(string)
	if kind == "" {
		kind = inferInstallKind(dirPath)
	}
	return &askuser.QuestionItem{Kind: kind, Name: installItemName(dirPath), Source: source}
}

// inferInstallKind guesses the registry item kind from the trailing manifest
// filename of a dir_path (e.g. ".../mcp.md" → "mcp"). A path with no manifest
// suffix is treated as a skill/agent directory and reported as "skill".
func inferInstallKind(dirPath string) string {
	switch strings.ToLower(path.Base(dirPath)) {
	case "mcp.md", "mcp.json":
		return "mcp"
	case "agent.md", "agent.json":
		return "agent"
	case "a2a.json":
		return "a2a"
	case "squad.json":
		return "squad"
	}
	if strings.HasSuffix(strings.ToLower(path.Base(dirPath)), ".md") {
		return "command"
	}
	return "skill"
}

// installItemName extracts a human-friendly item name from a dir_path. For a
// manifest-file path (".../<name>/mcp.md") the name is the parent directory;
// for a bare command/skill file the extension is dropped; otherwise the last
// path segment is used.
func installItemName(dirPath string) string {
	if dirPath == "" {
		return ""
	}
	base := path.Base(dirPath)
	switch strings.ToLower(base) {
	case "mcp.md", "mcp.json", "agent.md", "agent.json", "a2a.json", "squad.json":
		return path.Base(path.Dir(dirPath))
	}
	if ext := path.Ext(base); ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}
