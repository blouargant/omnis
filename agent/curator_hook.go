// curator_hook.go — wires the soft-skills curator to the event bus.
//
// On every EventSessionEnd, we spawn the curator agent in a goroutine
// with a 2-minute timeout. The curator reads the per-session audit and
// state log files (written by the compress plugin) and decides whether
// to create or update one soft-skill. Failures are logged and swallowed:
// curation must never break the user-facing session.
package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"google.golang.org/adk/model"

	"github.com/blouargant/agent-toolkit/core/events"
	"github.com/blouargant/agent-toolkit/internal/softskills"
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

// registerCuratorHook subscribes a handler to EventSessionEnd that fires
// the curator agent in a detached goroutine. It is intentionally lazy:
// the curator agent + runner are built on-demand inside the goroutine so
// (a) startup cost is paid only when the hook actually fires and (b) a
// curator construction error never aborts the main agent boot.
func registerCuratorHook(bus *events.Bus, llm model.LLM, softDir, skillsDir string, sessionSuffix func(u, s string) string) {
	bus.On(events.EventSessionEnd, func(_ string, payload map[string]any) {
		userID, _ := payload["user_id"].(string)
		sessionID, _ := payload["session_id"].(string)
		if userID == "" || sessionID == "" {
			return // Cannot locate the per-session files without IDs.
		}
		key := sessionSuffix(userID, sessionID)
		auditPath := fmt.Sprintf(".agent_memory_%s.md", key)
		statePath := fmt.Sprintf(".agent_statelog_%s.json", key)

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

			// Skip if neither input exists — nothing to learn from.
			if !fileExists(auditPath) && !fileExists(statePath) {
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), curateTimeout)
			defer cancel()

			r, err := softskills.CuratorRunner(ctx, softskills.CuratorConfig{
				Model:         llm,
				SoftSkillsDir: softDir,
				SkillsDir:     skillsDir,
			})
			if err != nil {
				log.Printf("curator: build failed for session %s: %v", key, err)
				return
			}
			if _, err := softskills.Curate(ctx, r, softskills.CurateInputs{
				AuditPath:    auditPath,
				StateLogPath: statePath,
			}); err != nil {
				log.Printf("curator: run failed for session %s: %v", key, err)
				return
			}
		}()
	})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
