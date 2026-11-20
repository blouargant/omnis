package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/events"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/lessons"
	"github.com/blouargant/omnis/internal/paths"
)

const (
	// lessonQueueCap bounds the episodes waiting for the lesson writer; an
	// episode arriving at a full queue is dropped.
	lessonQueueCap = 64
	// lessonHintKey is the result key the hint is added under.
	lessonHintKey = "lesson"
	// lessonsBlockTag opens the prompt block (see lessons.PromptBlock).
	lessonsBlockTag = "<agent-lessons>"
	// lessonPendingMax bounds the hints waiting for their outcome.
	lessonPendingMax = 1024
	// lessonStartsMax and lessonStartTTL bound the recorded call start times: a
	// call whose after-tool callback never runs must not leak.
	lessonStartsMax = 4096
	lessonStartTTL  = 30 * time.Minute
	// lessonCloseWait bounds how long Close waits for the worker.
	lessonCloseWait = 2 * time.Second
)

// lessonService is the process-wide agent-lessons machinery: it adds a known
// lesson to a failing tool result (the hint), records whether the next call of
// that tool recovered, and queues failure→success episodes for the lesson
// writer. It lives on Infrastructure and survives hot-reload.
//
// Every entry point recovers from a panic and leaves the tool call alone: a
// failure in lessons code never breaks or delays a tool call.
type lessonService struct {
	tracker *lessonTracker
	queue   chan lessonEpisode
	closed  atomic.Bool
	now     func() time.Time

	// switchState is the fleet-wide switch: 0 = not set yet (enabledFn, else
	// OMNIS_LESSONS, default on), 1 = on, 2 = off. The Manager sets it whenever
	// the current generation changes, so reading it is one atomic load.
	switchState atomic.Int32
	enabledFn   func() bool // tests only; read when switchState is 0

	mu      sync.Mutex
	pending map[pendingHintKey]pendingHint
	seq     uint64

	startsMu sync.Mutex
	starts   map[callKey]callStart

	// The writer worker: one goroutine consuming the queue.
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	writerMu sync.RWMutex
	writer   lessonWriter

	wmu     sync.Mutex // guards the two bounded maps below
	none    map[noneKey]lessonStamp
	runs    map[runCountKey]lessonStamp
	wseq    uint64
	lastLog atomic.Int64 // unix nanos of the last queue-full log line
}

// callKey identifies one tool call: its start time is recorded by BeforeTool
// and taken by AfterTool.
type callKey struct{ session, invocation, call string }

// callStart is what is known about a call before its after-tool callback: when
// it started and, for Bash, the exit status the tool reported.
type callStart struct {
	at   time.Time
	exit *bashExit
}

type pendingHintKey struct {
	run     runKey
	toolKey string
}

// pendingHint is a hint shown after a failed call: the next call of the same
// tool in the same run decides whether it helped.
type pendingHint struct {
	id, sig string
	seq     uint64
	at      time.Time // when the hint was delivered
}

// newLessonService builds the service. enabled, when non-nil, answers the
// fleet-wide switch until setEnabledValue is called (tests); nil ⇒ the
// OMNIS_LESSONS env var, default on.
func newLessonService(enabled func() bool) *lessonService {
	s := &lessonService{
		queue:     make(chan lessonEpisode, lessonQueueCap),
		now:       time.Now,
		enabledFn: enabled,
		pending:   map[pendingHintKey]pendingHint{},
		starts:    map[callKey]callStart{},
		none:      map[noneKey]lessonStamp{},
		runs:      map[runCountKey]lessonStamp{},
		done:      make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.tracker = newLessonTracker(s.enqueue)
	go s.worker()
	return s
}

// setEnabledValue sets the fleet-wide switch. The Manager calls it with the
// CURRENT generation's resolved setting whenever that generation changes, so a
// hot-reload that turns lessons off also silences sessions still draining on an
// older generation. It also installs or removes the Bash exit observer.
func (s *lessonService) setEnabledValue(on bool) {
	if s == nil {
		return
	}
	if on {
		s.switchState.Store(1)
	} else {
		s.switchState.Store(2)
	}
	s.syncBashObserver()
}

func (s *lessonService) isEnabled() bool {
	switch s.switchState.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	if s.enabledFn != nil {
		return s.enabledFn()
	}
	if on, ok := lessonsEnvSwitch(); ok {
		return on
	}
	return true
}

// syncBashObserver installs the Bash exit observer while lessons are on and
// removes it while they are off, so a disabled service costs the Bash tool one
// nil check. The observer is process-wide: removing it clears only this
// service's own, never one another service installed since.
func (s *lessonService) syncBashObserver() {
	if s.isEnabled() {
		fstools.InstallBashExitObserver(s, s.observeBashExit)
	} else {
		fstools.RemoveBashExitObserver(s)
	}
}

// lessonsEnvSwitch reads OMNIS_LESSONS; ok is false when it is unset or not a
// boolean.
func lessonsEnvSwitch() (on, ok bool) {
	raw := strings.TrimSpace(os.Getenv("OMNIS_LESSONS"))
	if raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return v, true
}

// lessonsActive reports whether a generation built from runtime gives cfg its
// lesson callbacks. Decided from the settings being built, never from the
// current generation: a reload that turns lessons on is built while the
// previous (off) generation is still current.
func lessonsActive(runtime RuntimeSettings, cfg RuntimeAgentConfig) bool {
	return runtime.LessonsEnabled && cfg.Lessons
}

// rootFor returns the state root of a session's lessons: the owner's per-user
// root in cookie mode, else the shared write root. ok is false when a per-user
// root resolver is installed but cannot name the session's owner: lessons then
// do nothing for that call rather than fall back to the shared root.
func (s *lessonService) rootFor(sessionID string) (root string, ok bool) {
	root, installed := collectionRootResolved(sessionID)
	if root != "" {
		return root, true
	}
	if installed {
		return "", false
	}
	return paths.ConfigWriteDir(), true
}

// Close stops queueing episodes, removes its Bash exit observer if it is still
// the installed one, and stops the writer worker, waiting for it for at most
// lessonCloseWait.
func (s *lessonService) Close() {
	if s == nil {
		return
	}
	s.closed.Store(true)
	fstools.RemoveBashExitObserver(s)
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(lessonCloseWait):
	}
}

// enqueue hands an episode to the writer without ever blocking: a full queue
// drops it.
func (s *lessonService) enqueue(ep lessonEpisode) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("lessons: episode dropped after a panic: %v", r)
		}
	}()
	if s.closed.Load() {
		return
	}
	select {
	case s.queue <- ep:
	default:
		now := time.Now().UnixNano()
		if last := s.lastLog.Load(); last == 0 || now-last >= int64(time.Minute) {
			if s.lastLog.CompareAndSwap(last, now) {
				log.Printf("lessons: episode queue full, dropped an episode of %s on %s", ep.Agent, ep.ToolKey)
			}
		}
	}
}

// AfterTool returns the after-tool callback of one agent. It must run after the
// output shaper so the hint is never cut. nil receiver ⇒ nil callback.
func (s *lessonService) AfterTool(agent string) llmagent.AfterToolCallback {
	if s == nil || agent == "" {
		return nil
	}
	return func(tc adk.ToolContext, t tool.Tool, args, result map[string]any, err error) (out map[string]any, outErr error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("lessons: after-tool callback of %s recovered from a panic: %v", agent, r)
				out, outErr = nil, nil
			}
		}()
		if tc == nil || t == nil || !s.isEnabled() {
			return nil, nil
		}
		return s.afterTool(agent, tc, t.Name(), args, result, err), nil
	}
}

// BeforeTool returns the before-tool callback that records when a call
// started, so a parallel sibling that started before a failure completed is
// never read as the success that followed it. It always returns nil, nil: it
// never short-circuits a tool. nil receiver ⇒ nil callback.
func (s *lessonService) BeforeTool(agent string) llmagent.BeforeToolCallback {
	if s == nil || agent == "" {
		return nil
	}
	return func(tc adk.ToolContext, _ tool.Tool, _ map[string]any) (map[string]any, error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("lessons: before-tool callback of %s recovered from a panic: %v", agent, r)
			}
		}()
		if tc == nil || !s.isEnabled() {
			return nil, nil
		}
		s.recordStart(callKey{realSessionID(tc), tc.InvocationID(), tc.FunctionCallID()}, s.now())
		return nil, nil
	}
}

func (s *lessonService) recordStart(k callKey, at time.Time) {
	s.startsMu.Lock()
	defer s.startsMu.Unlock()
	s.starts[k] = callStart{at: at}
	if len(s.starts) <= lessonStartsMax {
		return
	}
	for key, st := range s.starts {
		if at.Sub(st.at) > lessonStartTTL {
			delete(s.starts, key)
		}
	}
	for len(s.starts) > lessonStartsMax {
		var oldest callKey
		var min time.Time
		first := true
		for key, st := range s.starts {
			if first || st.at.Before(min) {
				oldest, min, first = key, st.at, false
			}
		}
		delete(s.starts, oldest)
	}
}

// recordBashExit attaches the exit status of a Bash call to its recorded start.
// A call with no recorded start (an agent without lessons, a short-circuited
// call) is ignored, so nothing accumulates for it.
func (s *lessonService) recordBashExit(k callKey, exit bashExit) {
	s.startsMu.Lock()
	defer s.startsMu.Unlock()
	st, ok := s.starts[k]
	if !ok {
		return
	}
	st.exit = &exit
	s.starts[k] = st
}

// observeBashExit is the Bash exit observer installed in core/tools while
// lessons are on (see syncBashObserver).
func (s *lessonService) observeBashExit(tc adk.ToolContext, exitCode int, timedOut bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("lessons: bash exit observer recovered from a panic: %v", r)
		}
	}()
	if tc == nil {
		return
	}
	s.recordBashExit(callKey{realSessionID(tc), tc.InvocationID(), tc.FunctionCallID()}, bashExit{code: exitCode, timedOut: timedOut})
}

// takeStart returns and forgets a call's recorded start time, or fallback when
// none was recorded, and the Bash exit status when the tool reported one.
func (s *lessonService) takeStart(k callKey, fallback time.Time) (time.Time, *bashExit) {
	s.startsMu.Lock()
	defer s.startsMu.Unlock()
	st, ok := s.starts[k]
	if !ok {
		return fallback, nil
	}
	delete(s.starts, k)
	return st.at, st.exit
}

// BeforeModel returns the before-model callback that puts an agent's prompt
// lessons in its system instruction. nil receiver ⇒ nil callback. The block
// goes in the system instruction only, never a user-role content; a disabled
// service or an agent with no prompt lesson leaves the request untouched.
func (s *lessonService) BeforeModel(agent string) llmagent.BeforeModelCallback {
	if s == nil || agent == "" {
		return nil
	}
	return func(ctx adk.CallbackContext, req *model.LLMRequest) (resp *model.LLMResponse, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("lessons: before-model callback of %s recovered from a panic: %v", agent, r)
				resp, err = nil, nil
			}
		}()
		if ctx == nil || req == nil || !s.isEnabled() {
			return nil, nil
		}
		sid := steerSessionID(ctx)
		if sid == "" {
			sid = events.RootSessionFromContext(ctx)
		}
		if sid == "" {
			sid = ctx.SessionID()
		}
		root, ok := s.rootFor(sid)
		if !ok {
			return nil, nil
		}
		appendLessonBlock(req, lessons.In(root).PromptBlock(agent, s.now()))
		return nil, nil
	}
}

// appendLessonBlock appends the lessons block at the end of the request's system
// instruction, after a blank line. An empty block, or a request that already
// carries one, is left alone.
func appendLessonBlock(req *model.LLMRequest, block string) {
	if block == "" || req == nil {
		return
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	si := req.Config.SystemInstruction
	if si != nil {
		for _, p := range si.Parts {
			if p != nil && strings.Contains(p.Text, lessonsBlockTag) {
				return
			}
		}
	}
	part := &genai.Part{Text: "\n\n" + block}
	if si == nil {
		req.Config.SystemInstruction = &genai.Content{Parts: []*genai.Part{part}}
		return
	}
	si.Parts = append(si.Parts, part)
}

// lessonsPlugin is the runner plugin carrying the before-model callback on a
// squad root.
func lessonsPlugin(name string, cb llmagent.BeforeModelCallback) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{Name: name, BeforeModelCallback: cb})
}

// lessonStartPlugin is the runner plugin carrying the start-time recorder on a
// squad root. Mounted first so the stamp precedes hooks, permissions and budget.
func lessonStartPlugin(name string, cb llmagent.BeforeToolCallback) (*plugin.Plugin, error) {
	if cb == nil {
		return nil, nil
	}
	return plugin.New(plugin.Config{Name: name, BeforeToolCallback: cb})
}

// logLessonState logs one line when a lesson changed state. Never its content.
func logLessonState(agent, id string, old, now lessons.State) {
	if old != now {
		log.Printf("lessons: %s %s %s -> %s", agent, id, old, now)
	}
}

func (s *lessonService) afterTool(agent string, tc adk.ToolContext, name string, args, result map[string]any, err error) map[string]any {
	now := s.now()
	sid := realSessionID(tc)
	started, exit := s.takeStart(callKey{sid, tc.InvocationID(), tc.FunctionCallID()}, now)
	errText, outcome := classifyToolCall(name, args, result, err, exit)
	if outcome == callNeither {
		// Refused before it ran: neither a mistake nor a proof that the
		// arguments work. No episode, no outcome, no count.
		return nil
	}
	root, ok := s.rootFor(sid)
	if !ok {
		return nil
	}
	failed := outcome == callFailed
	store := lessons.In(root)
	// Counters and episodes only from turns the user started: a hidden
	// session or an unattended turn (mailbox, A2A, …) still gets the hint.
	record := !sessionHidden(sid) && !memoryReadOnly(tc)
	k := runKey{session: sid, invocation: tc.InvocationID(), agent: agent}
	toolKey := lessons.ToolKey(name, args)
	var sig string
	if failed {
		sig = lessons.Signature(toolKey, errText)
	}
	if record {
		s.settleOutcome(store, k, toolKey, failed, sig, started)
		s.tracker.observe(k, root, name, args, errText, failed, started)
	}
	if !failed {
		return nil
	}
	l, found := store.Find(agent, sig)
	if found && record {
		// Count the occurrence first so the hint reflects the lesson's state now.
		if seen, ok, serr := store.RecordSeen(agent, sig, sid, s.now()); serr != nil {
			log.Printf("lessons: count occurrence of %s for %s: %v", l.ID, agent, serr)
		} else if ok {
			logLessonState(agent, l.ID, l.State, seen.State)
			l = seen
		}
	}
	if !found || l.State == lessons.StateRetired {
		return nil
	}
	if record {
		if herr := store.RecordHint(agent, l.ID); herr != nil {
			log.Printf("lessons: count hint %s of %s: %v", l.ID, agent, herr)
		}
		s.setPending(k, toolKey, l.ID, sig, s.now())
	}
	return withLessonHint(result, err, l)
}

// settleOutcome decides a pending hint from the next call of its tool that
// ran and started after the hint was delivered: a success recovered, the same
// error again repeated. Another error drops the hint without counting it. A
// sibling that started before the hint leaves it pending — it ran without
// knowing the lesson — and so does a refused call (it never reaches here).
func (s *lessonService) settleOutcome(store *lessons.Store, k runKey, toolKey string, failed bool, sig string, started time.Time) {
	pk := pendingHintKey{run: k, toolKey: toolKey}
	s.mu.Lock()
	p, ok := s.pending[pk]
	if ok && started.Before(p.at) {
		ok = false
	} else if ok {
		delete(s.pending, pk)
	}
	s.mu.Unlock()
	if !ok || (failed && sig != p.sig) {
		return
	}
	old, _ := store.Find(k.agent, p.sig)
	l, err := store.RecordOutcome(k.agent, p.id, !failed)
	if err != nil {
		log.Printf("lessons: record outcome of %s for %s: %v", p.id, k.agent, err)
		return
	}
	logLessonState(k.agent, p.id, old.State, l.State)
}

func (s *lessonService) setPending(k runKey, toolKey, id, sig string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.pending[pendingHintKey{run: k, toolKey: toolKey}] = pendingHint{id: id, sig: sig, seq: s.seq, at: at}
	for len(s.pending) > lessonPendingMax {
		var oldest pendingHintKey
		var min uint64
		first := true
		for key, p := range s.pending {
			if first || p.seq < min {
				oldest, min, first = key, p.seq, false
			}
		}
		delete(s.pending, oldest)
	}
}

// withLessonHint returns a copy of the result carrying the lesson. A tool error
// becomes an "error" key, as ADK would have rendered it.
func withLessonHint(result map[string]any, err error, l lessons.Lesson) map[string]any {
	out := make(map[string]any, len(result)+2)
	for k, v := range result {
		out[k] = v
	}
	if err != nil {
		if _, has := out["error"]; !has {
			out["error"] = err.Error()
		}
	}
	out[lessonHintKey] = fmt.Sprintf("Learned from an earlier error with this tool: %s\nArguments that worked then: %s", l.Text, l.Worked)
	return out
}
