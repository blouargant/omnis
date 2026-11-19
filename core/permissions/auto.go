package permissions

import (
	"fmt"

	"github.com/blouargant/omnis/core/adk"
)

// AutoVerdict is the auto-mode classifier's decision for one call.
type AutoVerdict struct {
	Allow  bool
	Reason string
}

// AutoJudge decides whether a call that would otherwise prompt may run in auto
// mode. why is the gate's or hook's reason for wanting confirmation. An error
// means "could not judge".
type AutoJudge func(tc adk.ToolContext, toolName string, args map[string]any, cwd, why string) (AutoVerdict, error)

// AutoBlockMessage is the tool output returned when auto mode blocks a call.
func AutoBlockMessage(tool, reason string) string {
	return fmt.Sprintf("[BLOCKED BY AUTO MODE] %s: %s. Try another approach that stays within the user's request, or ask the user.", tool, reason)
}
