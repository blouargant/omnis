// subagent_hook.go — per-sub-agent-invocation softskill stats + tagging.
//
// Two responsibilities:
//
//   1. Bump LoadedCount on `<sub-agent>/<skill>` each time a sub-agent
//      invokes load_softskill (deduped per invocation).
//   2. Phase 6: buffer the sub-agent invocations observed inside a
//      leader run, then at EventRunEnd walk the buffer to detect
//      retries + lexically classify the leader's reaction, and apply
//      one tag per loaded skill (helpful/harmful/neutral) via
//      softskills.TagInvocation + Stats.RecordTag.
//
// Run-scoping uses the `run_id` payload field added in Phase 6 (matches
// ADK's InvocationID for the leader's outer run). Tool calls fired by
// the sub-agent's internal runner carry a *different* run_id but their
// EventBeforeTool/EventAfterTool flows through the same bus thanks to
// the AgentCallbacks attached in agent/squad.go.
//
// The leader-side `EventSubAgentStart` / `EventSubAgentEnd` events
// (synthesised in agent/subagent_event.go from the leader's BeforeTool /
// AfterTool / ToolError events) ARE keyed by the leader's run_id, which
// is the value we use as the buffer key here.

package agent

import (
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/softskills"
)

// invocation is one buffered EventSubAgentStart→End pair seen during a
// single leader run.
type invocation struct {
	agent      string
	callID     string
	endedAt    time.Time
	output     string
	hasError   bool
	loaded     []string        // skill keys loaded inside this invocation
	loadedSeen map[string]bool // dedup per (sub-agent-internal-session, key)
	toolErrors []softskills.ToolError
	subSession string // the sub-agent's internal session_id (for dedup)
	subRunID   string // the sub-agent's internal run_id (proxy for the invocation window)
}

// runBuffer holds all invocations + the leader's assistant text observed
// for one leader run.
type runBuffer struct {
	mu          sync.Mutex
	invocations []*invocation
	leaderText  strings.Builder
	leaderName  string
}

// registerSubAgentLoadHook subscribes to:
//   - EventAfterTool        → bump LoadedCount on every sub-agent load_softskill.
//   - EventSubAgentStart    → open an invocation in the current run buffer.
//   - EventSubAgentEnd      → close it (capture output + ts).
//   - EventToolError        → attribute tool errors to the open invocation
//     when the agent matches.
//   - EventAfterModel       → capture the leader's assistant text (used by
//     the leader-reaction lexical scan).
//   - EventRunEnd           → walk the buffer, classify retries + reaction,
//     RecordTag each invocation's loaded skills,
//     save stats, free the buffer.
//
// subAgentNames is the set of agent names treated as sub-agents.
// leaderNames is the set of agents whose AfterModel text counts as the
// "leader reaction" (typically the same as the squad leader).
func registerSubAgentLoadHook(
	bus *events.Bus,
	softDir string,
	subAgentNames []string,
	leaderNames []string,
) []*events.Subscription {
	subSet := nameSet(subAgentNames)
	leaderSet := nameSet(leaderNames)
	if len(subSet) == 0 {
		return nil
	}

	var bufMu sync.Mutex
	buffers := map[string]*runBuffer{} // key: leader run_id

	bufFor := func(runID, leaderName string) *runBuffer {
		bufMu.Lock()
		defer bufMu.Unlock()
		b, ok := buffers[runID]
		if !ok {
			b = &runBuffer{leaderName: leaderName}
			buffers[runID] = b
		} else if leaderName != "" && b.leaderName == "" {
			b.leaderName = leaderName
		}
		return b
	}
	dropBuf := func(runID string) *runBuffer {
		bufMu.Lock()
		defer bufMu.Unlock()
		b := buffers[runID]
		delete(buffers, runID)
		return b
	}

	// load_softskill counting (Phase 2 behaviour) — independent of the
	// invocation buffer so loads are still counted even when no run is
	// observed (e.g. tests that fire EventAfterTool directly).
	var seenMu sync.Mutex
	seen := map[string]struct{}{}
	afterTool := func(_ string, payload map[string]any) {
		tool, _ := payload["tool"].(string)
		agentName, _ := payload["agent"].(string)
		if tool == loadSoftSkillTool {
			if _, ok := subSet[agentName]; !ok {
				return
			}
			input, _ := payload["input"].(map[string]any)
			name, _ := input["name"].(string)
			if name == "" {
				return
			}
			sessionID, _ := payload["session_id"].(string)
			key := softskills.Key(agentName, name)
			dedupKey := sessionID + "::" + key

			seenMu.Lock()
			if _, dup := seen[dedupKey]; dup {
				seenMu.Unlock()
				return
			}
			seen[dedupKey] = struct{}{}
			seenMu.Unlock()

			s, err := softskills.LoadStats(softDir)
			if err != nil {
				log.Printf("subagent_hook: load stats: %v", err)
				return
			}
			s.RecordLoad(key, sessionID, time.Now().UTC())
			if err := s.Save(softDir); err != nil {
				log.Printf("subagent_hook: save stats: %v", err)
			}

			// Also attach to the current open invocation (if any) so
			// the run-end tagger can credit / debit this skill against
			// the right invocation.
			attachLoad(buffers, &bufMu, agentName, sessionID, key)
			return
		}
		// Non-load_softskill tools — no work needed.
	}

	// EventToolError inside a sub-agent invocation: route to the
	// invocation's tool-errors list so TagInvocation sees it.
	toolError := func(_ string, payload map[string]any) {
		agentName, _ := payload["agent"].(string)
		if _, ok := subSet[agentName]; !ok {
			return
		}
		sessionID, _ := payload["session_id"].(string)
		te := softskills.ToolError{
			Tool:  asString(payload["tool"]),
			Agent: agentName,
			Error: asString(payload["error"]),
			When:  time.Now().UTC(),
		}
		attachToolError(buffers, &bufMu, agentName, sessionID, te)
	}

	subStart := func(_ string, payload map[string]any) {
		runID, _ := payload["run_id"].(string)
		if runID == "" {
			return
		}
		callerAgent, _ := payload["caller_agent"].(string)
		b := bufFor(runID, callerAgent)
		inv := &invocation{
			agent:      asString(payload["agent"]),
			callID:     asString(payload["call_id"]),
			loadedSeen: map[string]bool{},
		}
		b.mu.Lock()
		b.invocations = append(b.invocations, inv)
		b.mu.Unlock()
	}

	subEnd := func(_ string, payload map[string]any) {
		runID, _ := payload["run_id"].(string)
		if runID == "" {
			return
		}
		agentName, _ := payload["agent"].(string)
		callID, _ := payload["call_id"].(string)
		bufMu.Lock()
		b, ok := buffers[runID]
		bufMu.Unlock()
		if !ok {
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		// Match the most recent open invocation for this (agent, callID).
		for i := len(b.invocations) - 1; i >= 0; i-- {
			inv := b.invocations[i]
			if inv.agent != agentName {
				continue
			}
			if callID != "" && inv.callID != "" && inv.callID != callID {
				continue
			}
			if !inv.endedAt.IsZero() {
				continue
			}
			inv.endedAt = time.Now().UTC()
			if out, ok := payload["output"].(map[string]any); ok {
				inv.output = outputAsText(out)
			}
			if errStr, ok := payload["error"].(string); ok && errStr != "" {
				inv.hasError = true
				inv.output = "Error: " + errStr
			}
			break
		}
	}

	// Capture leader assistant text on AfterModel for the lexical
	// reaction scan. Only the configured leader names contribute.
	afterModel := func(_ string, payload map[string]any) {
		agentName, _ := payload["agent"].(string)
		if _, ok := leaderSet[agentName]; !ok {
			return
		}
		runID, _ := payload["run_id"].(string)
		if runID == "" {
			return
		}
		text := extractAssistantText(payload["response"])
		if text == "" {
			return
		}
		b := bufFor(runID, agentName)
		b.mu.Lock()
		b.leaderText.WriteString(text)
		b.leaderText.WriteString("\n")
		b.mu.Unlock()
	}

	runEnd := func(_ string, payload map[string]any) {
		runID, _ := payload["run_id"].(string)
		if runID == "" {
			return
		}
		b := dropBuf(runID)
		if b == nil || len(b.invocations) == 0 {
			return
		}
		processRunBuffer(softDir, b)
	}

	return []*events.Subscription{
		bus.Subscribe(events.EventAfterTool, afterTool),
		bus.Subscribe(events.EventToolError, toolError),
		bus.Subscribe(events.EventSubAgentStart, subStart),
		bus.Subscribe(events.EventSubAgentEnd, subEnd),
		bus.Subscribe(events.EventAfterModel, afterModel),
		bus.Subscribe(events.EventRunEnd, runEnd),
	}
}

// processRunBuffer walks the buffer at EventRunEnd: detects retries,
// classifies leader reactions, and applies tags via Stats.RecordTag.
func processRunBuffer(softDir string, b *runBuffer) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Stable order — invocations should already be in order of subStart
	// arrival but we re-sort by endedAt as a safety net.
	sort.SliceStable(b.invocations, func(i, j int) bool {
		return b.invocations[i].endedAt.Before(b.invocations[j].endedAt)
	})

	// Phase 6 doesn't reuse the leader's complete text per gap; the
	// simplest correct rule is "if the same sub-agent appears later in
	// the run, the earlier call was a retry". For the leader reaction
	// scan we pass the whole leader text — keyword hits are conservative
	// enough that scoping doesn't materially change the verdict.
	leaderText := b.leaderText.String()
	laterCallsByAgent := map[string]int{}
	for _, inv := range b.invocations {
		laterCallsByAgent[inv.agent]++
	}

	s, err := softskills.LoadStats(softDir)
	if err != nil {
		log.Printf("subagent_hook: load stats at run_end: %v", err)
		return
	}
	changed := false
	seen := map[string]int{}
	for _, inv := range b.invocations {
		seen[inv.agent]++
		retried := seen[inv.agent] < laterCallsByAgent[inv.agent]
		reaction := softskills.ClassifyLeaderReaction(inv.agent, leaderText)
		tags := softskills.TagInvocation(softskills.SubAgentInvocation{
			Agent:          inv.agent,
			LoadedSkills:   inv.loaded,
			ToolErrors:     inv.toolErrors,
			OutputText:     inv.output,
			Retried:        retried,
			LeaderReaction: reaction,
		})
		for k, tag := range tags {
			s.RecordTag(k, tag)
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := s.Save(softDir); err != nil {
		log.Printf("subagent_hook: save stats at run_end: %v", err)
	}
}

// attachLoad appends a freshly-counted skill key onto the most recent
// open invocation that matches (agent, subSession). It's safe to call
// when no buffer is open for the run; the call simply no-ops.
func attachLoad(buffers map[string]*runBuffer, mu *sync.Mutex, agentName, subSession, key string) {
	mu.Lock()
	allBufs := make([]*runBuffer, 0, len(buffers))
	for _, b := range buffers {
		allBufs = append(allBufs, b)
	}
	mu.Unlock()

	// We don't know which leader run owns this load — we attach to
	// every open buffer that has an open invocation for this agent.
	// In practice only one such buffer exists at a time per host.
	for _, b := range allBufs {
		b.mu.Lock()
		for i := len(b.invocations) - 1; i >= 0; i-- {
			inv := b.invocations[i]
			if inv.agent != agentName || !inv.endedAt.IsZero() {
				continue
			}
			if inv.subSession == "" {
				inv.subSession = subSession
			}
			// Dedup per (subSession, key) within the invocation.
			dk := subSession + "::" + key
			if !inv.loadedSeen[dk] {
				inv.loadedSeen[dk] = true
				inv.loaded = append(inv.loaded, key)
			}
			break
		}
		b.mu.Unlock()
	}
}

// attachToolError routes a tool_error to the open invocation for the
// sub-agent. Same matching semantics as attachLoad.
func attachToolError(buffers map[string]*runBuffer, mu *sync.Mutex, agentName, subSession string, te softskills.ToolError) {
	mu.Lock()
	allBufs := make([]*runBuffer, 0, len(buffers))
	for _, b := range buffers {
		allBufs = append(allBufs, b)
	}
	mu.Unlock()

	for _, b := range allBufs {
		b.mu.Lock()
		for i := len(b.invocations) - 1; i >= 0; i-- {
			inv := b.invocations[i]
			if inv.agent != agentName || !inv.endedAt.IsZero() {
				continue
			}
			inv.toolErrors = append(inv.toolErrors, te)
			break
		}
		b.mu.Unlock()
	}
}

// asString coerces an any to a string (or empty when not a string).
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// outputAsText collapses the sub-agent's output payload (map[string]any)
// into a single text string for the leader-reaction / failure heuristics.
func outputAsText(out map[string]any) string {
	if out == nil {
		return ""
	}
	// Common shapes: {"result": "..."}, {"text": "..."}, {"output": "..."}.
	for _, k := range []string{"result", "text", "output", "summary"} {
		if s, ok := out[k].(string); ok && s != "" {
			return s
		}
	}
	// Fallback: concatenate every string-valued field.
	var b strings.Builder
	for _, v := range out {
		if s, ok := v.(string); ok && s != "" {
			b.WriteString(s)
			b.WriteString(" ")
		}
	}
	return strings.TrimSpace(b.String())
}

// extractAssistantText pulls the assistant-side text from an LLM
// response payload. The shape depends on the ADK adapter; we accept
// either a *model.LLMResponse-like object (already a string via Stringer),
// a map with `text` / `content`, or a raw string.
func extractAssistantText(resp any) string {
	if resp == nil {
		return ""
	}
	if s, ok := resp.(string); ok {
		return s
	}
	// model.LLMResponse exposes Stringer for many adapters; if not, the
	// payload is a struct we'd need reflection for — accept the cheap
	// path here (the lexical scan can survive on tool-call breadcrumbs
	// from sub-agent events if the leader text is empty).
	if stringer, ok := resp.(interface{ String() string }); ok {
		return stringer.String()
	}
	return ""
}
