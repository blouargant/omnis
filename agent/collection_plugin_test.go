package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/collectionctx"
)

func TestCollectionResolver_SetGetClear(t *testing.T) {
	defer SetCollectionResolver(nil)

	// No resolver installed ⇒ empty (no injection).
	if got := resolveCollection("s1"); got != "" {
		t.Fatalf("nil resolver should yield empty, got %q", got)
	}

	SetCollectionResolver(func(id string) string {
		if id == "s1" {
			return "Client X"
		}
		return ""
	})
	if got := resolveCollection("s1"); got != "Client X" {
		t.Fatalf("got %q, want Client X", got)
	}
	if got := resolveCollection("s2"); got != "" {
		t.Fatalf("unknown session should be empty, got %q", got)
	}

	SetCollectionResolver(nil)
	if got := resolveCollection("s1"); got != "" {
		t.Fatalf("cleared resolver should yield empty, got %q", got)
	}
}

// The router is the one root whose job is to DECIDE, not to answer, and the
// rendered block tells its reader to treat the collection as "authoritative
// guidance for this workstream" (collectionctx.render). Handed to the router
// unqualified that is an invitation to act on prose it has no tools for — the
// failure mode routerVisibleTools already exists to swallow. So the router gets
// the same context under a routing-scoped framing, and an answering root gets
// the block untouched.
func TestCollectionCtxBlock_RouterGetsRoutingScopedFraming(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	defer SetCollectionResolver(nil)

	if err := collectionctx.WriteInstructions("LiteLLM", "LiteLLM is a proxy for LLM APIs."); err != nil {
		t.Fatal(err)
	}
	SetCollectionResolver(func(string) string { return "LiteLLM" })

	answering := collectionCtxBlock("s1", false)
	if !strings.Contains(answering, "<collection-context") ||
		!strings.Contains(answering, "LiteLLM is a proxy") {
		t.Fatalf("answering root should get the rendered block, got %q", answering)
	}
	if strings.Contains(answering, routerCollectionCtxNote) {
		t.Fatal("answering root must not get the router's routing-scoped note")
	}

	router := collectionCtxBlock("s1", true)
	if !strings.HasPrefix(router, routerCollectionCtxNote) {
		t.Fatalf("router block should lead with the routing-scoped note, got %q", router)
	}
	if !strings.Contains(router, "LiteLLM is a proxy") {
		t.Fatalf("router should still receive the collection's substance, got %q", router)
	}
}

// No collection, or a collection with no prose, injects nothing on either root —
// the no-op contract is unchanged by the router mounting.
func TestCollectionCtxBlock_NothingToInjectIsNoOpOnBothRoots(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	defer SetCollectionResolver(nil)

	for _, forRouter := range []bool{false, true} {
		if got := collectionCtxBlock("s1", forRouter); got != "" {
			t.Fatalf("no resolver (forRouter=%v) should inject nothing, got %q", forRouter, got)
		}
	}
	// Collection named but empty on disk ⇒ still nothing, note included.
	SetCollectionResolver(func(string) string { return "Empty" })
	for _, forRouter := range []bool{false, true} {
		if got := collectionCtxBlock("s1", forRouter); got != "" {
			t.Fatalf("empty collection (forRouter=%v) should inject nothing, got %q", forRouter, got)
		}
	}
}

// With a root resolver installed, the block comes from the session's per-user
// root — never from the shared one.
func TestCollectionCtxBlock_UsesRootResolver(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	alice := filepath.Join(home, "users", "alice")
	if err := collectionctx.In(alice).WriteInstructions("Infra", "alice rules"); err != nil {
		t.Fatal(err)
	}
	if err := collectionctx.WriteInstructions("Infra", "shared rules"); err != nil {
		t.Fatal(err)
	}
	SetCollectionResolver(func(string) string { return "Infra" })
	SetCollectionRootResolver(func(sid string) string {
		if sid == "a" {
			return alice
		}
		return ""
	})
	t.Cleanup(func() { SetCollectionResolver(nil); SetCollectionRootResolver(nil) })
	if got := collectionCtxBlock("a", false); !strings.Contains(got, "alice rules") || strings.Contains(got, "shared rules") {
		t.Fatalf("alice block: %q", got)
	}
	if got := collectionCtxBlock("other", false); !strings.Contains(got, "shared rules") {
		t.Fatalf("fallback to shared root: %q", got)
	}
}
