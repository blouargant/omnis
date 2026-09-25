package sessions

import (
	"os"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

func pq(id, turn string) PendingQuestion {
	return PendingQuestion{
		Question: askuser.Question{ID: id, SessionID: "s1", Kind: askuser.KindText, Prompt: "Which env?", Durable: true},
		TurnID:   turn, Prompt: "deploy it", Squad: "system", AskedAt: time.Now(),
	}
}

func TestPendingQuestionsRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := AddPendingQuestion("s1", pq("q1", "t1")); err != nil {
		t.Fatal(err)
	}
	if err := AddPendingQuestion("s1", pq("q2", "t1")); err != nil {
		t.Fatal(err)
	}
	if err := SetPendingAnswer("s1", "q1", askuser.Answer{Text: "prod"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPendingQuestions("s1")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0].Answer == nil || got[0].Answer.Text != "prod" || got[1].Answer != nil {
		t.Fatalf("answer must be recorded on q1 only: %+v", got)
	}
	if err := RemovePendingQuestion("s1", "q2"); err != nil {
		t.Fatal(err)
	}
	if err := RemovePendingTurn("s1", "t1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadPendingQuestions("s1"); len(got) != 0 {
		t.Fatalf("expected none left, got %+v", got)
	}
}

// A session whose first turn was interrupted has a conversation file with no
// turns. It must still be restored, or the GC deletes it with its question.
func TestLoadPersistedSessionsKeepsTurnlessSessionWithPendingQuestion(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := AddPendingQuestion("fresh", pq("q1", "t1")); err != nil {
		t.Fatal(err)
	}
	var found *SessionMeta
	for _, m := range LoadPersistedSessions() {
		if m.ID == "fresh" {
			found = m
		}
	}
	if found == nil {
		t.Fatal("turnless session with a pending question was not loaded")
	}
	if len(found.PendingQuestions) != 1 || found.CreatedAt.IsZero() {
		t.Fatalf("meta must carry the question and a created time: %+v", found)
	}
}

// Removing an entry after the session was deleted must not recreate the file.
func TestRemoveDoesNotRecreateDeletedConversation(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_ = AddPendingQuestion("gone", pq("q1", "t1"))
	DeleteConversationFile("gone")
	for _, fn := range []func() error{
		func() error { return RemovePendingQuestion("gone", "q1") },
		func() error { return SetPendingAnswer("gone", "q1", askuser.Answer{Text: "x"}) },
		func() error { return RemovePendingTurn("gone", "t1") },
		func() error { return ClearPendingQuestions("gone") },
	} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(ConversationPath("gone")); !os.IsNotExist(err) {
			t.Fatal("a deleted conversation file was recreated")
		}
	}
}
