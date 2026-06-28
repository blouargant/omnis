// goal_eval.go — the /goal completion evaluator. After a turn driven by an
// active goal finishes, the surface calls Manager.EvaluateGoal with the goal
// condition and a transcript of the agent's recent work. A single non-streamed
// completion on the "small fast" evaluator model (eval_model_ref, falling back
// to the session's leader model) returns a GOAL_MET / GOAL_NOT_MET verdict plus
// a one-line reason — the same one-off-LLM pattern as the routing capability
// probe (routing.go) and session-title generation (session_title.go): no runner,
// tools, or event bus, so nothing reaches the SSE stream.
//
// The evaluator judges ONLY what the transcript demonstrates; it cannot run
// commands or read files. Write conditions whose satisfaction the agent's own
// output makes visible (a test result, a clean git status, an empty queue).
package agent

import (
	"context"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/llm"
)

// goalTranscriptCap bounds how much recent transcript is fed to the evaluator —
// the relevant evidence is in the latest work, and the judge model is meant to
// be cheap.
const goalTranscriptCap = 16000

const goalEvalSystemPrompt = "You are a strict completion-condition evaluator for an autonomous coding agent. " +
	"You are given a COMPLETION CONDITION and a TRANSCRIPT of the agent's most recent work. " +
	"Decide whether the condition is now FULLY satisfied, based ONLY on evidence visible in the transcript. " +
	"You cannot run commands, read files, or assume anything not shown — if the transcript does not demonstrate " +
	"the condition is met, it is NOT met. " +
	"Reply with exactly one verdict token first: GOAL_MET or GOAL_NOT_MET, " +
	"followed by a colon and ONE short sentence of justification. No other text."

// EvaluateGoal judges whether a session's goal condition is satisfied by the
// given transcript. It returns (met, reason, ok): ok is false when the evaluation
// itself could not be performed (model build / LLM error), in which case the
// caller should stop auto-continuing rather than burn turns blindly. The reason
// is a short human-readable justification surfaced in the status view and, on a
// "not met" verdict, used to guide the next turn.
func (m *Manager) EvaluateGoal(ctx context.Context, sessionID, condition, transcript string) (met bool, reason string, ok bool) {
	if m == nil {
		return false, "", false
	}
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return false, "", false
	}
	inst := m.Lookup(sessionID)
	if inst == nil {
		inst = m.Current()
	}
	if inst == nil {
		return false, "", false
	}
	mdl, err := m.evalModel(ctx, inst)
	if err != nil || mdl == nil {
		return false, "", false
	}

	transcript = strings.TrimSpace(transcript)
	if r := []rune(transcript); len(r) > goalTranscriptCap {
		// Keep the TAIL — the most recent work is the relevant evidence.
		transcript = "…(earlier work omitted)…\n" + string(r[len(r)-goalTranscriptCap:])
	}
	if transcript == "" {
		transcript = "(no transcript text was produced this turn)"
	}

	var user strings.Builder
	user.WriteString("COMPLETION CONDITION:\n")
	user.WriteString(condition)
	user.WriteString("\n\nTRANSCRIPT (the agent's most recent work):\n")
	user.WriteString(transcript)

	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: goalEvalSystemPrompt}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: user.String()}}},
		},
	}

	var out strings.Builder
	for resp, gerr := range mdl.GenerateContent(ctx, req, false) {
		if gerr != nil {
			return false, "", false
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			out.WriteString(p.Text)
		}
	}

	met, reason = parseGoalVerdict(out.String())
	return met, reason, true
}

// evalModel resolves the LLM for the goal evaluator: the catalogue model named by
// eval_model_ref when set and resolvable, otherwise the session's leader model
// (so a goal always works out-of-the-box). A build error on the named eval model
// also falls back to the leader.
func (m *Manager) evalModel(ctx context.Context, inst *Instance) (model.LLM, error) {
	if ref := strings.ToLower(strings.TrimSpace(inst.Settings.EvalModelRef)); ref != "" {
		if mc, ok := inst.Settings.Models[ref]; ok {
			if mdl, err := llm.NewWithSelection(ctx, selectionFromModelConfig(mc)); err == nil {
				return mdl, nil
			}
			// fall through to the leader model on a build error
		}
	}
	return newModelForAgent(ctx, inst.LeaderCfg)
}

// parseGoalVerdict extracts the GOAL_MET / GOAL_NOT_MET verdict and the trailing
// one-line reason from the evaluator's reply. GOAL_NOT_MET is checked first since
// a lazy contains-check would otherwise need ordering care. Reuses the routing
// verdict-reason helpers (same package).
func parseGoalVerdict(text string) (bool, string) {
	t := strings.TrimSpace(text)
	up := strings.ToUpper(t)
	if i := strings.Index(up, "GOAL_NOT_MET"); i >= 0 {
		return false, verdictReason(t, i+len("GOAL_NOT_MET"))
	}
	if i := strings.Index(up, "GOAL_MET"); i >= 0 {
		return true, verdictReason(t, i+len("GOAL_MET"))
	}
	// No clear verdict → treat as not met, surfacing whatever the model said.
	reason := firstNonEmptyLine(t)
	if reason == "" {
		reason = "evaluator returned no clear verdict"
	}
	return false, reason
}
