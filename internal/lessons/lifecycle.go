package lessons

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lifecycle rules. Each is a pure function of a Lesson returning its new state;
// the Record* methods apply them under the agent's directory lock.

// addSession records sid as the most recent session, keeping the maxSessionIDs
// most recent distinct ones. The list never shrinks below its size, so the
// distinct-session count used for promotion never goes down.
func addSession(ids []string, sid string) []string {
	if sid == "" || strings.ContainsAny(sid, ", \r\n\t") {
		return ids
	}
	out := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		if id != sid {
			out = append(out, id)
		}
	}
	out = append(out, sid)
	if len(out) > maxSessionIDs {
		out = out[len(out)-maxSessionIDs:]
	}
	return out
}

// promoteState: a hint seen often enough in enough sessions becomes a prompt
// lesson, unless a prompt already failed to prevent it.
func promoteState(l Lesson) State {
	if l.State == StateHint && !l.PromptFailed && l.Seen >= PromoteSeen && len(l.SessionIDs) >= PromoteSessions {
		return StatePrompt
	}
	return l.State
}

// demoteSeenState: a prompt lesson whose failure keeps recurring goes back to
// a hint and is flagged so it is not promoted again.
func demoteSeenState(l Lesson) (State, bool) {
	if l.State == StatePrompt && l.SeenInPrompt >= DemoteSeenInPrompt {
		return StateHint, true
	}
	return l.State, l.PromptFailed
}

// idle reports whether a prompt lesson has not been seen for DemoteIdleDays.
func idle(l Lesson, now time.Time) bool {
	return l.State == StatePrompt && now.Sub(l.LastSeen) > idleSpan
}

const idleSpan = time.Duration(DemoteIdleDays) * 24 * time.Hour

// retireState: RetireRepeated failed hints among the last RetireWindow retire it.
func retireState(l Lesson) State {
	if l.State == StateRetired {
		return l.State
	}
	failed := 0
	for _, ok := range l.Outcomes {
		if !ok {
			failed++
		}
	}
	if failed >= RetireRepeated {
		return StateRetired
	}
	return l.State
}

// update reads the lesson id of agent under the directory lock, applies fn and
// writes it back. found is false when it does not exist.
func (s *Store) update(agent, id string, fn func(*Lesson)) (l Lesson, found bool, err error) {
	dir := s.dir(agent)
	if dir == "" || !idRE.MatchString(id) {
		return Lesson{}, false, fmt.Errorf("%w: agent or id", ErrInvalid)
	}
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()
	path := filepath.Join(dir, id+".md")
	l, ok := readLesson(path, id)
	if !ok || l.Agent != agent {
		return Lesson{}, false, nil
	}
	fn(&l)
	l, err = prepare(l)
	if err != nil {
		return Lesson{}, true, err
	}
	if err := writeAtomic(dir, path, render(l)); err != nil {
		return Lesson{}, true, err
	}
	return l, true, nil
}

var errNoLesson = fmt.Errorf("%w: no such lesson", os.ErrNotExist)

// RecordSeen counts one error occurrence. found is false when no lesson exists.
// A retired lesson is returned unchanged in state (counters still move).
func (s *Store) RecordSeen(agent, signature, sessionID string, now time.Time) (l Lesson, found bool, err error) {
	if s.dir(agent) == "" {
		return Lesson{}, false, fmt.Errorf("%w: agent name", ErrInvalid)
	}
	if !sigRE.MatchString(signature) {
		return Lesson{}, false, nil
	}
	return s.update(agent, signature[:12], func(l *Lesson) {
		if l.Signature != signature {
			return
		}
		l.Seen++
		l.LastSeen = now
		l.SessionIDs = addSession(l.SessionIDs, sessionID)
		if l.State == StatePrompt {
			l.SeenInPrompt++
			l.State, l.PromptFailed = demoteSeenState(*l)
		} else {
			l.State = promoteState(*l)
			if l.State == StatePrompt {
				l.SeenInPrompt = 0
				l.PromotedSeen = l.Seen
			}
		}
	})
}

// RecordHint counts one hint shown to the agent.
func (s *Store) RecordHint(agent, id string) error {
	_, found, err := s.update(agent, id, func(l *Lesson) { l.Hinted++ })
	if err != nil {
		return err
	}
	if !found {
		return errNoLesson
	}
	return nil
}

// RecordOutcome records whether the call recovered after the lesson's hint, and
// retires the lesson when its hints do not help.
func (s *Store) RecordOutcome(agent, id string, recovered bool) (Lesson, error) {
	l, found, err := s.update(agent, id, func(l *Lesson) {
		if recovered {
			l.Recovered++
		} else {
			l.Repeated++
		}
		l.Outcomes = append(l.Outcomes, recovered)
		if len(l.Outcomes) > RetireWindow {
			l.Outcomes = l.Outcomes[len(l.Outcomes)-RetireWindow:]
		}
		l.State = retireState(*l)
	})
	if err != nil {
		return Lesson{}, err
	}
	if !found {
		return Lesson{}, errNoLesson
	}
	return l, nil
}
