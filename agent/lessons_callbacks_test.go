package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/lessons"
)

// lessonCtx is a fake tool context carrying a session, an invocation and a
// real context.Context (memoryReadOnly and the steer session read from it).
type lessonCtx struct {
	adk.ToolContext
	ctx            context.Context
	sid, inv, call string
}

func (c lessonCtx) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c lessonCtx) Done() <-chan struct{}       { return c.ctx.Done() }
func (c lessonCtx) Err() error                  { return c.ctx.Err() }
func (c lessonCtx) Value(k any) any             { return c.ctx.Value(k) }
func (c lessonCtx) SessionID() string           { return c.sid }
func (c lessonCtx) InvocationID() string        { return c.inv }
func (c lessonCtx) FunctionCallID() string      { return c.call }
func (c lessonCtx) AgentName() string           { return "" }
func (c lessonCtx) UserID() string              { return "" }

// withCall returns the context of one function call of the same run.
func (c lessonCtx) withCall(id string) lessonCtx { c.call = id; return c }

func newLessonCtx(sid, inv string) lessonCtx {
	return lessonCtx{ctx: context.Background(), sid: sid, inv: inv}
}

const lessonTestErr = "Error: kubectl: unknown flag --namespace-all"

var lessonTestArgs = map[string]any{"command": "kubectl get pods --namespace-all"}

func lessonTestSig() string {
	return lessons.Signature(lessons.ToolKey("Bash", lessonTestArgs), lessonTestErr)
}

// lessonEnv isolates the store root and the process-wide resolvers.
func lessonEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_LESSONS", "")
	SetHiddenSessionResolver(nil)
	SetCollectionRootResolver(nil)
	t.Cleanup(func() {
		SetHiddenSessionResolver(nil)
		SetCollectionRootResolver(nil)
	})
	return home
}

func seedLesson(t *testing.T, root, agent string, state lessons.State) lessons.Lesson {
	t.Helper()
	l := lessons.Lesson{
		Agent: agent, Tool: "Bash", Signature: lessonTestSig(),
		Cue: "when listing pods in every namespace", Text: "use -A, kubectl has no --namespace-all",
		Worked: `{"command":"kubectl get pods -A"}`, State: state,
	}
	if err := lessons.In(root).Save(l); err != nil {
		t.Fatal(err)
	}
	got, ok := lessons.In(root).Find(agent, l.Signature)
	if !ok {
		t.Fatal("seeded lesson not found")
	}
	return got
}

func failResult() map[string]any { return map[string]any{"output": lessonTestErr, "exit_code": 1} }
func okResult() map[string]any   { return map[string]any{"output": "NAME READY"} }

func findLesson(t *testing.T, root, agent string) lessons.Lesson {
	t.Helper()
	l, ok := lessons.In(root).Find(agent, lessonTestSig())
	if !ok {
		t.Fatal("lesson vanished")
	}
	return l
}

func TestHintIsAppendedOnAKnownError(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateHint)
	cb := newLessonService(nil).AfterTool("coder")
	in := failResult()
	out, err := cb(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, in, nil)
	if err != nil || out == nil {
		t.Fatalf("want a hinted result, got %v, %v", out, err)
	}
	want := "Learned from an earlier error with this tool: use -A, kubectl has no --namespace-all\nArguments that worked then: {\"command\":\"kubectl get pods -A\"}"
	if out[lessonHintKey] != want {
		t.Fatalf("hint = %q", out[lessonHintKey])
	}
	if out["output"] != lessonTestErr || out["exit_code"] != 1 {
		t.Fatalf("other keys changed: %v", out)
	}
	if _, touched := in[lessonHintKey]; touched {
		t.Fatal("the original result map was mutated")
	}
	if l := findLesson(t, home, "coder"); l.Hinted != 1 {
		t.Fatalf("Hinted = %d, want 1", l.Hinted)
	}
}

func TestHintKeepsAToolErrorAsAnErrorKey(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateHint)
	out, _ := newLessonService(nil).AfterTool("coder")(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"},
		lessonTestArgs, nil, errors.New(lessonTestErr))
	if out == nil || out["error"] != lessonTestErr || out[lessonHintKey] == nil {
		t.Fatalf("got %v", out)
	}
}

func TestNoHintForARetiredLesson(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateRetired)
	out, err := newLessonService(nil).AfterTool("coder")(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if out != nil || err != nil {
		t.Fatalf("retired lesson was delivered: %v %v", out, err)
	}
}

func TestNoHintForAnotherAgent(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateHint)
	out, err := newLessonService(nil).AfterTool("reviewer")(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if out != nil || err != nil {
		t.Fatalf("another agent's lesson was delivered: %v %v", out, err)
	}
}

func TestUnknownErrorReturnsNil(t *testing.T) {
	lessonEnv(t)
	cb := newLessonService(nil).AfterTool("coder")
	out, err := cb(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if out != nil || err != nil {
		t.Fatalf("got %v %v", out, err)
	}
	out, err = cb(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, okResult(), nil)
	if out != nil || err != nil {
		t.Fatalf("success: got %v %v", out, err)
	}
}

func TestOutcomeIsRecorded(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateHint)
	cb := newLessonService(nil).AfterTool("coder")
	bash := namedTool{name: "Bash"}

	c := newLessonCtx("s1", "inv1")
	cb(c, bash, lessonTestArgs, failResult(), nil)
	cb(c, bash, map[string]any{"command": "kubectl get pods -A"}, okResult(), nil)
	if l := findLesson(t, home, "coder"); l.Recovered != 1 || l.Repeated != 0 {
		t.Fatalf("after success: Recovered %d Repeated %d", l.Recovered, l.Repeated)
	}

	c2 := newLessonCtx("s1", "inv2")
	cb(c2, bash, lessonTestArgs, failResult(), nil)
	cb(c2, bash, lessonTestArgs, failResult(), nil)
	l := findLesson(t, home, "coder")
	if l.Recovered != 1 || l.Repeated != 1 {
		t.Fatalf("after a repeat: Recovered %d Repeated %d", l.Recovered, l.Repeated)
	}
	if l.Hinted != 3 {
		t.Fatalf("Hinted = %d, want 3", l.Hinted)
	}
}

func TestSuccessAfterFailureQueuesAnEpisode(t *testing.T) {
	home := lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s) // the test inspects the queue itself
	cb := s.AfterTool("coder")
	bash := namedTool{name: "Bash"}
	c := newLessonCtx("s1", "inv1")
	cb(c, bash, lessonTestArgs, failResult(), nil)
	cb(c, bash, map[string]any{"command": "kubectl get pods -A"}, okResult(), nil)
	select {
	case ep := <-s.queue:
		if ep.Root != home || ep.Agent != "coder" || ep.SessionID != "s1" || ep.Signature != lessonTestSig() {
			t.Fatalf("episode = %+v", ep)
		}
	default:
		t.Fatal("no episode queued")
	}
	if len(s.queue) != 0 {
		t.Fatalf("%d extra episodes", len(s.queue))
	}
}

func TestNothingIsRecordedInAHiddenOrReadOnlyTurn(t *testing.T) {
	cases := map[string]func(t *testing.T) lessonCtx{
		"hidden": func(t *testing.T) lessonCtx {
			SetHiddenSessionResolver(func(string) bool { return true })
			return newLessonCtx("s1", "inv1")
		},
		"read-only": func(t *testing.T) lessonCtx {
			c := newLessonCtx("s1", "inv1")
			c.ctx = WithMemoryReadOnly(context.Background())
			return c
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			home := lessonEnv(t)
			seedLesson(t, home, "coder", lessons.StateHint)
			s := newLessonService(nil)
			cb := s.AfterTool("coder")
			c := mk(t)
			out, _ := cb(c, namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
			if out == nil || out[lessonHintKey] == nil {
				t.Fatal("the hint must still be delivered")
			}
			cb(c, namedTool{name: "Bash"}, map[string]any{"command": "kubectl get pods -A"}, okResult(), nil)
			if len(s.queue) != 0 {
				t.Fatal("an episode was queued")
			}
			if l := findLesson(t, home, "coder"); l.Hinted != 0 || l.Recovered != 0 || l.Repeated != 0 {
				t.Fatalf("counters moved: %+v", l)
			}
		})
	}
}

func TestDisabledIsInert(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateHint)
	s := newLessonService(func() bool { return false })
	out, err := s.AfterTool("coder")(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if out != nil || err != nil {
		t.Fatalf("disabled service delivered %v %v", out, err)
	}
	var nilSvc *lessonService
	if nilSvc.AfterTool("coder") != nil || nilSvc.BeforeModel("coder") != nil {
		t.Fatal("a nil service must give nil callbacks")
	}
	on := RuntimeSettings{LessonsEnabled: true}
	if lessonsActive(on, RuntimeAgentConfig{Name: "coder", Lessons: false}) {
		t.Fatal("an agent with lessons:false got callbacks")
	}
	if lessonsActive(RuntimeSettings{}, RuntimeAgentConfig{Name: "coder", Lessons: true}) {
		t.Fatal("a generation with lessons off gave callbacks")
	}
	if !lessonsActive(on, RuntimeAgentConfig{Name: "coder", Lessons: true}) {
		t.Fatal("lessons on, agent on: want callbacks")
	}
}

func TestUnwritableStoreLeavesTheResultAlone(t *testing.T) {
	home := lessonEnv(t)
	file := filepath.Join(home, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	SetCollectionRootResolver(func(string) string { return file })
	cb := newLessonService(nil).AfterTool("coder")
	c := newLessonCtx("s1", "inv1")
	for _, r := range []map[string]any{failResult(), okResult()} {
		out, err := cb(c, namedTool{name: "Bash"}, lessonTestArgs, r, nil)
		if out != nil || err != nil {
			t.Fatalf("got %v %v", out, err)
		}
	}
}

func TestPanicInLessonCodeLeavesTheResultAlone(t *testing.T) {
	lessonEnv(t)
	SetHiddenSessionResolver(func(string) bool { panic("boom") })
	out, err := newLessonService(nil).AfterTool("coder")(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if out != nil || err != nil {
		t.Fatalf("got %v %v", out, err)
	}
}

func TestQueueFullDropsTheEpisode(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s) // the test inspects the queue itself
	for i := 0; i < lessonQueueCap; i++ {
		s.queue <- lessonEpisode{}
	}
	cb := s.AfterTool("coder")
	done := make(chan struct{})
	go func() {
		c := newLessonCtx("s1", "inv1")
		cb(c, namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
		cb(c, namedTool{name: "Bash"}, map[string]any{"command": "kubectl get pods -A"}, okResult(), nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("a full queue blocked the callback")
	}
	if len(s.queue) != lessonQueueCap {
		t.Fatalf("queue length %d", len(s.queue))
	}
}

func TestLessonsSwitchPrecedence(t *testing.T) {
	cases := []struct {
		name, file, env string
		want            bool
	}{
		{"default on", `{"agents":["leader"]}`, "", true},
		{"agents.json off", `{"agents":["leader"],"lessons":{"enabled":false}}`, "", false},
		{"env on over file", `{"agents":["leader"],"lessons":{"enabled":false}}`, "true", true},
		{"env off", `{"agents":["leader"]}`, "false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layer := t.TempDir()
			t.Setenv("OMNIS_HOME", t.TempDir())
			t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
			t.Setenv("OMNIS_CONFIG_DIRS", layer)
			t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
			t.Setenv("OMNIS_LESSONS", tc.env)
			writeAgentDef(t, layer, "leader", `{"name":"leader","enabled":true,"leader":true,"tools":[]}`)
			if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
			rs, err := ResolveRuntimeSettings(Options{})
			if err != nil {
				t.Fatal(err)
			}
			if rs.LessonsEnabled != tc.want {
				t.Fatalf("LessonsEnabled = %v, want %v", rs.LessonsEnabled, tc.want)
			}
		})
	}
}

func TestAgentLessonsFlagIsOptOut(t *testing.T) {
	layer := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	writeAgentDef(t, layer, "leader", `{"name":"leader","enabled":true,"leader":true,"tools":[]}`)
	writeAgentDef(t, layer, "quiet", `{"name":"quiet","enabled":true,"tools":[],"lessons":false}`)
	if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(`{"agents":["leader","quiet"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"leader": true, "quiet": false} {
		cfg, ok := rs.AgentConfig(name)
		if !ok || cfg.Lessons != want {
			t.Fatalf("%s: Lessons = %v (found %v), want %v", name, cfg.Lessons, ok, want)
		}
	}
}

// A success that STARTED before a sibling's failure completed ran without
// knowing the error: it must not close the episode. A call that starts after
// the failure completed does.
func TestParallelSiblingSuccessDoesNotQueueAnEpisode(t *testing.T) {
	lessonEnv(t)
	s := newLessonService(nil)
	stopLessonWorker(s) // the test inspects the queue itself
	before, after := s.BeforeTool("coder"), s.AfterTool("coder")
	bash := namedTool{name: "Bash"}
	run := newLessonCtx("s1", "inv1")
	okArgs := map[string]any{"command": "kubectl get pods -A"}

	slow, fast := run.withCall("ok-slow"), run.withCall("fail-fast")
	before(slow, bash, okArgs)
	before(fast, bash, lessonTestArgs)
	time.Sleep(time.Millisecond)
	after(fast, bash, lessonTestArgs, failResult(), nil)
	after(slow, bash, okArgs, okResult(), nil)
	if len(s.queue) != 0 {
		t.Fatalf("a parallel sibling closed the episode: %+v", <-s.queue)
	}

	later := run.withCall("ok-later")
	before(later, bash, okArgs)
	after(later, bash, okArgs, okResult(), nil)
	if len(s.queue) != 1 {
		t.Fatalf("want one episode from the later success, got %d", len(s.queue))
	}
}

// A sibling that started before the hint was delivered does not settle it.
func TestSiblingStartedBeforeHintDoesNotSettleIt(t *testing.T) {
	home := lessonEnv(t)
	seedLesson(t, home, "coder", lessons.StateHint)
	s := newLessonService(nil)
	before, after := s.BeforeTool("coder"), s.AfterTool("coder")
	bash := namedTool{name: "Bash"}
	run := newLessonCtx("s1", "inv1")
	okArgs := map[string]any{"command": "kubectl get pods -A"}

	sib, fail := run.withCall("sib"), run.withCall("fail")
	before(sib, bash, okArgs)
	before(fail, bash, lessonTestArgs)
	time.Sleep(time.Millisecond)
	after(fail, bash, lessonTestArgs, failResult(), nil) // hint delivered
	after(sib, bash, okArgs, okResult(), nil)
	if l := findLesson(t, home, "coder"); l.Recovered != 0 || l.Repeated != 0 {
		t.Fatalf("the sibling settled the hint: %+v", l)
	}
	next := run.withCall("next")
	before(next, bash, okArgs)
	after(next, bash, okArgs, okResult(), nil)
	if l := findLesson(t, home, "coder"); l.Recovered != 1 {
		t.Fatalf("the next call did not settle the hint: Recovered %d", l.Recovered)
	}
}

func TestStartTimesAreBounded(t *testing.T) {
	s := newLessonService(nil)
	base := time.Now()
	for i := 0; i < lessonStartsMax+10; i++ {
		s.recordStart(callKey{"s", "inv", strconv.Itoa(i)}, base.Add(time.Duration(i)*time.Millisecond))
	}
	if n := len(s.starts); n > lessonStartsMax {
		t.Fatalf("%d start times kept, cap %d", n, lessonStartsMax)
	}
	if _, kept := s.starts[callKey{"s", "inv", "0"}]; kept {
		t.Fatal("the oldest start time was kept over the cap")
	}
	// Entries older than the TTL go first.
	s.recordStart(callKey{"s", "inv", "late"}, base.Add(lessonStartTTL+time.Hour))
	if n := len(s.starts); n != 1 {
		t.Fatalf("stale start times not dropped: %d left", n)
	}
	// A call never recorded falls back to the given time.
	fb := time.Unix(42, 0)
	if got, exit := s.takeStart(callKey{"x", "y", "z"}, fb); !got.Equal(fb) || exit != nil {
		t.Fatalf("fallback = %v", got)
	}
	// BeforeTool never short-circuits.
	out, err := s.BeforeTool("coder")(newLessonCtx("s", "inv").withCall("c"), namedTool{name: "Bash"}, nil)
	if out != nil || err != nil {
		t.Fatalf("BeforeTool returned %v %v", out, err)
	}
}

// stopLessonWorker stops the writer worker so a test can read the queue.
func stopLessonWorker(s *lessonService) {
	s.cancel()
	<-s.done
}
