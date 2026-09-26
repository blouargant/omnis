package mcp

import (
	"context"
	"sync"
	"testing"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/deps"
)

// subAgentCtx simulates a sub-agent's tool context: its SessionID() is
// agenttool's ephemeral per-call session, while the run context still carries
// the user-facing session planted by events.WithRootSession.
type subAgentCtx struct {
	context.Context
	id string
}

func (c subAgentCtx) SessionID() string { return c.id }

func subCtx(root, ephemeral string) context.Context {
	return subAgentCtx{Context: events.WithRootSession(context.Background(), root), id: ephemeral}
}

// answeringRegistry records the session every question is registered under and
// answers text inputs with "val-<session>" (and declines install prompts).
func answeringRegistry(t *testing.T) (*askuser.Registry, func() []string) {
	t.Helper()
	reg := askuser.NewRegistry()
	var mu sync.Mutex
	var seen []string
	reg.SetNotify(func(q askuser.Question) {
		mu.Lock()
		seen = append(seen, q.SessionID)
		mu.Unlock()
		go func(qid, sid string) {
			_ = reg.Resolve(sid, qid, askuser.Answer{Text: "val-" + sid, Selected: []string{"Skip"}})
		}(q.ID, q.SessionID)
	})
	return reg, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestInputResolverAttributesAskToRootSession(t *testing.T) {
	reg, seen := answeringRegistry(t)
	r := NewInputResolver(reg)
	v, err := r.Resolve(subCtx("root-sess", "ephemeral-sess"), Input{ID: "pat", Type: InputPromptString})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := seen(); len(got) != 1 || got[0] != "root-sess" {
		t.Fatalf("question registered under %v, want [root-sess]", got)
	}
	if v != "val-root-sess" {
		t.Fatalf("value = %q", v)
	}
}

func TestInputResolverFallsBackToContextSession(t *testing.T) {
	reg, seen := answeringRegistry(t)
	r := NewInputResolver(reg)
	ctx := subAgentCtx{Context: context.Background(), id: "leader-sess"}
	if _, err := r.Resolve(ctx, Input{ID: "pat", Type: InputPromptString}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := seen(); len(got) != 1 || got[0] != "leader-sess" {
		t.Fatalf("question registered under %v, want [leader-sess]", got)
	}
}

func TestEnsureServerDepsAttributesAskToRootSession(t *testing.T) {
	reg, seen := answeringRegistry(t)
	r := NewInputResolver(reg)
	s := Server{Name: "x", Requires: []deps.Requirement{{
		Command: "omnis-no-such-binary-zz9",
		Install: deps.Install{Default: "true"},
	}}}
	if err := ensureServerDeps(subCtx("root-sess", "ephemeral-sess"), r, s); err == nil {
		t.Fatal("expected unmet-dependency error after declining")
	}
	if got := seen(); len(got) != 1 || got[0] != "root-sess" {
		t.Fatalf("install question registered under %v, want [root-sess]", got)
	}
}

// Not parallel: installs the process-wide owner resolver.
func TestInputCacheIsScopedPerOwner(t *testing.T) {
	owners := map[string]string{"a1": "alice", "a2": "alice", "b1": "bob"}
	SetOwnerResolver(func(sid string) string { return owners[sid] })
	defer SetOwnerResolver(nil)

	reg, seen := answeringRegistry(t)
	r := NewInputResolver(reg)
	in := Input{ID: "pat", Type: InputPromptString}

	va, err := r.Resolve(subCtx("a1", "e1"), in)
	if err != nil || va != "val-a1" {
		t.Fatalf("alice Resolve = (%q, %v)", va, err)
	}
	vb, err := r.Resolve(subCtx("b1", "e2"), in)
	if err != nil {
		t.Fatalf("bob Resolve: %v", err)
	}
	if vb == va {
		t.Fatalf("bob was served alice's cached answer %q", vb)
	}
	if vb != "val-b1" {
		t.Fatalf("bob value = %q, want val-b1", vb)
	}
	// Same owner, another session: cache hit, no new prompt.
	va2, err := r.Resolve(subCtx("a2", "e3"), in)
	if err != nil || va2 != "val-a1" {
		t.Fatalf("alice second session Resolve = (%q, %v)", va2, err)
	}
	if got := seen(); len(got) != 2 {
		t.Fatalf("prompts = %v, want exactly 2 (one per owner)", got)
	}
}

// Not parallel: asserts the default (no resolver) process-wide cache.
func TestInputCacheWithoutOwnerResolverIsProcessWide(t *testing.T) {
	SetOwnerResolver(nil)
	reg, seen := answeringRegistry(t)
	r := NewInputResolver(reg)
	in := Input{ID: "pat", Type: InputPromptString}
	va, _ := r.Resolve(subCtx("a1", "e1"), in)
	vb, _ := r.Resolve(subCtx("b1", "e2"), in)
	if va != vb || len(seen()) != 1 {
		t.Fatalf("single-user cache changed: a=%q b=%q prompts=%v", va, vb, seen())
	}
}
