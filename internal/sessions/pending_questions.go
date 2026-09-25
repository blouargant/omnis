package sessions

import (
	"os"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

// PendingQuestion is a durable AskUserQuestion waiting for an answer, stored in
// the session's conversation file so it survives a server restart.
type PendingQuestion struct {
	Question askuser.Question `json:"question"`
	// TurnID groups the questions of one interrupted turn; the resume turn
	// starts once every question of that turn has an answer.
	TurnID string `json:"turn_id"`
	// Prompt is the user request the interrupted turn was answering (empty for
	// an injected turn). A turn is persisted only when it ends, so this is the
	// only record of it.
	Prompt string `json:"prompt,omitempty"`
	Squad  string `json:"squad,omitempty"`
	// TurnCount is how many turns were persisted when the question was asked,
	// so the resume can tell whether the interrupted turn was saved since.
	TurnCount int       `json:"turn_count"`
	AskedAt   time.Time `json:"asked_at"`
	// Answer is set once a restored question is answered, so a restart
	// between two answers of the same turn loses nothing.
	Answer *askuser.Answer `json:"answer,omitempty"`
}

// mutateExisting applies fn only when the conversation file exists, so a
// removal racing a session delete never recreates the deleted file.
//
// The existence check, the load, the mutation, and the save all happen while
// holding the session's conversation lock in ONE critical section — the same
// lock DeleteConversationFile takes around its os.Remove. That is what makes
// this safe: if the check ran under the lock but the load+save ran after
// releasing it (or via a second, separate lock acquisition through
// mutateConversation), a delete could still land in the gap and this function
// would recreate the file it just confirmed was gone. Do not reintroduce that
// gap — e.g. by splitting this back into an os.Stat followed by a call to
// mutateConversation (which acquires convLock a second time; sync.Mutex is
// not reentrant, so doing that under our own lock would deadlock rather than
// merely race).
func mutateExisting(sessionID string, fn func(*ConversationFile)) error {
	mu := convLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(ConversationPath(sessionID)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if mutateExistingTestHook != nil {
		// Test-only synchronization point (nil in production, so this costs a
		// single nil check). Lets a test pause here — file confirmed to exist,
		// conversation lock still held — to prove a concurrent
		// DeleteConversationFile for the same session blocks on that lock
		// instead of racing in and having its removal undone by the save
		// below. See TestMutateExistingBlocksConcurrentDelete.
		mutateExistingTestHook()
	}
	f, err := loadForWrite(sessionID)
	if err != nil {
		return err
	}
	fn(f)
	return SaveConversationFile(sessionID, f)
}

// AddPendingQuestion stores a durable question, replacing one with the same id.
func AddPendingQuestion(sessionID string, pq PendingQuestion) error {
	return mutateConversation(sessionID, func(f *ConversationFile) {
		f.PendingQuestions = append(dropQuestion(f.PendingQuestions, pq.Question.ID), pq)
	})
}

// RemovePendingQuestion drops one stored question.
func RemovePendingQuestion(sessionID, questionID string) error {
	return mutateExisting(sessionID, func(f *ConversationFile) {
		f.PendingQuestions = dropQuestion(f.PendingQuestions, questionID)
	})
}

// SetPendingAnswer records the answer on a stored question.
func SetPendingAnswer(sessionID, questionID string, ans askuser.Answer) error {
	return mutateExisting(sessionID, func(f *ConversationFile) {
		for i := range f.PendingQuestions {
			if f.PendingQuestions[i].Question.ID == questionID {
				a := ans
				f.PendingQuestions[i].Answer = &a
			}
		}
	})
}

// RemovePendingTurn drops every stored question of one interrupted turn.
func RemovePendingTurn(sessionID, turnID string) error {
	return mutateExisting(sessionID, func(f *ConversationFile) {
		kept := f.PendingQuestions[:0]
		for _, p := range f.PendingQuestions {
			if p.TurnID != turnID {
				kept = append(kept, p)
			}
		}
		f.PendingQuestions = kept
	})
}

// ClearPendingQuestions drops every stored question of a session.
func ClearPendingQuestions(sessionID string) error {
	return mutateExisting(sessionID, func(f *ConversationFile) { f.PendingQuestions = nil })
}

// LoadPendingQuestions returns the stored questions of a session.
func LoadPendingQuestions(sessionID string) ([]PendingQuestion, error) {
	f, err := LoadConversationFile(sessionID)
	if err != nil || f == nil {
		return nil, err
	}
	return f.PendingQuestions, nil
}

// mutateExistingTestHook is nil in production. Tests set it to pause
// mutateExisting mid-critical-section (see the call site above).
var mutateExistingTestHook func()

func dropQuestion(in []PendingQuestion, id string) []PendingQuestion {
	out := in[:0]
	for _, p := range in {
		if p.Question.ID != id {
			out = append(out, p)
		}
	}
	return out
}
