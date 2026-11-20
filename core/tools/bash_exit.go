package tools

import (
	"sync"

	"github.com/blouargant/omnis/core/adk"
)

// bashExitObserver, when set, is told the exit status of every command the Bash
// tool (and the read-only Bash) ran. It is a side channel: the text the model
// sees does not change. The agent's lessons machinery installs one so it can
// tell a failed shell command from a successful one.
var (
	bashExitObserverMu sync.RWMutex
	bashExitObserver   func(ctx adk.ToolContext, exitCode int, timedOut bool)
)

// SetBashExitObserver installs the Bash exit-status observer. Pass nil to clear
// it. Safe for concurrent use.
func SetBashExitObserver(f func(ctx adk.ToolContext, exitCode int, timedOut bool)) {
	bashExitObserverMu.Lock()
	bashExitObserver = f
	bashExitObserverMu.Unlock()
}

// notifyBashExit reports a command's exit status to the observer, if any. A
// command that never started (safety floor, read-only refusal) is not reported.
func notifyBashExit(ctx adk.ToolContext, st bashStatus) {
	if !st.ran {
		return
	}
	bashExitObserverMu.RLock()
	f := bashExitObserver
	bashExitObserverMu.RUnlock()
	if f != nil {
		f(ctx, st.exitCode, st.timedOut)
	}
}
