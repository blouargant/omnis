package agent

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/blouargant/omnis/internal/lessons"
)

const (
	lessonArgsCap = 2000
	lessonMaxRuns = 512
)

// lessonEpisode is a failure of a tool followed by a success of the same tool
// in the same run: the raw material of a lesson.
type lessonEpisode struct {
	Root, SessionID, Agent, Tool, ToolKey, Signature string
	FailedArgs, ErrText, WorkedArgs                  string // JSON, capped at 2000 chars each
	Attempts                                         int
}

type runKey struct{ session, invocation, agent string }

type openFailure struct {
	sig      string
	args     string
	errText  string
	tool     string
	root     string
	doneAt   time.Time
	attempts int
}

type runState struct {
	open map[string]*openFailure // by tool key
	seq  uint64
}

type lessonTracker struct {
	mu        sync.Mutex
	runs      map[runKey]*runState
	seq       uint64
	onEpisode func(lessonEpisode)
	now       func() time.Time
}

func newLessonTracker(onEpisode func(lessonEpisode)) *lessonTracker {
	return &lessonTracker{runs: map[runKey]*runState{}, onEpisode: onEpisode, now: time.Now}
}

func capJSON(args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	s := string(b)
	if r := []rune(s); len(r) > lessonArgsCap {
		s = string(r[:lessonArgsCap])
	}
	return s
}

// observe records one finished call; it returns the open failure's signature when
// this call failed, and the closed episode when this call succeeded after a failure.
// The episode is also handed to onEpisode, if set.
func (t *lessonTracker) observe(k runKey, root, tool string, args map[string]any, errText string, failed bool, started time.Time) (sig string, ep *lessonEpisode) {
	toolKey := lessons.ToolKey(tool, args)
	t.mu.Lock()
	rs := t.runs[k]
	if rs == nil {
		if !failed {
			t.mu.Unlock()
			return "", nil
		}
		rs = &runState{open: map[string]*openFailure{}}
		t.runs[k] = rs
	}
	t.seq++
	rs.seq = t.seq
	t.evictLocked()
	if failed {
		sig = lessons.Signature(toolKey, errText)
		of := rs.open[toolKey]
		if of != nil && of.sig == sig {
			of.attempts++
			of.args, of.errText, of.doneAt = capJSON(args), errText, t.now()
		} else {
			rs.open[toolKey] = &openFailure{sig: sig, args: capJSON(args), errText: errText,
				tool: tool, root: root, doneAt: t.now(), attempts: 1}
		}
		t.mu.Unlock()
		return sig, nil
	}
	of := rs.open[toolKey]
	if of == nil || started.Before(of.doneAt) {
		t.mu.Unlock()
		return "", nil
	}
	delete(rs.open, toolKey)
	e := lessonEpisode{
		Root: of.root, SessionID: k.session, Agent: k.agent, Tool: of.tool, ToolKey: toolKey,
		Signature: of.sig, FailedArgs: of.args, ErrText: of.errText, WorkedArgs: capJSON(args),
		Attempts: of.attempts,
	}
	t.mu.Unlock()
	if t.onEpisode != nil {
		t.onEpisode(e)
	}
	return "", &e
}

// evictLocked drops the oldest run states beyond the bound.
func (t *lessonTracker) evictLocked() {
	for len(t.runs) > lessonMaxRuns {
		var oldest runKey
		var min uint64
		first := true
		for k, rs := range t.runs {
			if first || rs.seq < min {
				oldest, min, first = k, rs.seq, false
			}
		}
		delete(t.runs, oldest)
	}
}

func (t *lessonTracker) forgetSession(session string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.runs {
		if k.session == session {
			delete(t.runs, k)
		}
	}
}
