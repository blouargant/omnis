package tools

import (
	"fmt"

	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/askuser"
)

// askUserIn is the JSON schema for the ask_user tool.
type askUserIn struct {
	// Kind is the question type: "single", "multi", "text", or "confirm".
	Kind string `json:"kind"`
	// Prompt is the question text shown to the user (markdown supported).
	Prompt string `json:"prompt"`
	// Choices is the list of options for "single", "multi", and "confirm".
	// Required for those kinds; ignored for "text".
	// For "single"/"confirm", must contain 2–4 options.
	Choices []string `json:"choices,omitempty"`
	// AllowText, when true, also accepts a free-text answer alongside the choices
	// (only meaningful for "single" and "multi").
	AllowText bool `json:"allow_text,omitempty"`
	// Default is the pre-selected value suggested to the user (optional).
	Default string `json:"default,omitempty"`
	// TimeoutSeconds overrides the default per-question timeout (0 = registry default).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Subtle marks an optional question the user may ignore (e.g. a wrap-up
	// check): surfaces show it discreetly instead of as a blocking card.
	Subtle bool `json:"subtle,omitempty"`
}

// askUserOut is the JSON response returned to the LLM.
type askUserOut struct {
	// Selected contains the chosen option(s) for "single", "multi", and "confirm".
	Selected []string `json:"selected,omitempty"`
	// Text contains the user's free-text answer for "text" and "allow_text" cases.
	Text string `json:"text,omitempty"`
	// Cancelled is true when the user dismissed the prompt or it timed out.
	Cancelled bool `json:"cancelled,omitempty"`
}

// NewAskUserTool returns the ask_user tool backed by the provided registry.
// The tool blocks until the user answers (via the web UI, TUI, or console).
func NewAskUserTool(reg *askuser.Registry) tool.Tool {
	return mustTool("AskUserQuestion",
		"Ask the user a question and wait for the answer. `kind`: \"single\" (pick one of 2-4 `choices`), "+
			"\"multi\" (pick several), \"text\" (free text) or \"confirm\" (yes/no, safest choice first). "+
			"`prompt` is markdown. `allow_text` also accepts free text with choices; `default` pre-selects a value; "+
			"`subtle` shows an optional question (e.g. a wrap-up check) discreetly — pair it with `timeout_seconds`.",
		askUserHandler(reg),
	)
}

// askUserHandler is AskUserQuestion's body. The question is Durable: it
// survives a server restart and resumes the task in a new turn — except a
// Subtle one, which is optional and has nothing worth resuming. It waits on
// the tool context (the run context), so Stop, session end and shutdown
// release it; a client disconnect does not cancel the run context, so the
// question still waits for the user to come back.
func askUserHandler(reg *askuser.Registry) func(adk.ToolContext, askUserIn) (askUserOut, error) {
	return func(tc adk.ToolContext, in askUserIn) (askUserOut, error) {
		if err := validateAskUserIn(in); err != nil {
			return askUserOut{}, err
		}
		// Nobody can answer in an unattended run: say so at once instead of
		// waiting on a card no one will resolve (the wait has no timeout by
		// design). The first Harbor smoke had the coder ask which branch to
		// use; the answer it needed was "decide yourself".
		if askuser.NonInteractive() {
			return askUserOut{Cancelled: true, Text: NonInteractiveAnswer}, nil
		}
		q := askuser.Question{
			Kind:        askuser.Kind(in.Kind),
			Prompt:      in.Prompt,
			Choices:     in.Choices,
			AllowText:   in.AllowText,
			Default:     in.Default,
			TimeoutSecs: in.TimeoutSeconds,
			Durable:     !in.Subtle,
			Subtle:      in.Subtle,
			Agent:       tc.AgentName(),
		}
		ans, err := reg.Ask(tc, tc.SessionID(), q)
		if err != nil {
			return askUserOut{}, fmt.Errorf("ask_user: %w", err)
		}
		return askUserOut{Selected: ans.Selected, Text: ans.Text, Cancelled: ans.Cancelled}, nil
	}
}

// NonInteractiveAnswer is AskUserQuestion's reply when no user can answer.
const NonInteractiveAnswer = "No user is available in this run: nobody will answer. Do not ask again. " +
	"Choose the option a careful engineer would pick, state that assumption in your final answer, and continue."

func validateAskUserIn(in askUserIn) error {
	switch askuser.Kind(in.Kind) {
	case askuser.KindSingle, askuser.KindConfirm:
		if len(in.Choices) < 2 || len(in.Choices) > 4 {
			return fmt.Errorf("ask_user: kind %q requires 2-4 choices, got %d", in.Kind, len(in.Choices))
		}
	case askuser.KindMulti:
		if len(in.Choices) == 0 {
			return fmt.Errorf("ask_user: kind \"multi\" requires at least 1 choice")
		}
	case askuser.KindText:
		// no choices needed
	case "":
		return fmt.Errorf("ask_user: kind is required")
	default:
		return fmt.Errorf("ask_user: unknown kind %q (use: single, multi, text, confirm)", in.Kind)
	}
	if in.Prompt == "" {
		return fmt.Errorf("ask_user: prompt is required")
	}
	return nil
}
