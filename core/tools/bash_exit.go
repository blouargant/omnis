package tools

import (
	"sync"

	"github.com/blouargant/omnis/core/adk"
)

// bashExitObserver, when set, is told the exit status of every command the Bash
// tool (and the read-only Bash) ran. It is a side channel: the text the model
// sees does not change. The agent's lessons machinery installs one so it can
// tell a failed shell command from a successful one. bashExitOwner identifies
// who installed it, so an owner can remove its own observer without removing
// one another owner installed since.
var (
	bashExitObserverMu sync.RWMutex
	bashExitObserver   func(ctx adk.ToolContext, exitCode int, timedOut bool)
	bashExitOwner      any
)

// SetBashExitObserver installs the Bash exit-status observer with no owner.
// Pass nil to clear it. Safe for concurrent use.
func SetBashExitObserver(f func(ctx adk.ToolContext, exitCode int, timedOut bool)) {
	InstallBashExitObserver(nil, f)
}

// InstallBashExitObserver installs f as the Bash exit-status observer on behalf
// of owner (compared with ==, so use a pointer), replacing any other.
func InstallBashExitObserver(owner any, f func(ctx adk.ToolContext, exitCode int, timedOut bool)) {
	bashExitObserverMu.Lock()
	bashExitObserver, bashExitOwner = f, owner
	if f == nil {
		bashExitOwner = nil
	}
	bashExitObserverMu.Unlock()
}

// RemoveBashExitObserver clears the observer only when owner installed the
// current one; another owner's observer is left in place.
func RemoveBashExitObserver(owner any) {
	bashExitObserverMu.Lock()
	if bashExitObserver != nil && bashExitOwner == owner {
		bashExitObserver, bashExitOwner = nil, nil
	}
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
