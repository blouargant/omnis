package agent

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/lessons"
)

// lessonCBCtx is a fake callback context carrying a session id.
type lessonCBCtx struct {
	adk.CallbackContext
	ctx context.Context
	sid string
}

func (c lessonCBCtx) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c lessonCBCtx) Done() <-chan struct{}       { return c.ctx.Done() }
func (c lessonCBCtx) Err() error                  { return c.ctx.Err() }
func (c lessonCBCtx) Value(k any) any             { return c.ctx.Value(k) }
func (c lessonCBCtx) SessionID() string           { return c.sid }

func lessonRequest(system string) *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: system}}},
		},
	}
}

func systemText(req *model.LLMRequest) string {
	var b strings.Builder
	for _, p := range req.Config.SystemInstruction.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func runBeforeModel(t *testing.T, s *lessonService, agent string, req *model.LLMRequest) {
	t.Helper()
	cb := s.BeforeModel(agent)
	if cb == nil {
		t.Fatal("no before-model callback")
	}
	if resp, err := cb(lessonCBCtx{ctx: context.Background(), sid: "s1"}, req); resp != nil || err != nil {
		t.Fatalf("callback answered %v %v", resp, err)
	}
}

func TestPromptBlockIsAppendedToTheSystemInstruction(t *testing.T) {
	home := lessonEnv(t)
	s := newLessonService(func() bool { return true })
	seedLesson(t, home, "coder", lessons.StatePrompt)
	want := lessons.In(home).PromptBlock("coder", time.Now())
	if want == "" {
		t.Fatal("seeded prompt lesson gave no block")
	}
	req := lessonRequest("You are coder.")
	runBeforeModel(t, s, "coder", req)
	got := systemText(req)
	if !strings.HasPrefix(got, "You are coder.\n\n") || !strings.HasSuffix(got, want) {
		t.Fatalf("block not appended at the end after a blank line:\n%s", got)
	}
	// The same request seen twice never doubles the block.
	runBeforeModel(t, s, "coder", req)
	if systemText(req) != got {
		t.Fatal("block added twice")
	}
	// No lesson for another agent ⇒ untouched.
	other := lessonRequest("You are other.")
	runBeforeModel(t, s, "other", other)
	if systemText(other) != "You are other." || len(other.Config.SystemInstruction.Parts) != 1 {
		t.Fatal("agent without lesson was touched")
	}
	// Disabled at call time ⇒ untouched.
	off := newLessonService(func() bool { return false })
	req2 := lessonRequest("You are coder.")
	runBeforeModel(t, off, "coder", req2)
	if systemText(req2) != "You are coder." {
		t.Fatal("disabled service touched the request")
	}
}

func TestPromptBlockIsNotInUserContents(t *testing.T) {
	home := lessonEnv(t)
	s := newLessonService(func() bool { return true })
	seedLesson(t, home, "coder", lessons.StatePrompt)
	req := lessonRequest("sys")
	runBeforeModel(t, s, "coder", req)
	if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 1 || req.Contents[0].Parts[0].Text != "hello" {
		t.Fatal("user contents changed")
	}
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if strings.Contains(p.Text, lessonsBlockTag) {
				t.Fatal("block in a user content")
			}
		}
	}
}

func TestRouterGetsNoLessonBlock(t *testing.T) {
	// The gate is lessonsActive at build time; a router root never calls it.
	// Here: an agent whose lessons are off gets no callbacks at all.
	if lessonsActive(RuntimeSettings{LessonsEnabled: true}, RuntimeAgentConfig{Name: "omnis", Lessons: false}) {
		t.Fatal("router-style agent with lessons off got callbacks")
	}
	var nilSvc *lessonService
	if nilSvc.BeforeModel("omnis") != nil {
		t.Fatal("nil service gave a callback")
	}
}

func TestSeenIsCountedOnEveryFailure(t *testing.T) {
	home := lessonEnv(t)
	s := newLessonService(func() bool { return true })
	seedLesson(t, home, "coder", lessons.StateHint)
	after := s.AfterTool("coder")
	for i, sid := range []string{"s1", "s1", "s2"} {
		ctx := newLessonCtx(sid, "inv"+string(rune('a'+i)))
		after(ctx, namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	}
	l := findLesson(t, home, "coder")
	if l.State != lessons.StatePrompt {
		t.Fatalf("state %s after 3 failures over 2 sessions, want prompt", l.State)
	}
}

func TestStateChangesAreLogged(t *testing.T) {
	home := lessonEnv(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s := newLessonService(func() bool { return true })
	l := seedLesson(t, home, "coder", lessons.StateHint)
	after := s.AfterTool("coder")

	// Promotion.
	for i, sid := range []string{"s1", "s1", "s2"} {
		after(newLessonCtx(sid, "p"+string(rune('a'+i))), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	}
	if want := "lessons: coder " + l.ID + " hint -> prompt"; strings.Count(buf.String(), want) != 1 {
		t.Fatalf("promotion line missing or repeated in %q", buf.String())
	}

	// Demotion.
	buf.Reset()
	dl := seedLesson(t, home, "coder", lessons.StatePrompt)
	dl.SeenInPrompt = lessons.DemoteSeenInPrompt - 1
	if err := lessons.In(home).Save(dl); err != nil {
		t.Fatal(err)
	}
	after(newLessonCtx("s3", "d1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if want := "lessons: coder " + l.ID + " prompt -> hint"; strings.Count(buf.String(), want) != 1 {
		t.Fatalf("demotion line missing in %q", buf.String())
	}

	// Retirement: a hinted failure followed by the same failure again.
	buf.Reset()
	rl := seedLesson(t, home, "coder", lessons.StateHint)
	rl.Outcomes = make([]bool, lessons.RetireRepeated-1)
	if err := lessons.In(home).Save(rl); err != nil {
		t.Fatal(err)
	}
	after(newLessonCtx("s4", "r1"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	time.Sleep(2 * time.Millisecond)
	after(newLessonCtx("s4", "r1").withCall("c2"), namedTool{name: "Bash"}, lessonTestArgs, failResult(), nil)
	if want := "lessons: coder " + l.ID + " hint -> retired"; strings.Count(buf.String(), want) != 1 {
		t.Fatalf("retirement line missing in %q", buf.String())
	}
	if strings.Contains(buf.String(), "kubectl has no") {
		t.Fatal("lesson content logged")
	}
}
