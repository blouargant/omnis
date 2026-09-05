package agent

import (
	"fmt"
	"sync"

	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// runnableTool is a tool that can be invoked and packed into a model request —
// the shape both the plain agenttool and the resumable one satisfy.
type runnableTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx adk.ToolContext, args any) (map[string]any, error)
}

// concurrentAgentTool bounds how many invocations of ONE sub-agent may be in
// flight at once. It is the single wrapper every delegation mount point goes
// through, and `max_instances` is simply its width (default 1).
//
// It advertises the inner sub-agent's OWN single-task schema, unchanged — that is
// the whole point. It replaces an earlier design in which max_instances > 1 swapped
// the sub-agent for a BATCH tool ({tasks: [...]}), i.e. max_instances changed the
// SCHEMA the model saw. Two things make the plain schema strictly better:
//
//   - ADK already fans out for us. Flow.handleFunctionCalls dispatches every
//     function call in ONE model response concurrently (a sync.WaitGroup and a
//     goroutine per call) against the single shared tool object from toolsDict. So
//     a model that wants three lookups just emits three calls and they overlap; a
//     batch schema was never needed to get parallelism, only to cap it.
//   - A batch schema is a schema weak models decline to use. With web_fetcher at
//     max_instances 8 the `high` critic silently never called it and wrote "I
//     cannot confirm this claim without fetching" — the tool was mounted and
//     declared correctly; the model simply would not construct {tasks: [...]}. A
//     single-task call is the shape every model already knows, so the fan-out now
//     works for a non-premium caller too.
//
// Excess siblings QUEUE on the semaphore; they are not rejected. The previous
// max_instances <= 1 wrapper took a TryLock and failed the losers with "already
// running", which silently threw work away: four concurrent web_fetcher calls from
// one critic response ran ONCE and lost three retrievals. Queueing runs all four
// (serially, at width 1) — which is what the caller asked for. The wait is bound to
// the caller's context, so a cancelled turn unblocks a queued sibling instead of
// stranding its goroutine.
//
// Concurrency is safe at the agent level: agenttool builds its own runner, session
// service and session per call, and the resumable wrapper keys durable state by
// per-call handle (an in-use handle cannot be double-resumed). The semaphore is
// therefore a POLICY limit — how much parallel work one caller may provoke — not a
// correctness lock. It is per MOUNT POINT (wrapSubAgentTool mints a fresh wrapper
// per caller), so a gatherer shared by two specialists gives each its own width.
//
// The semaphore is keyed BY SESSION, not held as one channel on the wrapper.
// wrapSubAgentTool mints one wrapper per mount point, and mount points are built by
// BuildInstance — once per config generation, never per session. A single shared
// channel therefore made max_instances a SERVER-WIDE serialisation rather than the
// per-caller policy limit it is documented to be: at width 1 (k8s_validator) one
// invocation that never returned held the only token for every session on the box,
// so an unrelated chat's Kubernetes mutation queued behind somebody else's stuck
// validation. Nothing bounded that wait — there is no deadline on inner.Run, and
// the realistic wedge is an unanswered permission card inside the sub-agent, which
// has no timeout at all (askuser.DefaultTimeout is 0) and survives a client
// disconnect. Per-session keying restores the intended meaning: how much parallel
// work ONE caller may provoke.
type concurrentAgentTool struct {
	inner runnableTool
	max   int

	mu   sync.Mutex
	sems map[string]*sessionSem
}

// sessionSem is one session's slots. refs counts holders AND waiters so the entry
// can be forgotten the moment nobody is using it (see drop).
type sessionSem struct {
	ch   chan struct{}
	refs int
}

func newConcurrentAgentTool(inner runnableTool, max int) tool.Tool {
	if max < 1 {
		max = 1
	}
	return &concurrentAgentTool{inner: inner, max: max, sems: map[string]*sessionSem{}}
}

func (t *concurrentAgentTool) Name() string { return t.inner.Name() }

func (t *concurrentAgentTool) IsLongRunning() bool { return t.inner.IsLongRunning() }

// Declaration is the inner sub-agent's own, verbatim: one call = one job.
func (t *concurrentAgentTool) Declaration() *genai.FunctionDeclaration {
	return t.inner.Declaration()
}

// Description tells a fan-out-capable sub-agent's caller that it may issue several
// calls at once. Without this the model has no way to know parallelism is available
// — the schema is single-task and looks exactly like a serial tool.
func (t *concurrentAgentTool) Description() string {
	if t.max <= 1 {
		return t.inner.Description()
	}
	return t.inner.Description() + fmt.Sprintf(
		"\n\nYou may call this tool SEVERAL TIMES IN THE SAME RESPONSE — one call per "+
			"independent job — and the calls run in parallel (up to %d at a time; any beyond "+
			"that queue and start as slots free up). Prefer one call per question over "+
			"bundling several questions into one call.", t.max)
}

// ProcessRequest packs THIS wrapper (not the inner agenttool) into the request.
// ADK builds its function-call dispatch map from req.Tools, so registering the
// inner here would make the runner call the inner's Run directly and bypass the
// semaphore — the concurrency limit would silently not exist. The declaration is
// the inner's either way (Declaration delegates), so the model sees no difference.
func (t *concurrentAgentTool) ProcessRequest(_ adk.ToolContext, req *model.LLMRequest) error {
	return packToolDecl(req, t)
}

func (t *concurrentAgentTool) Run(ctx adk.ToolContext, args any) (map[string]any, error) {
	release, err := t.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return t.inner.Run(ctx, args)
}

// semKey is the session whose budget this call spends. It must be the USER-FACING
// session id, not ctx.SessionID(): a sub-agent runs under agenttool's ephemeral
// per-call session, so keying on that would mint a fresh semaphore for every
// invocation and the limit would never bind at all. realSessionID recovers the id
// the surface planted on the run context, which propagates into sub-agents.
//
// An absent id (CLI examples, tests) collapses to one shared bucket — the
// single-session case, where sharing is exactly right.
func (t *concurrentAgentTool) semKey(ctx adk.ToolContext) string {
	if ctx == nil {
		return ""
	}
	return realSessionID(ctx)
}

// acquire takes a slot, waiting while the sub-agent is at its concurrency limit.
// adk.ToolContext embeds context.Context, so a cancelled turn (Stop button, session
// end, shutdown) releases a queued sibling rather than leaking its goroutine for
// the lifetime of the process.
//
// The nil-context case is handled by leaving `done` nil rather than by branching:
// a receive on a nil channel blocks forever, so the select degenerates to a plain
// blocking send. That keeps ONE code path — a separate nil branch would be a path
// production never takes and tests always would, which is how a regression test
// ends up passing against the very bug it exists to catch.
func (t *concurrentAgentTool) acquire(ctx adk.ToolContext) (func(), error) {
	key := t.semKey(ctx)

	t.mu.Lock()
	s := t.sems[key]
	if s == nil {
		s = &sessionSem{ch: make(chan struct{}, t.max)}
		t.sems[key] = s
	}
	s.refs++
	t.mu.Unlock()

	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	select {
	case s.ch <- struct{}{}:
		return func() {
			<-s.ch
			t.drop(key)
		}, nil
	case <-done:
		t.drop(key)
		return nil, fmt.Errorf("sub-agent %q: cancelled while waiting for a free slot (max_instances=%d): %w",
			t.Name(), t.max, ctx.Err())
	}
}

// drop releases this call's reference and forgets the session's semaphore once
// nobody holds or awaits it. A generation lives until the next hot-reload, so an
// unpruned map would retain one entry per session the server ever serves.
//
// refs counts holders AND waiters, so refs == 0 means the channel is idle and
// empty: a later arrival minting a fresh one is equivalent, never a lost slot.
func (t *concurrentAgentTool) drop(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sems[key]
	if s == nil {
		return
	}
	if s.refs--; s.refs <= 0 {
		delete(t.sems, key)
	}
}

// declaredTool is a tool that also advertises a function declaration — the shape
// ADK's request packing needs.
type declaredTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
}

// packToolDecl replicates google.golang.org/adk/internal/toolinternal/toolutils.PackTool
// (unexported) for a single tool: it registers the tool for dispatch under its name
// in req.Tools and appends its function declaration to req.Config.Tools.
func packToolDecl(req *model.LLMRequest, t declaredTool) error {
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	name := t.Name()
	if _, ok := req.Tools[name]; ok {
		return fmt.Errorf("duplicate tool: %q", name)
	}
	req.Tools[name] = t

	decl := t.Declaration()
	if decl == nil {
		return nil
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	var funcTool *genai.Tool
	for _, ft := range req.Config.Tools {
		if ft != nil && ft.FunctionDeclarations != nil {
			funcTool = ft
			break
		}
	}
	if funcTool == nil {
		req.Config.Tools = append(req.Config.Tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{decl},
		})
	} else {
		funcTool.FunctionDeclarations = append(funcTool.FunctionDeclarations, decl)
	}
	return nil
}
