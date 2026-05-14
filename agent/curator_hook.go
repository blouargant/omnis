// curator_hook.go — wires the soft-skills curator to the event bus.
//
// On EventSessionEnd (real session shutdown — emitted once by the TUI on
// quit, or by the launcher entry point — NOT the per-turn EventRunEnd),
// or on EventCurateNow (explicit trigger from /learn-now or idle scanner),
// we spawn the curator agent in a goroutine with a 2-minute timeout.
// The curator reads the per-session audit and state log files (written
// by the compress plugin) and decides whether to create, update, or delete
// soft-skills. Failures are logged and swallowed: curation must never
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
	"github.com/blouargant/yoke/internal/softskills"
)

// curateTimeout caps how long a single curator invocation may run.
// 2 minutes is generous for read-2-files + 1 LLM call + 3 tool calls;
// anything longer is almost certainly a runaway loop.
const curateTimeout = 2 * time.Minute

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

// registerCuratorHook subscribes a handler to EventSessionEnd that fires
// the curator agent in a detached goroutine. It is intentionally lazy:
// the curator agent + runner are built on-demand inside the goroutine so
// (a) startup cost is paid only when the hook actually fires and (b) a
// curator construction error never aborts the main agent boot.
func registerCuratorHook(bus *events.Bus, llm model.LLM, softDir, skillsDir string, agentNames []string, gate CuratorGateConfig, sessionSuffix func(u, s string) string) {
	handler := func(_ string, payload map[string]any) {
		userID, _ := payload["user_id"].(string)
		sessionID, _ := payload["session_id"].(string)
		if userID == "" || sessionID == "" {
			return // Cannot locate the per-session files without IDs.
		}
		key := sessionSuffix(userID, sessionID)
		forced := CurateSessionRequested(key)
		auditPath := filepath.Join("logs", fmt.Sprintf("agent_memory_%s.md", key))
		statePath := filepath.Join("logs", fmt.Sprintf("agent_statelog_%s.json", key))

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
			// sessions that are too shallow to have learnable procedures.
			// Forced sessions (/learn) bypass all threshold checks.
			if !curatorGate(statePath, forced, agentNames, gate) {
				if forced {
					emitEnd("", true, "session too shallow for soft-skill curation", nil)
				}
				return
			}

			bus.Emit(events.EventCuratorStart, map[string]any{
				"user_id":    userID,
				"session_id": sessionID,
			})

			ctx, cancel := context.WithTimeout(context.Background(), curateTimeout)
			defer cancel()

			r, err := softskills.CuratorRunner(ctx, softskills.CuratorConfig{
				Model:         llm,
				SoftSkillsDir: softDir,
				SkillsDir:     skillsDir,
				AgentNames:    agentNames,
			})
			if err != nil {
				log.Printf("curator: build failed for session %s: %v", key, err)
				emitEnd("", false, "", fmt.Errorf("curator build failed: %w", err))
				return
			}
			summary, err := softskills.Curate(ctx, r, softskills.CurateInputs{
				AuditPath:    auditPath,
				StateLogPath: statePath,
				AgentNames:   agentNames,
			})
			if err != nil {
				log.Printf("curator: run failed for session %s: %v", key, err)
				emitEnd("", false, "", fmt.Errorf("curation failed: %w", err))
				return
			}
			emitEnd(summary, false, "", nil)
		}()
	}
	bus.On(events.EventSessionEnd, handler)
	bus.On(events.EventCurateNow, handler)
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
		Decisions  []string       `json:"decisions"`
		Files      map[string]string `json:"files"`
		Tools      map[string]int `json:"tools"`
		TurnCount  int            `json:"turn_count"`
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
