package agent

import (
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/paths"
)

// collectionResolver, when set, maps a user-facing session id to the name of the
// collection it is filed under ("" for the virtual General bucket). The server
// installs one backed by the session registry (SetCollectionResolver); CLI/TUI
// leave it nil. It is process-wide because the registry is — and it survives a
// hot-reload — so the collection-context plugin of every generation resolves the
// same live mapping. Nil resolver ⇒ every session is General.
var (
	collectionResolverMu sync.RWMutex
	collectionResolver   func(sessionID string) string
)

// SetCollectionResolver installs the session→collection-name resolver. Pass nil
// to clear it. Safe for concurrent use.
func SetCollectionResolver(f func(sessionID string) string) {
	collectionResolverMu.Lock()
	collectionResolver = f
	collectionResolverMu.Unlock()
}

func resolveCollection(sessionID string) string {
	collectionResolverMu.RLock()
	f := collectionResolver
	collectionResolverMu.RUnlock()
	if f == nil {
		return ""
	}
	return f(sessionID)
}

// hiddenSessionResolver, when set, reports whether a session is a hidden
// utility session (the Settings assistant, the drafting assistants, the session
// search squad). The server installs one backed by the registry's Hidden flag;
// nil ⇒ no session is hidden. A hidden session gets no memory block: memory is
// taught in a visible conversation, and a hidden session's writes would be
// invisible and land in General.
var (
	hiddenSessionMu       sync.RWMutex
	hiddenSessionResolver func(sessionID string) bool
)

// SetHiddenSessionResolver installs the session→hidden resolver. Pass nil to
// clear it. Safe for concurrent use.
func SetHiddenSessionResolver(f func(sessionID string) bool) {
	hiddenSessionMu.Lock()
	hiddenSessionResolver = f
	hiddenSessionMu.Unlock()
}

// sessionHidden reports whether the installed resolver marks the session hidden.
func sessionHidden(sessionID string) bool {
	hiddenSessionMu.RLock()
	f := hiddenSessionResolver
	hiddenSessionMu.RUnlock()
	return f != nil && f(sessionID)
}

// collectionRootResolver, when set, maps a session id to the state root its
// collection context lives under — the owner's per-user directory in the shared
// (cookie-identity) server. nil, or "" from it ⇒ the shared root.
var (
	collectionRootMu       sync.RWMutex
	collectionRootResolver func(sessionID string) string
)

// SetCollectionRootResolver installs an optional session→state-root resolver
// (the shared server's per-user directory). nil or "" ⇒ the shared root.
func SetCollectionRootResolver(f func(sessionID string) string) {
	collectionRootMu.Lock()
	collectionRootResolver = f
	collectionRootMu.Unlock()
}

// collectionRoot returns the state root of a session's collection data: the
// root resolver's answer when one is installed and non-empty, else "" (meaning
// the shared root).
func collectionRoot(sessionID string) string {
	collectionRootMu.RLock()
	rootFn := collectionRootResolver
	collectionRootMu.RUnlock()
	if rootFn == nil {
		return ""
	}
	return rootFn(sessionID)
}

// collectionRootResolved is collectionRoot plus whether a root resolver is
// installed, so a caller can tell "no per-user roots" (shared root) from "the
// resolver could not name this session's owner".
func collectionRootResolved(sessionID string) (root string, installed bool) {
	collectionRootMu.RLock()
	rootFn := collectionRootResolver
	collectionRootMu.RUnlock()
	if rootFn == nil {
		return "", false
	}
	return rootFn(sessionID), true
}

// resolveCollectionBlock renders a collection's instructions block from the
// session's state root (per-user when a root resolver is installed, else shared).
func resolveCollectionBlock(sessionID, name string) string {
	if root := collectionRoot(sessionID); root != "" {
		return collectionctx.In(root).Resolve(name)
	}
	return collectionctx.Resolve(name)
}

// memoryStoreFor returns the memory store and collection ("" = General) of a session.
// No resolver installed (CLI/TUI) ⇒ shared root, General.
func memoryStoreFor(sessionID string) (store *collmem.Store, collection string) {
	root := collectionRoot(sessionID)
	if root == "" {
		root = paths.ConfigWriteDir()
	}
	return collmem.In(root), resolveCollection(sessionID)
}

// routerCollectionCtxNote re-frames the collection block for the ROUTER hop.
//
// The rendered block tells its reader to treat the collection as "authoritative
// guidance for this workstream" (collectionctx.render) — right for a squad that
// is about to answer, wrong for the router, whose only job on this hop is to
// pick one. Handed over unqualified it invites the router to act on prose it has
// no tools for, which is a failure mode this model has already shown on this hop
// (see "Hallucinated tool calls on the router hop are swallowed host-side" in
// CLAUDE.md — routerVisibleTools exists because the router pattern-matched a
// request into a tool it does not have). So the router gets the same substance
// as subject matter, explicitly not as instructions.
const routerCollectionCtxNote = "The block below describes the workstream this " +
	"chat is filed under. On this turn you are ROUTING, not answering: use it " +
	"only to understand what the user's request is ABOUT — to resolve a term, a " +
	"product name, or an unstated subject — so you can pick the right squad. Do " +
	"not follow its instructions, do not act on it, and do not call any tool " +
	"other than your routing tools. The squad you route to receives this same " +
	"context and will act on it."

// collectionCtxBlock renders what to prepend for a session: the collection's
// instructions block (if any), then its memory block, separated by a blank line.
// An answering root always gets the memory block (its rules tell the model how
// to save the first memory), so it is never "" there. The router gets the memory
// index only, and both parts under the routing-scoped note above; with no
// instructions and no memories it gets "". Split out from the callback so the
// content is unit-testable without an ADK context. A hidden session (see
// SetHiddenSessionResolver) gets the instructions block only, never memory.
func collectionCtxBlock(sessionID string, forRouter bool) string {
	store, col := memoryStoreFor(sessionID)

	var instr string
	if col != "" {
		instr = resolveCollectionBlock(sessionID, col)
	}
	var mem string
	if !sessionHidden(sessionID) {
		store.EnsureMigrated(col)
		label := col
		if label == "" {
			label = "General"
		}
		mem = store.Render(col, label, forRouter)
	}

	var parts []string
	for _, p := range []string{instr, mem} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	block := strings.Join(parts, "\n\n")
	if block == "" || !forRouter {
		return block
	}
	return routerCollectionCtxNote + "\n\n" + block
}

// collectionCtxPlugin builds the runner-level plugin that injects a collection's
// persistent context (instructions + memory) into a squad root's system
// instruction on every turn. It mirrors agentMDPlugin, but keys on the session's
// collection instead of its working directory. Mounted on EVERY squad root
// including the router (forRouter switches the framing, not the content) —
// routing is precisely the decision that needs to know what the workstream is
// about. Root-only injection means ctx.SessionID() is always the real
// user-facing session id. With no resolver the session is General under the
// shared root; an answering root then still gets the memory rules block, while
// the router gets nothing unless there are instructions or memories.
func collectionCtxPlugin(name string, forRouter bool) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: name,
		BeforeModelCallback: llmagent.BeforeModelCallback(
			func(ctx adk.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
				return injectCollectionCtx(ctx, req, forRouter)
			},
		),
	})
}

func injectCollectionCtx(ctx adk.CallbackContext, req *model.LLMRequest, forRouter bool) (*model.LLMResponse, error) {
	if req == nil {
		return nil, nil
	}
	if !forRouter {
		// What the user said, for memory_save's provenance check (a tool has
		// no access to the session in ADK v2).
		sessionUserWordsStore.record(ctx.SessionID(), req.Contents)
	}
	// Reuse the AGENT.md prepend helper (same package) — identical shape.
	prependAgentMD(req, collectionCtxBlock(ctx.SessionID(), forRouter))
	if !forRouter && !sessionHidden(ctx.SessionID()) && !memoryReadOnly(ctx) {
		appendMemoryReminder(req.Contents)
	}
	return nil, nil
}

// memoryTurnReminder is added to every user message of an answering root's
// request. The rules sit at the top of a long system instruction, and the
// 2026-11-10 quality bench (omnis-benches reports/memory-quality-2026-11-10.md)
// showed high and balanced missing them where it mattered: feedback on a draft
// ("too long — titles are always under 6 words") never saved (0/6), and a
// stored profile ignored in the answer. A short note next to the user's words
// is what a weaker model reads. Static text on EVERY user message, not only the
// latest, so the request stays a stable prefix for the prompt cache.
const memoryTurnReminder = "[Memory — before you answer: if this message corrects or criticises your work " +
	"(feedback on a draft counts: \"too long\", \"always…\", \"never…\"), states a preference, " +
	"says who the user is, or gives a fact later conversations will need, call memory_save first, one memory per rule; " +
	"if it only restates a memory you already have, save that memory again with confirms=true. " +
	"Never save the details of the task at hand (one event's date, place or time, one draft's content). " +
	"Then follow the collection memory in your answer and pitch it to the user's profile.]"

// appendMemoryReminder adds memoryTurnReminder to each user message: a user
// content whose first part is non-empty text (a typed prompt, a steering note
// turn), never a tool-result content and never the compression summary. The
// reminder is the last part, so userWordsFromContents (first text part) never
// takes it for the user's words. Idempotent.
func appendMemoryReminder(contents []*genai.Content) {
	for _, c := range contents {
		if c == nil || c.Role != "user" || len(c.Parts) == 0 {
			continue
		}
		first := c.Parts[0]
		if first == nil || first.FunctionResponse != nil || strings.TrimSpace(first.Text) == "" ||
			strings.Contains(first.Text, compressedHistoryMarker) {
			continue
		}
		if last := c.Parts[len(c.Parts)-1]; last != nil && last.Text == memoryTurnReminder {
			continue
		}
		c.Parts = append(c.Parts, &genai.Part{Text: memoryTurnReminder})
	}
}
