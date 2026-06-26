package agent

import (
	"testing"
	"time"

	"google.golang.org/adk/session"
)

func newTestResumable(cap int, ttl time.Duration) *resumableAgentTool {
	return &resumableAgentTool{
		runnableTool: &countingRunnableTool{}, // base for Name/Declaration/Description
		svc:          session.InMemoryService(),
		appName:      "investigator",
		ttl:          ttl,
		cap:          cap,
		handles:      map[string]*subSession{},
	}
}

func TestResumableDeclarationAddsResumeSession(t *testing.T) {
	rt := newTestResumable(4, time.Minute)
	decl := rt.Declaration()
	if decl == nil || decl.Parameters == nil {
		t.Fatal("nil declaration")
	}
	if _, ok := decl.Parameters.Properties["resume_session"]; !ok {
		t.Fatalf("declaration missing resume_session: %+v", decl.Parameters.Properties)
	}
	// The base schema's own params survive.
	if _, ok := decl.Parameters.Properties["request"]; !ok {
		t.Fatalf("declaration dropped the base 'request' param: %+v", decl.Parameters.Properties)
	}
	// resume_session must not be required.
	for _, r := range decl.Parameters.Required {
		if r == "resume_session" {
			t.Fatal("resume_session should be optional, not required")
		}
	}
}

func TestResumableDeclarationDoesNotMutateBase(t *testing.T) {
	rt := newTestResumable(4, time.Minute)
	_ = rt.Declaration()
	// The embedded base tool's own declaration must be untouched.
	base := rt.runnableTool.Declaration()
	if _, leaked := base.Parameters.Properties["resume_session"]; leaked {
		t.Fatal("Declaration() mutated the base schema (resume_session leaked into it)")
	}
}

func TestBuildSubAgentContent(t *testing.T) {
	// Lone request → that text.
	c, err := buildSubAgentContent("x", map[string]any{"request": "do it", "resume_session": "h1"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if c.Parts[0].Text != "do it" {
		t.Fatalf("text = %q, want %q", c.Parts[0].Text, "do it")
	}
	// No payload (only resume_session) → error.
	if _, err := buildSubAgentContent("x", map[string]any{"resume_session": "h1"}); err == nil {
		t.Fatal("expected error for missing request")
	}
	// Structured args → JSON object (resume_session stripped).
	c, err = buildSubAgentContent("x", map[string]any{"a": "1", "b": "2", "resume_session": "h"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if c.Parts[0].Text == "" || contains(c.Parts[0].Text, "resume_session") {
		t.Fatalf("structured content should be JSON without resume_session, got %q", c.Parts[0].Text)
	}
}

func TestResumableSweepDropsIdleKeepsBusyAndFresh(t *testing.T) {
	rt := newTestResumable(32, time.Minute)
	rt.handles["old"] = &subSession{lastUsed: time.Now().Add(-2 * time.Minute)}
	rt.handles["fresh"] = &subSession{lastUsed: time.Now()}
	rt.handles["busy"] = &subSession{lastUsed: time.Now().Add(-2 * time.Minute), inUse: true}

	rt.mu.Lock()
	rt.sweepLocked()
	rt.mu.Unlock()

	if _, ok := rt.handles["old"]; ok {
		t.Fatal("idle-expired handle was not swept")
	}
	if _, ok := rt.handles["fresh"]; !ok {
		t.Fatal("fresh handle was wrongly swept")
	}
	if _, ok := rt.handles["busy"]; !ok {
		t.Fatal("in-use handle was wrongly swept")
	}
}

func TestResumableEvictsOldestOverCap(t *testing.T) {
	rt := newTestResumable(2, 0) // ttl 0 disables sweep, isolate eviction
	rt.handles["a"] = &subSession{lastUsed: time.Now().Add(-3 * time.Minute)}
	rt.handles["b"] = &subSession{lastUsed: time.Now().Add(-2 * time.Minute)}
	rt.handles["c"] = &subSession{lastUsed: time.Now().Add(-1 * time.Minute)}

	rt.mu.Lock()
	rt.evictLocked()
	rt.mu.Unlock()

	if len(rt.handles) != 2 {
		t.Fatalf("len = %d, want 2 after eviction", len(rt.handles))
	}
	if _, ok := rt.handles["a"]; ok {
		t.Fatal("oldest handle 'a' should have been evicted")
	}
}

func TestResumableEvictionSkipsInUse(t *testing.T) {
	rt := newTestResumable(1, 0)
	rt.handles["busy"] = &subSession{lastUsed: time.Now().Add(-5 * time.Minute), inUse: true}
	rt.handles["idle"] = &subSession{lastUsed: time.Now()}

	rt.mu.Lock()
	rt.evictLocked()
	rt.mu.Unlock()

	// cap is 1 but the older one is in use, so only the idle one can go.
	if _, ok := rt.handles["busy"]; !ok {
		t.Fatal("in-use handle must never be evicted")
	}
}

func TestResumableResumeInUseRejected(t *testing.T) {
	rt := newTestResumable(8, time.Minute)
	rt.handles["h"] = &subSession{lastUsed: time.Now(), inUse: true}
	// resolveSession returns before touching toolCtx on the in-use branch.
	if _, err := rt.resolveSession(nil, "h"); err == nil {
		t.Fatal("resuming an in-use session should error")
	}
}
