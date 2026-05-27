// subagent_reflector.go — per-sub-agent-invocation heuristic tagger.
//
// The leader's run is a graph of sub-agent calls. After the run ends we
// know enough about each call to tag the soft-skills it loaded:
//
//   1. Retry: if the leader called the same sub-agent again later in
//      the same run, the first call was probably unhelpful. Mark its
//      loaded skills `harmful`.
//   2. Error / empty result: if the call returned an error string or a
//      suspiciously short / empty output, mark loaded skills `harmful`.
//   3. Leader reaction: lexical scan of the leader's assistant text
//      between this sub-agent's EventSubAgentEnd and the next sub-agent
//      call (or run end) classifies the leader as Approved / Retried /
//      Unknown. Approved → `helpful`. Retried → `harmful`. Unknown →
//      `neutral` (the default).
//
// The fragile bit is (3) — it's a keyword scan. If it produces false
// positives in practice the plan calls for escalating to a cheap LLM
// micro-classifier per run; for now the keyword set is small and the
// rules conservative (Unknown is preferred to a wrong helpful/harmful).

package softskills

import (
	"regexp"
	"strings"
)

// LeaderReaction is the leader's stance toward a single sub-agent
// invocation, inferred from its assistant text after the call.
type LeaderReaction int

const (
	// LeaderUnknown — no clear signal in the assistant text.
	LeaderUnknown LeaderReaction = iota
	// LeaderApproved — the leader cited the result approvingly.
	LeaderApproved
	// LeaderRetried — the leader explicitly re-tasked the sub-agent or
	// said the result was unusable.
	LeaderRetried
)

// SubAgentInvocation is the per-call snapshot the tagger consumes.
type SubAgentInvocation struct {
	// Agent is the sub-agent's name ("investigator", "web_agent", …).
	Agent string
	// LoadedSkills are the skill keys the sub-agent loaded during this
	// invocation (typically "<agent>/<name>").
	LoadedSkills []string
	// ToolErrors observed inside this invocation.
	ToolErrors []ToolError
	// OutputText is the sub-agent's final reply to the leader. The
	// reflector looks at length + an "Error:" prefix to decide whether
	// the call was a tool-style failure.
	OutputText string
	// Retried is true when the leader called the same sub-agent again
	// later in the same run.
	Retried bool
	// LeaderReaction is the lexical reading of the leader's assistant
	// text between this invocation and the next sub-agent call.
	LeaderReaction LeaderReaction
}

// approvalKeyword fires on phrases that explicitly cite a sub-agent's
// result as authoritative for the next step.
//
//   - "investigator reported …"           (or any agent name)
//   - "per investigator, …"
//   - "investigator confirmed …"
//   - "according to the investigator"
//
// The agent name is matched by the caller (we substitute it into the
// pattern); the leftover keywords are intentionally narrow.
var approvalVerb = regexp.MustCompile(`(?i)\b(reported|confirmed|found|identified|shows|says|told us|established|verified)\b`)

// retryKeyword fires when the leader visibly re-tasked or dismissed a
// sub-agent's reply.
var retryKeyword = regexp.MustCompile(`(?i)\b(let me (?:ask|try) again|re-ask|let'?s try again|that didn'?t work|that'?s wrong|failed|empty result|let me retry|need more|retask|try with|please try)\b`)

// ClassifyLeaderReaction reads a leader's assistant text and returns
// the inferred reaction toward an invocation of the given sub-agent
// name. Empty or whitespace-only text returns LeaderUnknown.
//
// Detection rules (in priority order):
//   - retryKeyword anywhere → LeaderRetried.
//   - "<agentName> <approvalVerb>", "per <agentName>", "according to <agentName>", or
//     "<agentName>'s findings" → LeaderApproved.
//   - otherwise → LeaderUnknown.
func ClassifyLeaderReaction(agentName, leaderText string) LeaderReaction {
	text := strings.TrimSpace(leaderText)
	if text == "" || agentName == "" {
		return LeaderUnknown
	}
	if retryKeyword.MatchString(text) {
		return LeaderRetried
	}
	// "per investigator," / "according to investigator" / "investigator's findings"
	citationPattern := regexp.MustCompile(`(?i)\b(per|according to|from)\s+` + regexp.QuoteMeta(agentName) + `\b`)
	if citationPattern.MatchString(text) {
		return LeaderApproved
	}
	possessive := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(agentName) + `'?s\s+(findings|result|report|output|evidence|analysis)\b`)
	if possessive.MatchString(text) {
		return LeaderApproved
	}
	// "<agentName> reported / confirmed / found …" — must occur
	// reasonably close to the verb so we don't hit a stray verb that
	// isn't about the sub-agent.
	idx := indexFoldedWord(text, agentName)
	if idx >= 0 {
		end := idx + len(agentName) + 80
		if end > len(text) {
			end = len(text)
		}
		if approvalVerb.MatchString(text[idx:end]) {
			return LeaderApproved
		}
	}
	return LeaderUnknown
}

// indexFoldedWord returns the byte index of the first occurrence of
// word in s (case-insensitive, with word-boundary matching on both
// sides). Returns -1 when missing.
func indexFoldedWord(s, word string) int {
	if word == "" {
		return -1
	}
	pat := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`)
	loc := pat.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// TagInvocation applies the three signals to produce one tag per loaded
// skill. The caller (subagent_hook) merges these into the stats sidecar.
//
//   - Retry OR "Error:" output OR empty/short output → harmful.
//   - LeaderRetried                                  → harmful.
//   - LeaderApproved AND no errors                   → helpful.
//   - otherwise                                       → neutral.
//
// Returns a map from skill key → tag. Keys absent from
// inv.LoadedSkills are never produced.
func TagInvocation(inv SubAgentInvocation) map[string]string {
	out := map[string]string{}
	if len(inv.LoadedSkills) == 0 {
		return out
	}

	tag := "neutral"
	switch {
	case inv.Retried, looksLikeFailure(inv), inv.LeaderReaction == LeaderRetried:
		tag = "harmful"
	case inv.LeaderReaction == LeaderApproved && len(inv.ToolErrors) == 0:
		tag = "helpful"
	}

	for _, k := range inv.LoadedSkills {
		out[k] = tag
	}
	return out
}

// looksLikeFailure returns true when the sub-agent's reply itself
// indicates a failure: an "Error:" prefix, or a body short enough that
// no useful work could have happened (≤8 non-whitespace chars or empty
// after trimming).
func looksLikeFailure(inv SubAgentInvocation) bool {
	if len(inv.ToolErrors) > 0 {
		return true
	}
	body := strings.TrimSpace(inv.OutputText)
	if body == "" {
		return true
	}
	if strings.HasPrefix(body, "Error:") || strings.HasPrefix(body, "ERROR:") {
		return true
	}
	// Count non-whitespace characters; below the threshold means the
	// reply was effectively empty.
	nonWS := 0
	for _, r := range body {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			nonWS++
		}
		if nonWS > 8 {
			break
		}
	}
	return nonWS <= 8
}
