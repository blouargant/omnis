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
type SendIn struct {
	Prompt string `json:"prompt"`
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
				resp, err := SendTask(context.Background(), agent, in.Prompt)
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
	return fmt.Sprintf(
		"Delegate a task to the remote A2A agent %q at %s. %s "+
			"Arguments: `prompt` (string, required) — the full task description to send. "+
			"Returns the remote agent's text response.",
		a.Name, a.URL, purpose,
	)
}

var toolNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// SanitizeToolName collapses any character outside [A-Za-z0-9_-] into `_` so
// the resulting tool name passes provider-side function-name validation.
func SanitizeToolName(s string) string {
	return toolNameSanitizer.ReplaceAllString(strings.TrimSpace(s), "_")
}
