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
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/lessons"
	"github.com/blouargant/omnis/internal/paths"
)

const (
	// lessonQueueCap bounds the episodes waiting for the lesson writer; an
	// episode arriving at a full queue is dropped.
	lessonQueueCap = 64
	// lessonHintKey is the result key the hint is added under.
	lessonHintKey = "lesson"
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

	enabledMu sync.RWMutex
	enabledFn func() bool

	mu      sync.Mutex
	pending map[pendingHintKey]pendingHint
	seq     uint64

	startsMu sync.Mutex
	starts   map[callKey]time.Time

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

// newLessonService builds the service. enabled reports the fleet-wide switch
// at call time; nil ⇒ the OMNIS_LESSONS env var, default on (setEnabled
// replaces it once a Manager exists).
func newLessonService(enabled func() bool) *lessonService {
	s := &lessonService{
		queue:     make(chan lessonEpisode, lessonQueueCap),
		now:       time.Now,
		enabledFn: enabled,
		pending:   map[pendingHintKey]pendingHint{},
		starts:    map[callKey]time.Time{},
		none:      map[noneKey]lessonStamp{},
		runs:      map[runCountKey]lessonStamp{},
		done:      make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.tracker = newLessonTracker(s.enqueue)
	go s.worker()
	return s
}

// setEnabled installs the fleet-wide switch read at call time. NewManager sets
// it to read the CURRENT generation's resolved settings, so a hot-reload that
// turns lessons off silences sessions still draining on an older generation.
func (s *lessonService) setEnabled(f func() bool) {
	if s == nil {
		return
	}
	s.enabledMu.Lock()
	s.enabledFn = f
	s.enabledMu.Unlock()
}

func (s *lessonService) isEnabled() bool {
	s.enabledMu.RLock()
	f := s.enabledFn
	s.enabledMu.RUnlock()
	if f != nil {
		return f()
	}
	if on, ok := lessonsEnvSwitch(); ok {
		return on
	}
	return true
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
// root in cookie mode, else the shared write root.
func (s *lessonService) rootFor(sessionID string) string {
	if root := collectionRoot(sessionID); root != "" {
		return root
	}
	return paths.ConfigWriteDir()
}

// storeFor returns the lesson store of a session.
func (s *lessonService) storeFor(sessionID string) *lessons.Store {
	return lessons.In(s.rootFor(sessionID))
}

// Close stops queueing episodes and stops the writer worker, waiting for it for
// at most lessonCloseWait.
func (s *lessonService) Close() {
	if s == nil {
		return
	}
	s.closed.Store(true)
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
	s.starts[k] = at
	if len(s.starts) <= lessonStartsMax {
		return
	}
	for key, t := range s.starts {
		if at.Sub(t) > lessonStartTTL {
			delete(s.starts, key)
		}
	}
	for len(s.starts) > lessonStartsMax {
		var oldest callKey
		var min time.Time
		first := true
		for key, t := range s.starts {
			if first || t.Before(min) {
				oldest, min, first = key, t, false
			}
		}
		delete(s.starts, oldest)
	}
}

// takeStart returns and forgets a call's recorded start time, or fallback when
// none was recorded (a callback earlier in the chain short-circuited the call).
func (s *lessonService) takeStart(k callKey, fallback time.Time) time.Time {
	s.startsMu.Lock()
	defer s.startsMu.Unlock()
	t, ok := s.starts[k]
	if !ok {
		return fallback
	}
	delete(s.starts, k)
	return t
}

// BeforeModel returns the before-model callback that puts an agent's prompt
// lessons in its system instruction. Inert for now.
func (s *lessonService) BeforeModel(agent string) llmagent.BeforeModelCallback {
	return nil
}

func (s *lessonService) afterTool(agent string, tc adk.ToolContext, name string, args, result map[string]any, err error) map[string]any {
	now := s.now()
	sid := realSessionID(tc)
	started := s.takeStart(callKey{sid, tc.InvocationID(), tc.FunctionCallID()}, now)
	root := s.rootFor(sid)
	store := lessons.In(root)
	errText, failed := classifyToolFailure(result, err)
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
	l, ok := store.Find(agent, sig)
	if !ok || l.State == lessons.StateRetired {
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
// started after the hint was delivered: a success recovered, the same error
// again repeated, another error neither. A sibling that started before the hint
// leaves it pending — it ran without knowing the lesson.
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
	if _, err := store.RecordOutcome(k.agent, p.id, !failed); err != nil {
		log.Printf("lessons: record outcome of %s for %s: %v", p.id, k.agent, err)
	}
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
