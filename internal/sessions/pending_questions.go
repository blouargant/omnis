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
func mutateExisting(sessionID string, fn func(*ConversationFile)) error {
	if _, err := os.Stat(ConversationPath(sessionID)); os.IsNotExist(err) {
		return nil
	}
	return mutateConversation(sessionID, fn)
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

func dropQuestion(in []PendingQuestion, id string) []PendingQuestion {
	out := in[:0]
	for _, p := range in {
		if p.Question.ID != id {
			out = append(out, p)
		}
	}
	return out
}
