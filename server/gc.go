package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/sessions"
)

// logsDir returns the per-user logs directory ($YOKE_HOME/logs). Resolved
// at each call so tests can redirect via t.Setenv("YOKE_HOME", ...).
func logsDir() string { return paths.LogsDir() }

// defaultGCInterval is the period between sweeps when YOKE_SERVER_GC_INTERVAL
// is unset. Sweeps are cheap (a few stat calls per file in logs/) so an hourly
// cadence is conservative.
const defaultGCInterval = time.Hour

// gcStats summarises what a single sweep removed. Reported via the admin
// endpoint and logged after every periodic sweep.
type gcStats struct {
	Conversations   int      `json:"conversations"`
	AgentFiles      int      `json:"agent_files"`
	Uploads         int      `json:"uploads"`
	Mailboxes       int      `json:"mailboxes"`
	RegistryEntries int      `json:"registry_entries"`
	Errors          []string `json:"errors,omitempty"`
}

func (s gcStats) total() int {
	return s.Conversations + s.AgentFiles + s.Uploads + s.Mailboxes + s.RegistryEntries
}

// gcDeps bundles the optional cross-session-registry callbacks. Both are
// nil-safe; when nil the registry sweep is skipped (e.g. in unit tests that
// don't wire up the full agent).
type gcDeps struct {
	listRegistry func() map[string]string
	unregister   func(displayName string) error
}

// parseGCInterval reads YOKE_SERVER_GC_INTERVAL. Returns the configured
// duration and an enabled flag. "0" or "off" disables the periodic sweep.
// Invalid values fall back to the default with a warning.
func parseGCInterval(raw string) (time.Duration, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return defaultGCInterval, true
	}
	if v == "0" || strings.EqualFold(v, "off") || strings.EqualFold(v, "disabled") {
		return 0, false
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("gc: invalid YOKE_SERVER_GC_INTERVAL=%q, using default %s", raw, defaultGCInterval)
		return defaultGCInterval, true
	}
	return d, true
}

// activeSession represents the identifiers the GC needs to recognise files
// belonging to a live session.
type activeSession struct {
	id     string
	suffix string
}

func activeFromRegistry(reg *sessions.Registry) (ids map[string]struct{}, suffixes map[string]struct{}) {
	ids = make(map[string]struct{})
	suffixes = make(map[string]struct{})
	for _, m := range reg.List() {
		ids[m.ID] = struct{}{}
		suffixes[agent.SessionSuffix(m.UserID, m.ID)] = struct{}{}
	}
	return ids, suffixes
}

// agentLogPrefixes maps each per-session agent log filename prefix to its
// extension. The suffix lives between the prefix and the extension.
var agentLogPrefixes = []struct {
	prefix string
	ext    string
}{
	{"agent_tasks_", ".json"},
	{"agent_todo_", ".json"},
	{"agent_memory_", ".md"},
	{"agent_statelog_", ".json"},
}

// runGC scans logs/ and .mailboxes/ once and deletes anything that doesn't
// belong to a session currently in the registry. The global
// agent_events_<ts>.log is intentionally preserved (one file per build,
// shared across all sessions). When deps.listRegistry is set, stale entries
// in the cross-session mailbox registry (.mailboxes/sessions.json) are
// pruned too.
func runGC(reg *sessions.Registry, deps gcDeps) gcStats {
	var stats gcStats
	activeIDs, activeSuffixes := activeFromRegistry(reg)

	stats = sweepLogsDir(stats, activeIDs, activeSuffixes)
	stats = sweepUploadsDir(stats, activeIDs)
	stats = sweepMailboxesDir(stats, activeSuffixes)
	stats = sweepMailboxRegistry(stats, activeSuffixes, deps)
	return stats
}

func sweepLogsDir(stats gcStats, activeIDs, activeSuffixes map[string]struct{}) gcStats {
	entries, err := os.ReadDir(logsDir())
	if err != nil {
		if !os.IsNotExist(err) {
			stats.Errors = append(stats.Errors, fmt.Sprintf("read %s: %v", logsDir(), err))
		}
		return stats
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		full := filepath.Join(logsDir(), name)

		if strings.HasPrefix(name, "conversation_") && strings.HasSuffix(name, ".json") {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "conversation_"), ".json")
			if _, ok := activeIDs[id]; ok {
				continue
			}
			if err := os.Remove(full); err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("remove %s: %v", full, err))
				continue
			}
			stats.Conversations++
			continue
		}

		for _, p := range agentLogPrefixes {
			if !strings.HasPrefix(name, p.prefix) || !strings.HasSuffix(name, p.ext) {
				continue
			}
			suffix := strings.TrimSuffix(strings.TrimPrefix(name, p.prefix), p.ext)
			if _, ok := activeSuffixes[suffix]; ok {
				break
			}
			if err := os.Remove(full); err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("remove %s: %v", full, err))
				break
			}
			stats.AgentFiles++
			break
		}
		// Anything else (notably agent_events_<ts>.log) is left alone.
	}
	return stats
}

func sweepUploadsDir(stats gcStats, activeIDs map[string]struct{}) gcStats {
	entries, err := os.ReadDir(uploadsBaseDir())
	if err != nil {
		if !os.IsNotExist(err) {
			stats.Errors = append(stats.Errors, fmt.Sprintf("read %s: %v", uploadsBaseDir(), err))
		}
		return stats
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, ok := activeIDs[id]; ok {
			continue
		}
		path := filepath.Join(uploadsBaseDir(), id)
		if err := os.RemoveAll(path); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("remove %s: %v", path, err))
			continue
		}
		stats.Uploads++
	}
	return stats
}

func sweepMailboxesDir(stats gcStats, activeSuffixes map[string]struct{}) gcStats {
	mailboxesDir := paths.MailboxesDir()
	entries, err := os.ReadDir(mailboxesDir)
	if err != nil {
		if !os.IsNotExist(err) {
			stats.Errors = append(stats.Errors, fmt.Sprintf("read %s: %v", mailboxesDir, err))
		}
		return stats
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		// Per-session mailbox files use the form "<suffix>:<peer>.jsonl".
		idx := strings.IndexByte(name, ':')
		if idx <= 0 {
			continue
		}
		suffix := name[:idx]
		if _, ok := activeSuffixes[suffix]; ok {
			continue
		}
		full := filepath.Join(mailboxesDir, name)
		if err := os.Remove(full); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("remove %s: %v", full, err))
			continue
		}
		stats.Mailboxes++
	}
	return stats
}

// startGC runs an initial sweep, then schedules periodic sweeps every
// `interval` until ctx is done. Safe to call with a zero interval — it
// performs the initial sweep and returns without starting a ticker.
func startGC(ctx context.Context, reg *sessions.Registry, deps gcDeps, interval time.Duration) {
	s := runGC(reg, deps)
	logGCStats("startup", s)
	if interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				logGCStats("periodic", runGC(reg, deps))
			}
		}
	}()
}

// sweepMailboxRegistry removes entries from .mailboxes/sessions.json whose
// underlying session is no longer in the registry. Detection uses the
// mailbox address (format: "<userID>_<sessionID>:<name>") rather than the
// display name, so renamed sessions are preserved (their address suffix
// stays stable across renames).
func sweepMailboxRegistry(stats gcStats, activeSuffixes map[string]struct{}, deps gcDeps) gcStats {
	if deps.listRegistry == nil || deps.unregister == nil {
		return stats
	}
	for displayName, addr := range deps.listRegistry() {
		idx := strings.IndexByte(addr, ':')
		if idx <= 0 {
			// Unparseable address — leave it alone rather than risk
			// deleting an entry we don't understand.
			continue
		}
		suffix := addr[:idx]
		if _, ok := activeSuffixes[suffix]; ok {
			continue
		}
		if err := deps.unregister(displayName); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("unregister %q: %v", displayName, err))
			continue
		}
		stats.RegistryEntries++
	}
	return stats
}

func logGCStats(kind string, s gcStats) {
	if s.total() == 0 && len(s.Errors) == 0 {
		return
	}
	log.Printf("gc(%s): removed %d conversations, %d agent files, %d upload dirs, %d mailbox files, %d registry entries (errors=%d)",
		kind, s.Conversations, s.AgentFiles, s.Uploads, s.Mailboxes, s.RegistryEntries, len(s.Errors))
	for _, e := range s.Errors {
		log.Printf("gc(%s) error: %s", kind, e)
	}
}
