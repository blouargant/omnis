// curator_hook.go — wires the soft-skills curator (and the LLM reflector
// that primes it) to the event bus.
//
// On EventSessionEnd, the load_recorder runs the heuristic reflector and
// emits EventSessionReflected with the gathered signals. This hook
// subscribes to EventSessionReflected: it runs the LLM Reflector to
// produce a richer Outcome, merges that with the heuristic Outcome,
// applies the tag deltas to softskills/_stats.json (decrement heuristic
// tags that the LLM overrode, increment the new tags), and then spawns
// the Curator agent.
//
// EventCurateNow (manual /learn-now trigger) bypasses the heuristic
// pipeline — we run the curator directly without an Outcome, mirroring
// the pre-Phase-3 behaviour.
//
// Failures are logged and swallowed: reflection / curation must never
// break the user-facing session.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/adk/model"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/softskills"
)

// curateTimeout caps how long a single curator invocation may run.
// 2 minutes is generous for read-2-files + 1 LLM call + 3 tool calls;
// anything longer is almost certainly a runaway loop.
const curateTimeout = 2 * time.Minute

// reflectTimeout caps the LLM reflector. Strict — the reflector is one
// LLM call against a few small text inputs; if it can't finish in 60s,
// the LLM is wedged and we fall back on the heuristic.
const reflectTimeout = 60 * time.Second

// curatorInflight prevents concurrent curator invocations on the same
// session (e.g. if EventSessionEnd ever fires twice for one session).
var (
	curatorMu      sync.Mutex
	curatorRunning = map[string]bool{}
)

// CuratorGateConfig holds the quick pre-flight thresholds evaluated before
// spinning up the curator LLM. All fields default to sensible values when zero.
type CuratorGateConfig struct {
	// MinTurns is the minimum number of model responses a session must have
	// before automatic (non-forced) curation is considered. Default: 3.
	MinTurns int
	// MinSubAgentCalls is the minimum total sub-agent invocations the session
	// must contain before automatic curation is considered — unless the session
	// already has at least one recorded decision. Default: 2.
	MinSubAgentCalls int
}

func (c CuratorGateConfig) minTurns() int {
	if c.MinTurns > 0 {
		return c.MinTurns
	}
	return 3
}

func (c CuratorGateConfig) minSubAgentCalls() int {
	if c.MinSubAgentCalls > 0 {
		return c.MinSubAgentCalls
	}
	return 2
}

// registerCuratorHook subscribes handlers to EventSessionReflected and
// EventCurateNow that fire the LLM reflector + curator in a detached
// goroutine. Both agents + their runners are built on-demand inside the
// goroutine so (a) startup cost is paid only when the hook actually
// fires and (b) a construction error never aborts the main agent boot.
//
// reflectorLLM is optional: when nil, the reflector step is skipped and
// the curator runs with the heuristic Outcome only.
//
// Returns the bus subscriptions so the calling Instance can detach them
// on Close (hot-reload).
func registerCuratorHook(
	bus *events.Bus,
	curatorLLM, reflectorLLM model.LLM,
	softDir string,
	agentNames []string,
	gate CuratorGateConfig,
	sessionSuffix func(u, s string) string,
) []*events.Subscription {
	reflectedHandler := func(_ string, payload map[string]any) {
		userID, _ := payload["user_id"].(string)
		sessionID, _ := payload["session_id"].(string)
		if userID == "" || sessionID == "" {
			return
		}
		key, _ := payload["session_key"].(string)
		if key == "" {
			key = sessionSuffix(userID, sessionID)
		}
		runCuratorPipeline(bus, curatorLLM, reflectorLLM, softDir, agentNames, gate, key, userID, sessionID, payload)
	}

	curateNowHandler := func(_ string, payload map[string]any) {
		userID, _ := payload["user_id"].(string)
		sessionID, _ := payload["session_id"].(string)
		if userID == "" || sessionID == "" {
			return
		}
		key := sessionSuffix(userID, sessionID)
		// Forced trigger: skip the reflector (no heuristic context) and
		// drive the curator directly. Pass an empty payload.
		runCuratorPipeline(bus, curatorLLM, nil, softDir, agentNames, gate, key, userID, sessionID, nil)
	}

	return []*events.Subscription{
		bus.Subscribe(events.EventSessionReflected, reflectedHandler),
		bus.Subscribe(events.EventCurateNow, curateNowHandler),
	}
}

// runCuratorPipeline orchestrates one (heuristic-already-applied) →
// LLM reflector → tag delta → curator pass. payload is the
// EventSessionReflected payload when invoked from that path; nil from
// the EventCurateNow path.
func runCuratorPipeline(
	bus *events.Bus,
	curatorLLM, reflectorLLM model.LLM,
	softDir string,
	agentNames []string,
	gate CuratorGateConfig,
	key, userID, sessionID string,
	payload map[string]any,
) {
	forced := CurateSessionRequested(key)
	auditPath := filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_memory_%s.md", key))
	statePath := filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_statelog_%s.json", key))

	curatorMu.Lock()
	if curatorRunning[key] {
		curatorMu.Unlock()
		return
	}
	curatorRunning[key] = true
	curatorMu.Unlock()

	go func() {
		defer func() {
			curatorMu.Lock()
			delete(curatorRunning, key)
			curatorMu.Unlock()
			if r := recover(); r != nil {
				log.Printf("curator: panic recovered for session %s: %v", key, r)
			}
		}()

		emitEnd := func(summary string, skipped bool, reason string, err error) {
			p := map[string]any{
				"user_id":    userID,
				"session_id": sessionID,
				"summary":    summary,
				"skipped":    skipped,
				"reason":     reason,
				"error":      "",
			}
			if err != nil {
				p["error"] = err.Error()
			}
			bus.Emit(events.EventCuratorEnd, p)
		}

		// Skip if neither input exists — nothing to learn from.
		if !fileExists(auditPath) && !fileExists(statePath) {
			if forced {
				emitEnd("", true, "no session data to learn from", nil)
			}
			return
		}

		// Quick pre-flight: avoid spinning up the curator LLM for
		// sessions that are too shallow. Forced sessions bypass.
		if !curatorGate(statePath, forced, agentNames, gate) {
			if forced {
				emitEnd("", true, "session too shallow for soft-skill curation", nil)
			}
			return
		}

		// LLM reflector pass — only when we have payload data (came via
		// EventSessionReflected) AND a reflector model is configured.
		// On failure we log and continue with the heuristic Outcome alone.
		var llmOutcome softskills.Outcome
		llmRan := false
		if payload != nil && reflectorLLM != nil {
			loaded := stringSliceFromPayload(payload["loaded_skills"])
			if len(loaded) > 0 {
				rctx, rcancel := context.WithTimeout(context.Background(), reflectTimeout)
				out, err := runReflector(rctx, reflectorLLM, auditPath, statePath, loaded, payload)
				rcancel()
				if err != nil {
					log.Printf("reflector: skipped for session %s (%v)", key, err)
				} else {
					llmOutcome = out
					llmRan = true
				}
			}
		}

		// Apply LLM tag deltas to stats: where the LLM disagrees with
		// the heuristic, decrement the heuristic tag and increment the
		// LLM tag. Where the heuristic had no tag, just increment.
		if llmRan {
			if err := applyTagDeltas(softDir, payload, llmOutcome); err != nil {
				log.Printf("reflector: apply tag deltas for session %s: %v", key, err)
			}
		}

		// Build the merged Outcome the curator will consume. The
		// heuristic comes from the event payload (only Success + Tags
		// are conveyed; KeyInsight/TagReasons are LLM-only). When the
		// LLM didn't run, the merged outcome is just the heuristic
		// reconstruction.
		mergedOutcome := reconstructHeuristicOutcome(payload)
		if llmRan {
			merged := softskills.MergeOutcomes(mergedOutcome, llmOutcome)
			mergedOutcome = merged
		}
		// Stats sidecar (post tag-delta reconciliation).
		curStats, _ := softskills.LoadStats(softDir)

		bus.Emit(events.EventCuratorStart, map[string]any{
			"user_id":    userID,
			"session_id": sessionID,
		})

		ctx, cancel := context.WithTimeout(context.Background(), curateTimeout)
		defer cancel()

		r, err := softskills.CuratorRunner(ctx, softskills.CuratorConfig{
			Model:         curatorLLM,
			SoftSkillsDir: softDir,
			AgentNames:    agentNames,
		})
		if err != nil {
			log.Printf("curator: build failed for session %s: %v", key, err)
			emitEnd("", false, "", fmt.Errorf("curator build failed: %w", err))
			return
		}
		curateInputs := softskills.CurateInputs{
			AuditPath:    auditPath,
			StateLogPath: statePath,
			AgentNames:   agentNames,
			Stats:        curStats,
		}
		if payload != nil || llmRan {
			oc := mergedOutcome
			curateInputs.Outcome = &oc
		}
		summary, err := softskills.Curate(ctx, r, curateInputs)
		if err != nil {
			log.Printf("curator: run failed for session %s: %v", key, err)
			emitEnd("", false, "", fmt.Errorf("curation failed: %w", err))
			return
		}
		emitEnd(summary, false, "", nil)
	}()
}

// runReflector builds a reflector runner and runs it once with the data
// gathered from the EventSessionReflected payload.
func runReflector(
	ctx context.Context,
	llm model.LLM,
	auditPath, statePath string,
	loadedSkills []string,
	payload map[string]any,
) (softskills.Outcome, error) {
	r, err := softskills.ReflectorRunner(ctx, softskills.ReflectorConfig{Model: llm})
	if err != nil {
		return softskills.Outcome{}, fmt.Errorf("build reflector: %w", err)
	}
	feedback, _ := payload["explicit_feedback"].(string)
	in := softskills.ReflectInputs{
		AuditPath:        auditPath,
		StateLogPath:     statePath,
		LoadedSkills:     loadedSkills,
		LastUserMessages: stringSliceFromPayload(payload["last_user_messages"]),
		ToolErrors:       toolErrorsFromPayload(payload["tool_errors"]),
		ExplicitFeedback: feedback,
	}
	return softskills.Reflect(ctx, r, in)
}

// applyTagDeltas reconciles the LLM outcome with the heuristic tags
// already recorded in _stats.json. For each loaded skill key:
//   - both untagged: no-op.
//   - heuristic only: keep heuristic tag (no-op).
//   - LLM only:       RecordTag the LLM value.
//   - both same:      no-op (already counted).
//   - both differ:    Retag(key, heur, llm) — moves one count.
func applyTagDeltas(softDir string, payload map[string]any, llm softskills.Outcome) error {
	heurTags := map[string]string{}
	if raw, ok := payload["heuristic_tags"].(map[string]string); ok {
		heurTags = raw
	} else if raw, ok := payload["heuristic_tags"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				heurTags[k] = s
			}
		}
	}
	if len(heurTags) == 0 && len(llm.Tags) == 0 {
		return nil
	}

	s, err := softskills.LoadStats(softDir)
	if err != nil {
		return fmt.Errorf("load stats: %w", err)
	}
	changed := false
	keys := map[string]struct{}{}
	for k := range heurTags {
		keys[k] = struct{}{}
	}
	for k := range llm.Tags {
		keys[k] = struct{}{}
	}
	for k := range keys {
		heur := heurTags[k]
		ml := llm.Tags[k]
		switch {
		case ml == "":
			// LLM didn't tag this key — keep heuristic.
		case heur == "":
			// LLM tag is new — increment.
			s.RecordTag(k, ml)
			changed = true
		case heur != ml:
			s.Retag(k, heur, ml)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.Save(softDir)
}

// reconstructHeuristicOutcome rebuilds an Outcome from an
// EventSessionReflected payload. Used by the curator pipeline as the
// base for MergeOutcomes (the LLM outcome layers on top) — and as the
// only Outcome when the LLM reflector is disabled. The payload only
// carries Success + Tags; KeyInsight / TagReasons are LLM-only fields
// that stay empty here.
func reconstructHeuristicOutcome(payload map[string]any) softskills.Outcome {
	out := softskills.Outcome{
		Tags:       map[string]string{},
		TagReasons: map[string]string{},
	}
	if payload == nil {
		return out
	}
	switch s, _ := payload["heuristic_success"].(string); s {
	case "positive":
		out.Success = softskills.Positive
	case "negative":
		out.Success = softskills.Negative
	case "ambiguous":
		out.Success = softskills.Ambiguous
	default:
		out.Success = softskills.Unknown
	}
	if raw, ok := payload["heuristic_tags"].(map[string]string); ok {
		for k, v := range raw {
			out.Tags[k] = v
		}
	} else if raw, ok := payload["heuristic_tags"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				out.Tags[k] = s
			}
		}
	}
	return out
}

// stringSliceFromPayload coerces a payload entry into []string,
// accepting both []string and []any (json round-trip artefacts).
func stringSliceFromPayload(v any) []string {
	if s, ok := v.([]string); ok {
		return s
	}
	if raw, ok := v.([]any); ok {
		out := make([]string, 0, len(raw))
		for _, e := range raw {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// toolErrorsFromPayload converts the payload's []map[string]any into the
// typed []softskills.ToolError slice the reflector consumes.
func toolErrorsFromPayload(v any) []softskills.ToolError {
	raw, ok := v.([]map[string]any)
	if !ok {
		// json round-trip yields []any of map[string]any.
		if anys, ok2 := v.([]any); ok2 {
			raw = make([]map[string]any, 0, len(anys))
			for _, e := range anys {
				if m, ok3 := e.(map[string]any); ok3 {
					raw = append(raw, m)
				}
			}
		} else {
			return nil
		}
	}
	out := make([]softskills.ToolError, 0, len(raw))
	for _, m := range raw {
		tool, _ := m["tool"].(string)
		agent, _ := m["agent"].(string)
		errStr, _ := m["error"].(string)
		when, _ := m["when"].(time.Time)
		out = append(out, softskills.ToolError{
			Tool:  tool,
			Agent: agent,
			Error: errStr,
			When:  when,
		})
	}
	return out
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// curatorGate is the quick pre-flight check executed before the curator LLM
// is spun up. It reads the statelog once and applies cheap threshold checks
// so shallow or trivial sessions are rejected without any model call.
//
// Order of checks (cheapest → most discriminating):
//  1. forced (session marked via /learn) → always proceed.
//  2. Statelog unreadable or empty → skip.
//  3. TurnCount < MinTurns → skip (session too short).
//  4. len(Decisions) >= 1 → proceed (explicit decision recorded).
//  5. sub-agent call count >= MinSubAgentCalls → proceed (real delegation).
//  6. else → skip (shallow Q&A session).
func curatorGate(statePath string, forced bool, agentNames []string, cfg CuratorGateConfig) bool {
	if forced {
		return true
	}
	if statePath == "" {
		return false
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	var sl struct {
		Decisions []string          `json:"decisions"`
		Files     map[string]string `json:"files"`
		Tools     map[string]int    `json:"tools"`
		TurnCount int               `json:"turn_count"`
	}
	if err := json.Unmarshal(data, &sl); err != nil {
		return false
	}
	if sl.TurnCount < cfg.minTurns() {
		return false
	}
	if len(sl.Decisions) > 0 {
		return true
	}
	subAgentCalls := 0
	for _, name := range agentNames {
		subAgentCalls += sl.Tools[name]
	}
	return subAgentCalls >= cfg.minSubAgentCalls()
}
