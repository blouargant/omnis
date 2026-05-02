package compress

import (
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// compactNowIn is the argument schema for compact_now.
type compactNowIn struct {
	Reason string `json:"reason"`
}

type compactNowOut struct {
	Status string `json:"status"`
}

// tools returns the tool list to mount on the agent so it can request
// compression explicitly. The compact_now tool sets a per-session
// forceCompact flag consumed on the next BeforeModelCallback invocation.
func (m *manager) tools() []tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name: "compact_now",
		Description: "Request that the conversation history be compressed before the next model call. " +
			"Use this after completing a major sub-task (e.g. finishing one feature, closing a long " +
			"investigation) to free context for what comes next. " +
			"Arguments: `reason` (string, required) — a one-sentence justification.",
	}, func(ctx tool.Context, in compactNowIn) (compactNowOut, error) {
		st := m.state(ctx.UserID(), ctx.SessionID())
		st.forceCompact.Store(true)
		return compactNowOut{Status: "compaction scheduled: " + in.Reason}, nil
	})
	return []tool.Tool{t}
}
