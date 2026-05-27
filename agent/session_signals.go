// session_signals.go — gather per-session signals for the heuristic
// reflector.
//
// Reads from disk:
//   - the compressed StateLog (agent_statelog_<key>.json) — captures the
//     leader's "open issues / decisions / tools" digest.
//   - the conversation file (conversation_<sessionID>.json) — supplies
//     the last few user messages for the keyword scan.
//
// In-memory inputs (tool errors, loaded skills) are passed in by the
// caller: each session-scope hook (load_recorder, future curator_hook)
// keeps its own bucket and hands it to gatherSessionSignals when the
// session ends.

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/yoke/internal/compress"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/softskills"
)

// lastUserMessageCount is the number of trailing user turns the heuristic
// considers. The scan only inspects the last entry directly; surfacing
// three lets the LLM reflector (Phase 3) see context too.
const lastUserMessageCount = 3

// conversationTurnShim mirrors internal/sessions.ConversationTurn so we
// can read the conversation file without importing that package (it
// already imports agent). Only the fields we need.
type conversationTurnShim struct {
	UserText      string `json:"user_text"`
	AssistantText string `json:"assistant_text"`
}

// conversationFileShim mirrors internal/sessions.ConversationFile.
type conversationFileShim struct {
	Turns []conversationTurnShim `json:"turns"`
}

// gatherSessionSignals assembles a softskills.HeuristicInputs from disk
// state and the caller-supplied in-memory buckets.
//
//   - key is the per-session log-file suffix (userID + buildTs combo
//     resolved by Infrastructure.SessionSuffix).
//   - sessionID is the runtime session ID, used to locate the
//     conversation file.
//   - toolErrors and loadedSkills are the bus-accumulated buckets for
//     this session.
//   - explicitFeedback is the wrap-session answer (Phase 5); pass "" to
//     fall back on the implicit user-message scan.
func gatherSessionSignals(
	key, sessionID, explicitFeedback string,
	toolErrors []softskills.ToolError,
	loadedSkills []softskills.LoadedSkill,
) softskills.HeuristicInputs {
	in := softskills.HeuristicInputs{
		ToolErrors:       toolErrors,
		LoadedSkills:     loadedSkills,
		ExplicitFeedback: explicitFeedback,
	}

	// StateLog — agent_statelog_<key>.json. Missing or malformed → leave
	// nil; ReflectHeuristic copes.
	statePath := filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_statelog_%s.json", key))
	if data, err := os.ReadFile(statePath); err == nil && len(data) > 0 {
		var sl compress.StateLog
		if err := json.Unmarshal(data, &sl); err == nil {
			in.StateLog = &sl
		}
	}

	// Conversation file — last N user turns. Skip when the session has
	// no on-disk history (CLI / a2a-ephemeral / tests).
	if sessionID != "" {
		convPath := filepath.Join(paths.LogsDir(), fmt.Sprintf("conversation_%s.json", sessionID))
		if data, err := os.ReadFile(convPath); err == nil && len(data) > 0 {
			in.LastUserMessages = lastUserMessagesFromFile(data)
		}
	}

	return in
}

// lastUserMessagesFromFile parses either the modern envelope or the
// legacy plain-array conversation file format and returns the last
// `lastUserMessageCount` user messages, oldest first.
func lastUserMessagesFromFile(data []byte) []string {
	trimmed := strings.TrimLeftFunc(string(data), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var turns []conversationTurnShim
	if strings.HasPrefix(trimmed, "[") {
		// Legacy plain-array format.
		_ = json.Unmarshal(data, &turns)
	} else {
		var f conversationFileShim
		if err := json.Unmarshal(data, &f); err == nil {
			turns = f.Turns
		}
	}
	out := make([]string, 0, lastUserMessageCount)
	start := len(turns) - lastUserMessageCount
	if start < 0 {
		start = 0
	}
	for _, t := range turns[start:] {
		if msg := strings.TrimSpace(t.UserText); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}
