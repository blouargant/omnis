// reflector_heuristic.go — deterministic, no-LLM reflector.
//
// Synthesises a session-success verdict and per-skill tags from existing
// artefacts (compressed state log, recent user messages, tool errors).
// Cheap and good-enough for the common case; the LLM Reflector (Phase 3)
// overlays it on top and takes precedence on overlap.
//
// Signals & rules (kept in step with ace-plan.md Phase 2):
//
//   - Positive markers: StateLog.OpenIssues empty AND at least one
//     Decision recorded; last user message matches a "things worked"
//     keyword with no negation in the same message; zero tool errors in
//     the last 5 tool calls.
//   - Negative markers: StateLog.OpenIssues non-empty; last user message
//     matches a "things broke" keyword; ≥1 tool error in the last 5 tool
//     calls; SubAgentRetried==true (filled by Phase 6).
//   - Per-skill tag: defaults to "neutral". "helpful" when Success ==
//     Positive AND the skill was loaded. "harmful" when Success ==
//     Negative AND a tool error occurred AFTER the skill's load timestamp.
//   - FeedbackPath (Phase 5): when explicit feedback is present, it
//     dominates the user-message scan; the rest of the signals still
//     contribute to the per-skill tagging.

package softskills

import (
	"regexp"
	"strings"
	"time"

	"github.com/blouargant/yoke/internal/compress"
)

// Tristate carries the session-level success verdict.
type Tristate int

const (
	// Unknown means we did not gather enough signal to decide.
	Unknown Tristate = iota
	// Positive: user got what they came for.
	Positive
	// Negative: something failed.
	Negative
	// Ambiguous: signals contradict each other.
	Ambiguous
)

func (t Tristate) String() string {
	switch t {
	case Positive:
		return "positive"
	case Negative:
		return "negative"
	case Ambiguous:
		return "ambiguous"
	default:
		return "unknown"
	}
}

// ToolError captures a single tool failure with its timestamp so the
// reflector can correlate it with skill loads.
type ToolError struct {
	Tool   string
	Agent  string
	Error  string
	When   time.Time
	CallID string
}

// LoadedSkill is a single load_softskill event recorded for this scope
// (session or sub-agent invocation).
type LoadedSkill struct {
	// Key is the stable stats identifier: "<agent>/<name>" for sub-agent
	// loads, bare "<name>" for the leader.
	Key string
	// When is the moment the load happened.
	When time.Time
}

// HeuristicInputs is the snapshot the reflector consumes. Callers gather
// these once per scope and pass them in; the reflector is pure.
type HeuristicInputs struct {
	// StateLog is the compressed digest of the session. Optional.
	StateLog *compress.StateLog
	// LastUserMessages: the tail of the user-side turns (oldest → newest).
	// One to three messages is typical.
	LastUserMessages []string
	// ToolErrors observed during the scope, in chronological order.
	ToolErrors []ToolError
	// LoadedSkills are the load_softskill calls observed during the
	// scope, in chronological order.
	LoadedSkills []LoadedSkill
	// SubAgentRetried is set by Phase 6 when the leader re-tasked the
	// same sub-agent within one user turn. Treated as a strong negative
	// marker.
	SubAgentRetried bool
	// ExplicitFeedback is Phase 5's wrap-up answer (free text). When
	// non-empty it dominates the user-message scan.
	ExplicitFeedback string
}

// Outcome is the reflector's verdict. Tags is keyed by skill key (same
// shape as Stats.Entries) and holds one of "helpful" / "harmful" /
// "neutral" — the curator and stats.RecordTag handle the rest.
type Outcome struct {
	Success    Tristate
	Confidence float64
	// Signals is a list of human-readable markers that contributed to the
	// verdict. Useful for debugging and for feeding into Phase 3's
	// reflector prompt as additional context.
	Signals []string
	// Tags maps skill key → "helpful" | "harmful" | "neutral".
	Tags map[string]string
	// KeyInsight is a short generalisable lesson extracted by the LLM
	// reflector — empty when nothing emerged or when only the heuristic
	// ran. The curator consults this to decide whether to *create* a
	// new soft-skill.
	KeyInsight string
	// TagReasons maps skill key → free-text justification supplied by
	// the LLM reflector. Surfaced to the curator so it can decide
	// whether a `harmful` tag warrants deletion (e.g. reason mentions
	// "wrong assumptions" / "superseded"). Empty in heuristic-only
	// outcomes.
	TagReasons map[string]string
}

// positiveKeyword matches expressions of approval. The (?i) flag makes
// the whole expression case-insensitive; \b enforces word boundaries so
// "perfectly" does not match (we want clear acknowledgements, not
// adverbs scattered through technical prose).
var positiveKeyword = regexp.MustCompile(`(?i)\b(thanks|thank you|works|perfect|great|good|exactly|nice|awesome|excellent|brilliant)\b`)

// negativeKeyword matches expressions of dissatisfaction or breakage.
var negativeKeyword = regexp.MustCompile(`(?i)\b(no|wrong|broken|doesn'?t|isn'?t|fail(?:ed|s|ing)?|error(?:s|ed)?|bad|nope|nothing|stuck|crash(?:ed|es|ing)?)\b`)

// negationKeyword matches local negations that should suppress a
// positive hit ("not great", "doesn't work"). Cheap heuristic; the LLM
// reflector picks up the nuance the keyword scan misses.
var negationKeyword = regexp.MustCompile(`(?i)\b(not|never|no|don'?t|doesn'?t|isn'?t|wasn'?t|aren'?t)\b`)

// ReflectHeuristic returns an Outcome derived purely from in. The
// function is total — every input shape yields a valid Outcome (possibly
// Unknown with empty tags).
func ReflectHeuristic(in HeuristicInputs) Outcome {
	out := Outcome{
		Success: Unknown,
		Tags:    map[string]string{},
	}

	// Score positive vs. negative signals; the strongest wins. We collect
	// signals as strings for debuggability rather than computing a
	// weighted average — the rule set is small enough.
	pos, neg := 0, 0

	// ── Explicit feedback (Phase 5) — dominant signal ────────────────
	// When the wrap-up answer classifies as Positive or Negative, it
	// drives the verdict directly: the user has told us the answer, the
	// rest is corroboration. Implicit signals still contribute to
	// per-skill tagging (e.g. "harmful only when an error followed the
	// load") but not to the verdict tally.
	var explicitVerdict Tristate = Unknown
	if fb := strings.TrimSpace(in.ExplicitFeedback); fb != "" {
		explicitVerdict = classifyMessage(fb)
		switch explicitVerdict {
		case Positive:
			out.Signals = append(out.Signals, "explicit_feedback:positive")
		case Negative:
			out.Signals = append(out.Signals, "explicit_feedback:negative")
		}
	}

	if explicitVerdict == Unknown && len(in.LastUserMessages) > 0 {
		// Use only the final message — earlier ones may belong to a
		// failed-then-fixed flow.
		last := in.LastUserMessages[len(in.LastUserMessages)-1]
		switch classifyMessage(last) {
		case Positive:
			pos++
			out.Signals = append(out.Signals, "final_user_message:positive")
		case Negative:
			neg++
			out.Signals = append(out.Signals, "final_user_message:negative")
		}
	}

	// ── StateLog scan ────────────────────────────────────────────────
	if sl := in.StateLog; sl != nil {
		if len(sl.OpenIssues) == 0 && len(sl.Decisions) > 0 {
			pos++
			out.Signals = append(out.Signals, "statelog:clean_with_decisions")
		}
		if len(sl.OpenIssues) > 0 {
			neg++
			out.Signals = append(out.Signals, "statelog:open_issues")
		}
	}

	// ── Tool-error scan (last 5) ─────────────────────────────────────
	recentErrors := in.ToolErrors
	if n := len(recentErrors); n > 5 {
		recentErrors = recentErrors[n-5:]
	}
	if len(recentErrors) == 0 {
		// Only counts as a positive marker when we had tool activity at
		// all — we don't reward sessions that did nothing. Cheap proxy:
		// require at least one tool call recorded in StateLog.Tools.
		if in.StateLog != nil && len(in.StateLog.Tools) > 0 {
			pos++
			out.Signals = append(out.Signals, "tool_errors:none_recent")
		}
	} else {
		neg++
		out.Signals = append(out.Signals, "tool_errors:recent")
	}

	// ── Sub-agent retry (Phase 6 plumbing) ───────────────────────────
	if in.SubAgentRetried {
		neg++
		out.Signals = append(out.Signals, "subagent_retry")
	}

	// ── Verdict ──────────────────────────────────────────────────────
	// Explicit feedback shortcuts the tally; otherwise sum signals.
	switch {
	case explicitVerdict == Positive:
		out.Success = Positive
		out.Confidence = 1.0
	case explicitVerdict == Negative:
		out.Success = Negative
		out.Confidence = 1.0
	case pos > 0 && neg == 0:
		out.Success = Positive
		out.Confidence = 1.0
	case neg > 0 && pos == 0:
		out.Success = Negative
		out.Confidence = 1.0
	case pos > 0 && neg > 0:
		out.Success = Ambiguous
		diff := pos - neg
		if diff < 0 {
			diff = -diff
		}
		out.Confidence = float64(diff) / float64(pos+neg)
	default:
		out.Success = Unknown
	}

	// ── Per-skill tagging ────────────────────────────────────────────
	// We only emit a tag when there is a real verdict to attach. Unknown
	// outcomes (no signals at all) leave Tags empty so the per-session
	// stats save doesn't pollute the neutral counter with vacuous loads.
	for _, ls := range in.LoadedSkills {
		switch out.Success {
		case Positive:
			out.Tags[ls.Key] = "helpful"
		case Negative:
			// Harmful only when a tool error happened AFTER this load.
			// Sub-agent retry is also a strong "this skill didn't help"
			// signal (Phase 6).
			if in.SubAgentRetried || hasErrorAfter(in.ToolErrors, ls.When) {
				out.Tags[ls.Key] = "harmful"
			} else {
				out.Tags[ls.Key] = "neutral"
			}
		case Ambiguous:
			out.Tags[ls.Key] = "neutral"
		}
	}

	return out
}

// classifyMessage returns Positive / Negative / Unknown for a single
// user-message string. Negation in the same message suppresses a
// positive hit (e.g. "not great" stays neutral / leans negative).
func classifyMessage(msg string) Tristate {
	if msg == "" {
		return Unknown
	}
	posHit := positiveKeyword.MatchString(msg)
	negHit := negativeKeyword.MatchString(msg)
	hasNegation := negationKeyword.MatchString(msg)

	switch {
	case negHit:
		return Negative
	case posHit && !hasNegation:
		return Positive
	case posHit && hasNegation:
		return Unknown
	default:
		return Unknown
	}
}

// hasErrorAfter reports whether any tool error in errs occurred strictly
// after t. Zero-valued timestamps are treated as "we don't know" and
// excluded so a malformed sample never flips a tag to harmful.
func hasErrorAfter(errs []ToolError, t time.Time) bool {
	if t.IsZero() {
		return false
	}
	for _, e := range errs {
		if e.When.IsZero() {
			continue
		}
		if e.When.After(t) {
			return true
		}
	}
	return false
}
