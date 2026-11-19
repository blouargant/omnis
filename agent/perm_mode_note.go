package agent

import (
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/permissions"
)

// permModeNote is the one-paragraph note appended to the root's system
// instruction telling the model which permission mode the session runs in.
// ModeDefault adds nothing. The text is stable per mode, so the prompt cache
// only misses when the mode changes.
func permModeNote(m permissions.Mode) string {
	switch m {
	case permissions.ModePlan:
		return "Plan mode: you may read and investigate, not modify files or run changing commands. Present a plan; to carry it out, ask the user with AskUserQuestion to leave plan mode."
	case permissions.ModeAcceptEdits:
		return "Permission mode: acceptEdits. File edits inside the working directory run without confirmation; other changing commands still ask."
	case permissions.ModeAuto:
		return "Permission mode: auto. Calls are judged automatically; a call the judge blocks is refused, so do not work around it."
	case permissions.ModeDontAsk:
		return "Permission mode: dontAsk. Only pre-approved calls run; any other call is refused, so do not retry it."
	case permissions.ModeBypass:
		return "Permission mode: bypassPermissions. Tool calls run without confirmation."
	default:
		return ""
	}
}

// appendPermModeNote appends the session's permission-mode note to the request's
// system instruction. Nil store or default mode ⇒ no change.
func appendPermModeNote(req *model.LLMRequest, modes *permissions.ModeStore, sid string) {
	if req == nil {
		return
	}
	note := permModeNote(modes.Effective(sid))
	if note == "" {
		return
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	part := &genai.Part{Text: "\n\n" + note}
	if req.Config.SystemInstruction == nil {
		req.Config.SystemInstruction = &genai.Content{Parts: []*genai.Part{part}}
		return
	}
	req.Config.SystemInstruction.Parts = append(req.Config.SystemInstruction.Parts, part)
}

// permModePlugin is the BeforeModelCallback that keys the note on the root's
// real session id. Mounted on answering squad roots only.
func permModePlugin(modes *permissions.ModeStore) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: "perm_mode",
		BeforeModelCallback: llmagent.BeforeModelCallback(
			func(ctx adk.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
				appendPermModeNote(req, modes, ctx.SessionID())
				return nil, nil
			},
		),
	})
}
