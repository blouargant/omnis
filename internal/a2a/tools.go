package a2a

// Tool wiring: turns a list of A2A Agent entries into ADK tools the model can
// invoke. Each tool is named `a2a_<sanitized-name>` and accepts a single
// `prompt` string argument; the response is the remote agent's full text
// reply.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// SendIn is the JSON input schema the model fills in when calling an A2A tool.
// `squad` is optional: when omitted, the tool falls back to the per-peer
// default declared in a2a_config.json, and finally to the receiving
// server's own default squad.
// `session_name` is optional: when set, the call targets an existing named
// session on the remote (its friendly name as listed in the remote's web
// UI sidebar, e.g. "teaching-kite"). When omitted, the call is stateless.
// `create`, when true and `session_name` is set, materialises the session
// on the remote if it does not yet exist. Idempotent: an already-existing
// session is reused.
type SendIn struct {
	Prompt      string `json:"prompt"`
	Squad       string `json:"squad,omitempty"`
	SessionName string `json:"session_name,omitempty"`
	Create      bool   `json:"create,omitempty"`
}

// SendOut is the JSON output returned to the model.
type SendOut struct {
	Response string `json:"response"`
}

// ToolPrefix is the prefix applied to every A2A tool name so they can be
// distinguished from local tools and MCP server tools in traces.
const ToolPrefix = "a2a_"

// NewTools builds one ADK tool per A2A agent in the input slice. Agents whose
// URL is empty are skipped (no point exposing a tool with nothing to call).
func NewTools(agents []Agent) []tool.Tool {
	if len(agents) == 0 {
		return nil
	}
	out := make([]tool.Tool, 0, len(agents))
	for _, a := range agents {
		if strings.TrimSpace(a.URL) == "" {
			continue
		}
		agent := a
		name := ToolPrefix + SanitizeToolName(agent.Name)
		desc := buildToolDescription(agent)
		t, err := functiontool.New(
			functiontool.Config{Name: name, Description: desc},
			func(_ tool.Context, in SendIn) (SendOut, error) {
				squad := strings.TrimSpace(in.Squad)
				if squad == "" {
					squad = strings.TrimSpace(agent.Squad)
				}
				session := strings.TrimSpace(in.SessionName)
				if session == "" {
					session = strings.TrimSpace(agent.SessionName)
				}
				create := in.Create || agent.CreateSession
				resp, err := SendTask(context.Background(), agent, in.Prompt, squad, session, create)
				if err != nil {
					return SendOut{}, err
				}
				return SendOut{Response: resp}, nil
			},
		)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

func buildToolDescription(a Agent) string {
	purpose := strings.TrimSpace(a.Description)
	if purpose == "" {
		purpose = "Remote A2A agent."
	}
	squadNote := "the remote server's default squad"
	if s := strings.TrimSpace(a.Squad); s != "" {
		squadNote = fmt.Sprintf("the %q squad on the remote server", s)
	}
	sessionNote := "no session — each call is stateless and creates a fresh throwaway session on the remote"
	if s := strings.TrimSpace(a.SessionName); s != "" {
		sessionNote = fmt.Sprintf("the remote named session %q (its friendly name in the remote's web UI sidebar)", s)
	}
	createNote := "false (the named session must already exist on the remote — call will -32602 if missing)"
	if a.CreateSession {
		createNote = "true (a missing session will be materialised on the remote)"
	}
	return fmt.Sprintf(
		"Delegate a task to the remote A2A agent %q at %s. %s "+
			"Arguments: `prompt` (string, required) — the full task description to send; "+
			"`squad` (string, optional) — override the remote squad to address (default: %s); "+
			"`session_name` (string, optional) — target an existing named session on the remote by its friendly name (e.g. \"teaching-kite\"), so the conversation history is preserved across calls (default: %s); "+
			"`create` (bool, optional, default %s) — when true together with `session_name`, materialise the session on the remote if it does not yet exist. Session names must be lowercase kebab-case (a-z, 0-9, dashes, up to 80 chars). "+
			"Returns the remote agent's text response. When session_name is set, the turn is persisted into the remote's web UI conversation file and any open web UI tab on that session receives a live mailbox_push event.",
		a.Name, a.URL, purpose, squadNote, sessionNote, createNote,
	)
}

var toolNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// SanitizeToolName collapses any character outside [A-Za-z0-9_-] into `_` so
// the resulting tool name passes provider-side function-name validation.
func SanitizeToolName(s string) string {
	return toolNameSanitizer.ReplaceAllString(strings.TrimSpace(s), "_")
}
