package sessions

import (
	"os"
	"sync"
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

// TestMutateExistingBlocksConcurrentDelete proves the fix for the race where
// mutateExisting's existence check and DeleteConversationFile's os.Remove
// could interleave: a delete landing between the check and the write used to
// recreate the file mutateExisting had just confirmed was gone (both used to
// take no lock, or took separate ones, around that gap).
//
// It pauses RemovePendingQuestion right after it confirms the file exists —
// while still holding the session's conversation lock — via
// mutateExistingTestHook, then asserts a concurrent DeleteConversationFile
// call for the same session does NOT complete while that pause is in effect.
// If it did, that would mean DeleteConversationFile ran without waiting on
// the lock RemovePendingQuestion holds, i.e. the fix's mutual exclusion is
// gone and the race is back.
func TestMutateExistingBlocksConcurrentDelete(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := AddPendingQuestion("held", pq("q1", "t1")); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	proceed := make(chan struct{})
	mutateExistingTestHook = func() {
		close(entered)
		<-proceed
	}
	defer func() { mutateExistingTestHook = nil }()

	removeDone := make(chan error, 1)
	go func() { removeDone <- RemovePendingQuestion("held", "q1") }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("RemovePendingQuestion never reached the test hook")
	}
	// RemovePendingQuestion has confirmed the file exists and is now parked in
	// the hook, still holding the session's conversation lock.

	deleteDone := make(chan struct{})
	go func() {
		DeleteConversationFile("held")
		close(deleteDone)
	}()

	select {
	case <-deleteDone:
		t.Fatal("DeleteConversationFile completed while a pending-question mutation still held the conversation lock — the fix's mutual exclusion is broken")
	case <-time.After(100 * time.Millisecond):
		// Expected: the delete is blocked waiting on the lock.
	}

	close(proceed) // let RemovePendingQuestion finish its load+mutate+save
	if err := <-removeDone; err != nil {
		t.Fatal(err)
	}
	<-deleteDone // now the delete can proceed and finish

	if _, err := os.Stat(ConversationPath("held")); !os.IsNotExist(err) {
		t.Fatal("conversation file should not exist once the delete has completed")
	}
}

// TestDeleteDuringPendingMutationNeverRecreatesFile stress-tests the same
// invariant across many random goroutine interleavings (run with -race): once
// DeleteConversationFile has actually removed a session's file, no concurrent
// RemovePendingQuestion/SetPendingAnswer call may recreate it — regardless of
// scheduling order, since every one of them is serialised on the same
// per-session lock and re-checks existence under it before writing anything.
func TestDeleteDuringPendingMutationNeverRecreatesFile(t *testing.T) {
	for i := 0; i < 200; i++ {
		t.Setenv("OMNIS_HOME", t.TempDir())
		const id = "racer"
		if err := AddPendingQuestion(id, pq("q1", "t1")); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); DeleteConversationFile(id) }()
		go func() { defer wg.Done(); _ = RemovePendingQuestion(id, "q1") }()
		go func() { defer wg.Done(); _ = SetPendingAnswer(id, "q1", askuser.Answer{Text: "x"}) }()
		wg.Wait()

		if _, err := os.Stat(ConversationPath(id)); err == nil {
			t.Fatalf("iteration %d: conversation file exists after a concurrent delete", i)
		} else if !os.IsNotExist(err) {
			t.Fatalf("iteration %d: unexpected stat error: %v", i, err)
		}
	}
}
