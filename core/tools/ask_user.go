package tools

import (
	"context"
	"fmt"

	"google.golang.org/adk/tool"

	"github.com/blouargant/yoke/internal/askuser"
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
		"Present a question to the user and wait for their answer. "+
			"Use this tool whenever you need the user to make a choice or provide information before proceeding. "+
			"Arguments: "+
			"`kind` (string, required) — question type: \"single\" (choose one of 2-4 options), "+
			"\"multi\" (choose one or more), \"text\" (free-text answer), or \"confirm\" (yes/no — safest first). "+
			"`prompt` (string, required) — the question shown to the user (markdown). "+
			"`choices` ([]string) — options for single/multi/confirm (required for those kinds; 2-4 items for single/confirm). "+
			"`allow_text` (bool) — also accept free text alongside choices (single/multi only). "+
			"`default` (string) — pre-suggested value. "+
			"`timeout_seconds` (int) — override default timeout (0 = use default).",
		func(tc tool.Context, in askUserIn) (askUserOut, error) {
			if err := validateAskUserIn(in); err != nil {
				return askUserOut{}, err
			}

			q := askuser.Question{
				Kind:        askuser.Kind(in.Kind),
				Prompt:      in.Prompt,
				Choices:     in.Choices,
				AllowText:   in.AllowText,
				Default:     in.Default,
				TimeoutSecs: in.TimeoutSeconds,
			}

			sessionID := tc.SessionID()
			ans, err := reg.Ask(context.Background(), sessionID, q)
			if err != nil {
				return askUserOut{}, fmt.Errorf("ask_user: %w", err)
			}
			return askUserOut{
				Selected:  ans.Selected,
				Text:      ans.Text,
				Cancelled: ans.Cancelled,
			}, nil
		},
	)
}

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
