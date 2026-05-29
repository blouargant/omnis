// precedents_hook.go — indexes each finalised session's StateLog (goal +
// decisions) into the cross-session precedent index when the reflection
// pipeline fires EventSessionReflected. Cheap and best-effort: failures are
// logged and never break the session.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/compress"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/precedents"
)

// registerPrecedentsHook subscribes to EventSessionReflected and upserts the
// session's goal + decisions into the precedent index. Returns the bus
// subscriptions so the Instance can detach them on Close.
func registerPrecedentsHook(
	bus *events.Bus,
	store *precedents.Store,
	sessionSuffix func(u, s string) string,
) []*events.Subscription {
	handler := func(_ string, payload map[string]any) {
		userID, _ := payload["user_id"].(string)
		sessionID, _ := payload["session_id"].(string)
		if userID == "" || sessionID == "" {
			return
		}
		key, _ := payload["session_key"].(string)
		if key == "" {
			key = sessionSuffix(userID, sessionID)
		}
		statePath := filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_statelog_%s.json", key))
		sl := readStateLog(statePath)
		if sl == nil {
			return
		}
		ts := time.Now()
		if st, err := os.Stat(statePath); err == nil {
			ts = st.ModTime()
		}
		if err := store.IndexStateLog(context.Background(), key, sl, ts); err != nil {
			log.Printf("precedents: index session %s: %v", key, err)
		}
	}
	return []*events.Subscription{bus.Subscribe(events.EventSessionReflected, handler)}
}

// readStateLog parses a statelog JSON file, returning nil on any error.
func readStateLog(path string) *compress.StateLog {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	var sl compress.StateLog
	if err := json.Unmarshal(data, &sl); err != nil {
		return nil
	}
	return &sl
}
