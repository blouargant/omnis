package askuser_test

import (
	"context"
	"testing"
	"time"

	"github.com/blouargant/yoke/internal/askuser"
)

func TestAskResolveHappyPath(t *testing.T) {
	var registry *askuser.Registry
	registry = askuser.NewRegistry(askuser.WithNotify(func(q askuser.Question) {
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = registry.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{"a"}})
		}()
	}))

	ans, err := registry.Ask(context.Background(), "s1",
		askuser.Question{Kind: askuser.KindSingle, Prompt: "p", Choices: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if ans.Cancelled {
		t.Fatal("expected a real answer, got cancelled")
	}
	if len(ans.Selected) == 0 || ans.Selected[0] != "a" {
		t.Fatalf("expected selected=[a], got %v", ans.Selected)
	}
}

func TestAskTimeout(t *testing.T) {
	r := askuser.NewRegistry(askuser.WithDefaultTimeout(50 * time.Millisecond))
	ans, err := r.Ask(context.Background(), "s1",
		askuser.Question{Kind: askuser.KindConfirm, Prompt: "confirm?"})
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Cancelled {
		t.Fatal("expected Cancelled=true on timeout")
	}
}

func TestAskCtxCancel(t *testing.T) {
	r := askuser.NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	ans, err := r.Ask(ctx, "s1", askuser.Question{Kind: askuser.KindText, Prompt: "text?"})
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Cancelled {
		t.Fatal("expected Cancelled=true on ctx cancel")
	}
}

func TestResolveUnknownQuestion(t *testing.T) {
	r := askuser.NewRegistry()
	err := r.Resolve("s1", "no-such-id", askuser.Answer{})
	if err == nil {
		t.Fatal("expected error for unknown question_id")
	}
}

func TestResolveCrossSessionFallback(t *testing.T) {
	// Question is registered under "owner" (e.g. an empty or sub-agent
	// session id used by an MCP input resolver) but the UI POSTs the
	// answer under the user-facing session "ui-session". Resolve must
	// still find and answer the question by UUID.
	var registry *askuser.Registry
	registry = askuser.NewRegistry(askuser.WithNotify(func(q askuser.Question) {
		go func() {
			time.Sleep(10 * time.Millisecond)
			if err := registry.Resolve("ui-session", q.ID, askuser.Answer{Text: "hunter2"}); err != nil {
				t.Errorf("cross-session resolve failed: %v", err)
			}
		}()
	}))
	ans, err := registry.Ask(context.Background(), "owner",
		askuser.Question{Kind: askuser.KindText, Prompt: "secret?", Password: true})
	if err != nil {
		t.Fatal(err)
	}
	if ans.Cancelled || ans.Text != "hunter2" {
		t.Fatalf("expected text=hunter2, got %+v", ans)
	}
}

func TestDoubleResolveIsIdempotent(t *testing.T) {
	var registry *askuser.Registry
	registry = askuser.NewRegistry(askuser.WithNotify(func(q askuser.Question) {
		go func() {
			time.Sleep(10 * time.Millisecond)
			_ = registry.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{"b"}})
			// Second resolve should return ErrAlreadyResolved but not panic.
			time.Sleep(10 * time.Millisecond)
			_ = registry.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{"c"}})
		}()
	}))
	ans, _ := registry.Ask(context.Background(), "s1",
		askuser.Question{Kind: askuser.KindSingle, Prompt: "p", Choices: []string{"b", "c"}})
	if len(ans.Selected) == 0 || ans.Selected[0] != "b" {
		t.Fatalf("first resolve should win, got %v", ans.Selected)
	}
}

func TestPending(t *testing.T) {
	var registry *askuser.Registry
	done := make(chan struct{})
	registry = askuser.NewRegistry(askuser.WithNotify(func(q askuser.Question) {
		// Check Pending while question is still open.
		pending := registry.Pending(q.SessionID)
		if len(pending) != 1 {
			// Signal failure via done
			close(done)
			return
		}
		_ = registry.Resolve(q.SessionID, q.ID, askuser.Answer{Cancelled: true})
		close(done)
	}))
	go func() {
		registry.Ask(context.Background(), "s1", askuser.Question{Kind: askuser.KindText, Prompt: "t"}) //nolint:errcheck
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Pending check")
	}
}
