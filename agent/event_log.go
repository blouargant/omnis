package agent

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/paths"
)

// eventLogCache memoises the process-wide event audit log on Infrastructure.
// Like the MCP pool, the LSP manager and the hooks engine it is built exactly
// once and survives hot-reload — see EventLog for why that matters.
type eventLogCache struct {
	once  sync.Once
	path  string
	close func() error
	err   error
}

// eventLogEvents is the set of bus events mirrored into the audit log.
var eventLogEvents = []string{
	events.EventBeforeTool, events.EventAfterTool,
	events.EventBeforeModel, events.EventAfterModel,
	events.EventToolError,
	events.EventSessionStart, events.EventSessionEnd,
	events.EventRunStart, events.EventRunEnd,
	events.EventCurateNow,
}

// EventLog opens the process-wide event audit log
// ($OMNIS_HOME/logs/agent_events_<buildTimestamp>.log) and subscribes it to the
// shared bus EXACTLY ONCE per process, memoised under a sync.Once. Idempotent:
// every later call (one per squad, per generation) is a no-op that reuses the
// single open file + handler.
//
// This ownership is load-bearing, not cosmetic. The logger used to be opened
// inside buildPlugins, which runs once per SQUAD (7 in the shipped config) and
// again for every squad of every hot-reloaded generation. Each call opened its
// own fd on the SAME path and subscribed its own handler to the SAME
// process-wide bus, so every event was written once per live logger (a
// before_tool line duplicated 14× = 7 squads × 2 generations) and the
// independent per-instance mutexes let concurrent writers interleave mid-line,
// leaving the audit trail unparseable. The file is one-per-build (see
// server/gc.go), so it belongs here beside the other process-wide components.
//
// fullPayload (Options.DebugLogging, i.e. the -d flag) is a process-level flag
// fixed for the lifetime of the process, so honouring the first caller's value
// is exact — a hot-reload cannot change it.
//
// The returned error is the open error, if any; it is memoised too, so a failure
// is reported to every caller and never retried mid-process. The file is closed
// by Infrastructure.Close (NOT by a generation teardown — an old generation
// draining must never close a log the current one is still writing to).
func (i *Infrastructure) EventLog(fullPayload bool) error {
	if i == nil || i.Bus == nil {
		return nil
	}
	i.eventLog.once.Do(func() {
		logsDir := paths.LogsDir()
		if err := os.MkdirAll(logsDir, 0o755); err != nil {
			i.eventLog.err = err
			return
		}
		path := filepath.Join(logsDir, "agent_events_"+i.BuildTimestamp+".log")
		logger, closeLog, err := events.FileLoggerWithOptions(
			path,
			events.FileLoggerOptions{FullPayload: fullPayload},
		)
		if err != nil {
			i.eventLog.err = err
			return
		}
		// bus.On (not Subscribe): the logger lives for the whole process, so it
		// is never detached — that is precisely the property a generation-scoped
		// subscription must not have.
		for _, ev := range eventLogEvents {
			i.Bus.On(ev, logger)
		}
		i.eventLog.path = path
		i.eventLog.close = closeLog
	})
	return i.eventLog.err
}

// EventLogPath returns the path of the process-wide event audit log, or "" when
// it has not been opened (or failed to open).
func (i *Infrastructure) EventLogPath() string {
	if i == nil {
		return ""
	}
	return i.eventLog.path
}

// closeEventLog closes the process-wide event log file. Called from
// Infrastructure.Close on process shutdown — never on generation teardown.
func (i *Infrastructure) closeEventLog() error {
	if i == nil || i.eventLog.close == nil {
		return nil
	}
	return i.eventLog.close()
}
