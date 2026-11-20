package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/lessons"
)

// drainEpisodes returns the episodes waiting in the queue.
func drainEpisodes(s *lessonService) []lessonEpisode {
	var out []lessonEpisode
	for {
		select {
		case ep := <-s.queue:
			out = append(out, ep)
		default:
			return out
		}
	}
}

// callOnce runs one call through the before- and after-tool callbacks.
func callOnce(s *lessonService, c lessonCtx, name string, args, result map[string]any) map[string]any {
	s.BeforeTool("coder")(c, namedTool{name: name}, args)
	out, _ := s.AfterTool("coder")(c, namedTool{name: name}, args, result, nil)
	return out
}

// A refused call is neither a failure nor a success: failure → refusal → real
// success yields one episode whose working arguments are the real success's.
func TestRefusalDoesNotCloseAnEpisode(t *testing.T) {
	for kind, refusal := range refusalResults() {
		t.Run(kind, func(t *testing.T) {
			lessonEnv(t)
			s := newLessonService(nil)
			stopLessonWorker(s)
			run := newLessonCtx("s1", "inv1")
			denied := map[string]any{"command": "kubectl delete ns prod"}
			okArgs := map[string]any{"command": "kubectl get pods -A"}

			callOnce(s, run.withCall("1"), "Bash", lessonTestArgs, failResult())
			time.Sleep(time.Millisecond)
			if out := callOnce(s, run.withCall("2"), "Bash", denied, refusal); out != nil {
				t.Fatalf("a refusal got a result change: %v", out)
			}
			if eps := drainEpisodes(s); len(eps) != 0 {
				t.Fatalf("the refusal closed the episode: %+v", eps)
			}
			callOnce(s, run.withCall("3"), "Bash", okArgs, okResult())
			eps := drainEpisodes(s)
			if len(eps) != 1 {
				t.Fatalf("want one episode, got %d", len(eps))
			}
			if eps[0].WorkedArgs != capJSON(okArgs) {
				t.Fatalf("WorkedArgs = %s, want the real success's", eps[0].WorkedArgs)
			}
		})
	}
}

// A refusal opens no episode either.
func TestRefusalOpensNoEpisode(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s)
	run := newLessonCtx("s1", "inv1")
	callOnce(s, run.withCall("1"), "Bash", lessonTestArgs, refusalResults()["gate deny"])
	callOnce(s, run.withCall("2"), "Bash", lessonTestArgs, okResult())
	if eps := drainEpisodes(s); len(eps) != 0 {
		t.Fatalf("a refusal opened an episode: %+v", eps)
	}
}

// A pending hint followed by a refusal stays pending; the next real call
// settles it. A refusal does not count as an occurrence either.
func TestRefusalLeavesAPendingHintPending(t *testing.T) {
	for kind, refusal := range refusalResults() {
		t.Run(kind, func(t *testing.T) {
			home := lessonEnv(t)
			seedLesson(t, home, "coder", lessons.StateHint)
			s := newLessonService(nil)
			run := newLessonCtx("s1", "inv1")
			okArgs := map[string]any{"command": "kubectl get pods -A"}

			callOnce(s, run.withCall("1"), "Bash", lessonTestArgs, failResult()) // hint
			seen := findLesson(t, home, "coder").Seen
			time.Sleep(time.Millisecond)
			callOnce(s, run.withCall("2"), "Bash", okArgs, refusal)
			l := findLesson(t, home, "coder")
			if l.Recovered != 0 || l.Repeated != 0 || l.Seen != seen {
				t.Fatalf("the refusal was counted: %+v", l)
			}
			callOnce(s, run.withCall("3"), "Bash", okArgs, okResult())
			if l := findLesson(t, home, "coder"); l.Recovered != 1 {
				t.Fatalf("the next real call did not settle the hint: %+v", l)
			}
		})
	}
}

// bashCall runs one Bash call with the exit status the tool would report.
func bashCall(s *lessonService, c lessonCtx, cmd, out string, code int) {
	args := map[string]any{"command": cmd}
	s.BeforeTool("coder")(c, namedTool{name: "Bash"}, args)
	s.observeBashExit(c, code, false)
	s.AfterTool("coder")(c, namedTool{name: "Bash"}, args, map[string]any{"output": out}, nil)
}

func TestBashExitStatusOpensAndClosesEpisodes(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s)
	run := newLessonCtx("s1", "inv1")

	bashCall(s, run.withCall("1"), "git status", "fatal: not a git repository (or any of the parent directories): .git", 128)
	time.Sleep(time.Millisecond)
	bashCall(s, run.withCall("2"), "git -C /repo status", "On branch main", 0)
	eps := drainEpisodes(s)
	if len(eps) != 1 || eps[0].ToolKey != "Bash:git" {
		t.Fatalf("want one Bash:git episode, got %+v", eps)
	}
	if eps[0].ErrText == "" || eps[0].WorkedArgs != capJSON(map[string]any{"command": "git -C /repo status"}) {
		t.Fatalf("episode %+v", eps[0])
	}
}

func TestBashNoMatchIsNotAFailure(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s)
	run := newLessonCtx("s1", "inv1")
	bashCall(s, run.withCall("1"), "grep nomatch file.txt", "(no output)", 1)
	time.Sleep(time.Millisecond)
	bashCall(s, run.withCall("2"), "grep match file.txt", "match", 0)
	if eps := drainEpisodes(s); len(eps) != 0 {
		t.Fatalf("grep's no-match opened an episode: %+v", eps)
	}
	// A bad regex (exit 2) is a failure.
	bashCall(s, run.withCall("3"), "grep '[' file.txt", "grep: Unmatched [, [^, [:, [., or [=", 2)
	time.Sleep(time.Millisecond)
	bashCall(s, run.withCall("4"), "grep -F '[' file.txt", "a [ b", 0)
	if eps := drainEpisodes(s); len(eps) != 1 {
		t.Fatalf("grep's bad regex: want one episode, got %d", len(eps))
	}
}

// A Bash failure followed by a different Bash failure of the same first word
// closes nothing: the second is not a success.
func TestBashFailureThenOtherFailureClosesNothing(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s)
	run := newLessonCtx("s1", "inv1")
	bashCall(s, run.withCall("1"), "kubectl get pod x", "Error from server (NotFound): pods \"x\" not found", 1)
	time.Sleep(time.Millisecond)
	bashCall(s, run.withCall("2"), "kubectl apply -f y.yaml", "error: unable to recognize \"y.yaml\": no matches for kind", 1)
	if eps := drainEpisodes(s); len(eps) != 0 {
		t.Fatalf("a failed retry closed the episode: %+v", eps)
	}
}

// The observer records nothing for a call without a recorded start (an agent
// without lessons), so nothing accumulates.
func TestBashExitWithoutStartIsIgnored(t *testing.T) {
	s := newLessonService(nil)
	s.observeBashExit(newLessonCtx("s1", "inv1").withCall("c"), 2, false)
	if n := len(s.starts); n != 0 {
		t.Fatalf("%d entries recorded", n)
	}
}

// The error text kept for an episode is capped like the arguments.
func TestEpisodeErrorTextIsCapped(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s)
	run := newLessonCtx("s1", "inv1")
	long := "Error: " + strings.Repeat("x", 5000)
	callOnce(s, run.withCall("1"), "Edit", map[string]any{"file_path": "a"}, map[string]any{"result": long})
	time.Sleep(time.Millisecond)
	callOnce(s, run.withCall("2"), "Edit", map[string]any{"file_path": "a"}, map[string]any{"result": "ok"})
	eps := drainEpisodes(s)
	if len(eps) != 1 || len([]rune(eps[0].ErrText)) != lessonArgsCap {
		t.Fatalf("error text not capped: %d episodes", len(eps))
	}
}

// Cookie mode: a per-user root resolver that cannot name the owner ⇒ lessons do
// nothing for that call (no read, no write, no hint). Without a resolver the
// shared root is used.
func TestUnresolvedOwnerSkipsLessons(t *testing.T) {
	home := lessonEnv(t)
	seeded := seedLesson(t, home, "coder", lessons.StateHint) // in the shared root
	s := newLessonService(nil)
	stopLessonWorker(s)
	run := newLessonCtx("s1", "inv1")

	SetCollectionRootResolver(func(string) string { return "" })
	if out := callOnce(s, run.withCall("1"), "Bash", lessonTestArgs, failResult()); out != nil {
		t.Fatalf("hint delivered from the shared root for an unresolved owner: %v", out)
	}
	time.Sleep(time.Millisecond)
	callOnce(s, run.withCall("2"), "Bash", map[string]any{"command": "kubectl get pods -A"}, okResult())
	if eps := drainEpisodes(s); len(eps) != 0 {
		t.Fatalf("episode recorded for an unresolved owner: %+v", eps)
	}
	if l := findLesson(t, home, "coder"); l.Seen != seeded.Seen || l.Hinted != seeded.Hinted {
		t.Fatalf("shared lesson counted: %+v", l)
	}
	if _, ok := s.rootFor("s1"); ok {
		t.Fatal("rootFor resolved an unresolved owner")
	}

	SetCollectionRootResolver(nil)
	if root, ok := s.rootFor("s1"); !ok || root != home {
		t.Fatalf("no resolver: root %q ok %v, want the shared root", root, ok)
	}
	if out := callOnce(s, run.withCall("3"), "Bash", lessonTestArgs, failResult()); out == nil {
		t.Fatal("no hint from the shared root without a resolver")
	}
}
