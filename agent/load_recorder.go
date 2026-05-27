// load_recorder.go — session-scope softskill stats recorder + heuristic
// reflector.
//
// Subscribes to three bus events:
//
//   - EventAfterTool      → per-session bucket: leader-loaded skills
//                           (sub-agent loads are handled by
//                           subagent_hook.go to avoid double-counting).
//   - EventToolError      → per-session bucket: tool errors.
//   - EventSessionEnd     → drain the buckets, gather signals from disk,
//                           run softskills.ReflectHeuristic, then
//                           RecordLoad+RecordTag the loaded skills and
//                           save softskills/_stats.json once.
//
// Phase 1 only recorded loads. Phase 2 layers tag-on-outcome on top of
// the same flow so we save the file exactly once per session end.

package agent

import (
	"log"
	"sync"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/softskills"
)

const loadSoftSkillTool = "load_softskill"

// sessionBucket holds the in-memory state accumulated for one session
// between its first event and EventSessionEnd.
type sessionBucket struct {
	// loaded lists skills the leader loaded during the session, in the
	// order they were observed. Same key is recorded only once (the
	// first time) — repeat loads in a session contribute 1 to the
	// LoadedCount.
	loaded     []softskills.LoadedSkill
	loadedKeys map[string]struct{}

	toolErrors []softskills.ToolError
}

// registerLoadRecorderHook subscribes the per-session recorder + reflector
// to the bus and returns the resulting subscriptions for hot-reload
// teardown.
//
// leaderNames is the set of agent names that act as a squad leader; only
// loads from those agents contribute to this hook. Sub-agent loads have
// their own counting path (see subagent_hook.go).
//
// subAgentNames is the set of sub-agent names. After-tool events whose
// `tool` matches a sub-agent are tool calls TO the sub-agent (made by
// the leader) and must not be miscounted as skill loads.
func registerLoadRecorderHook(
	bus *events.Bus,
	softDir string,
	leaderNames []string,
	sessionSuffix func(userID, sessionID string) string,
) []*events.Subscription {
	if sessionSuffix == nil {
		sessionSuffix = sessionKeyResolver
	}
	leaderSet := nameSet(leaderNames)

	var mu sync.Mutex
	pending := map[string]*sessionBucket{}

	bucket := func(sessionID string) *sessionBucket {
		b, ok := pending[sessionID]
		if !ok {
			b = &sessionBucket{loadedKeys: map[string]struct{}{}}
			pending[sessionID] = b
		}
		return b
	}

	afterTool := func(_ string, payload map[string]any) {
		sessionID, _ := payload["session_id"].(string)
		if sessionID == "" {
			return
		}
		tool, _ := payload["tool"].(string)
		if tool != loadSoftSkillTool {
			return
		}
		input, _ := payload["input"].(map[string]any)
		name, _ := input["name"].(string)
		if name == "" {
			return
		}
		agentName, _ := payload["agent"].(string)
		// Only count leader-loaded skills here. Sub-agent loads are
		// counted in subagent_hook.go on the per-invocation boundary so
		// each call attributes correctly even before Phase 6 retry
		// detection lands.
		if _, isLeader := leaderSet[agentName]; !isLeader && agentName != "" {
			return
		}
		key := softskills.Key("", name)

		mu.Lock()
		defer mu.Unlock()
		b := bucket(sessionID)
		if _, seen := b.loadedKeys[key]; seen {
			return
		}
		b.loadedKeys[key] = struct{}{}
		b.loaded = append(b.loaded, softskills.LoadedSkill{
			Key:  key,
			When: time.Now().UTC(),
		})
	}

	toolError := func(_ string, payload map[string]any) {
		sessionID, _ := payload["session_id"].(string)
		if sessionID == "" {
			return
		}
		tool, _ := payload["tool"].(string)
		if tool == "" {
			return
		}
		agentName, _ := payload["agent"].(string)
		errStr, _ := payload["error"].(string)
		callID, _ := payload["call_id"].(string)

		mu.Lock()
		defer mu.Unlock()
		b := bucket(sessionID)
		b.toolErrors = append(b.toolErrors, softskills.ToolError{
			Tool:   tool,
			Agent:  agentName,
			Error:  errStr,
			When:   time.Now().UTC(),
			CallID: callID,
		})
	}

	sessionEnd := func(_ string, payload map[string]any) {
		sessionID, _ := payload["session_id"].(string)
		if sessionID == "" {
			return
		}
		userID, _ := payload["user_id"].(string)

		mu.Lock()
		b, ok := pending[sessionID]
		if ok {
			delete(pending, sessionID)
		}
		mu.Unlock()
		if !ok || (len(b.loaded) == 0 && len(b.toolErrors) == 0) {
			return
		}

		// session key (matches Infrastructure.SessionSuffix) locates the
		// per-session statelog file; sessionID alone locates the
		// conversation file.
		key := sessionSuffix(userID, sessionID)

		// Explicit feedback (Phase 5): the wrap-session skill persists
		// the user's wrap-up answer to logs/agent_feedback_<key>.json.
		// When present it dominates the implicit user-message scan.
		feedbackAnswer := ""
		if fb, err := softskills.LoadFeedback(paths.LogsDir(), key); err == nil && fb != nil {
			feedbackAnswer = fb.Answer
		}

		signals := gatherSessionSignals(
			key,
			sessionID,
			feedbackAnswer,
			b.toolErrors,
			b.loaded,
		)
		outcome := softskills.ReflectHeuristic(signals)

		s, err := softskills.LoadStats(softDir)
		if err != nil {
			log.Printf("load_recorder: load stats for session %s: %v", sessionID, err)
			return
		}
		now := time.Now().UTC()
		for _, ls := range b.loaded {
			s.RecordLoad(ls.Key, sessionID, now)
			if tag, ok := outcome.Tags[ls.Key]; ok {
				s.RecordTag(ls.Key, tag)
			}
		}
		if err := s.Save(softDir); err != nil {
			log.Printf("load_recorder: save stats for session %s: %v", sessionID, err)
		}

		// Hand off the heuristic data to any downstream consumer
		// (curator_hook layers the LLM reflector on top). The payload
		// is self-contained so subscribers don't need access to this
		// goroutine's local buckets.
		emitSessionReflected(bus, userID, sessionID, key, feedbackAnswer, b, outcome, signals)
	}

	return []*events.Subscription{
		bus.Subscribe(events.EventAfterTool, afterTool),
		bus.Subscribe(events.EventToolError, toolError),
		bus.Subscribe(events.EventSessionEnd, sessionEnd),
	}
}

// sessionKeyResolver is overridable so tests can stub out the suffix
// resolver without standing up an Infrastructure. Defaults to the
// userID-only key (sufficient when build-timestamps are out of scope).
var sessionKeyResolver = func(userID, sessionID string) string {
	if userID != "" && sessionID != "" {
		return userID + "_" + sessionID
	}
	if sessionID != "" {
		return sessionID
	}
	return userID
}

// emitSessionReflected publishes EventSessionReflected with the data a
// downstream LLM reflector + curator needs. Wraps slices into
// map[string]any payloads so subscribers don't depend on the
// internal/softskills types directly (which would tighten coupling).
func emitSessionReflected(
	bus *events.Bus,
	userID, sessionID, key, explicitFeedback string,
	b *sessionBucket,
	heuristic softskills.Outcome,
	signals softskills.HeuristicInputs,
) {
	loadedKeys := make([]string, 0, len(b.loaded))
	for _, ls := range b.loaded {
		loadedKeys = append(loadedKeys, ls.Key)
	}
	heurTags := map[string]string{}
	for k, v := range heuristic.Tags {
		heurTags[k] = v
	}
	toolErrors := make([]map[string]any, 0, len(b.toolErrors))
	for _, te := range b.toolErrors {
		toolErrors = append(toolErrors, map[string]any{
			"tool":  te.Tool,
			"agent": te.Agent,
			"error": te.Error,
			"when":  te.When,
		})
	}
	bus.Emit(events.EventSessionReflected, map[string]any{
		"user_id":            userID,
		"session_id":         sessionID,
		"session_key":        key,
		"loaded_skills":      loadedKeys,
		"heuristic_success":  heuristic.Success.String(),
		"heuristic_tags":     heurTags,
		"tool_errors":        toolErrors,
		"last_user_messages": signals.LastUserMessages,
		"explicit_feedback":  explicitFeedback,
	})
}

// nameSet builds a fast membership lookup from a list of names. Blanks
// are dropped.
func nameSet(names []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, n := range names {
		if n != "" {
			out[n] = struct{}{}
		}
	}
	return out
}
