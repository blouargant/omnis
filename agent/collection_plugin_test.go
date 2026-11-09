package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/paths"
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

// No memories and no instructions: the router gets nothing at all (not even the
// routing note), while an answering root still gets the memory rules block so it
// knows how to save the first memory.
func TestCollectionCtxBlock_NothingToInjectContract(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Cleanup(func() { SetCollectionResolver(nil); SetCollectionRootResolver(nil) })
	SetCollectionResolver(nil)
	SetCollectionRootResolver(nil)

	for _, col := range []string{"", "Empty"} {
		SetCollectionResolver(func(string) string { return col })
		if got := collectionCtxBlock("s1", true); got != "" {
			t.Fatalf("router, collection %q, nothing stored: want empty, got %q", col, got)
		}
		got := collectionCtxBlock("s1", false)
		if !strings.Contains(got, "<collection-memory") || !strings.Contains(got, "memory_save") {
			t.Fatalf("answering root, collection %q: want the rules block, got %q", col, got)
		}
		if strings.Contains(got, "<collection-context") {
			t.Fatalf("answering root, collection %q: no instructions ⇒ no context block, got %q", col, got)
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

func resetCollectionResolvers(t *testing.T) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	SetCollectionResolver(nil)
	SetCollectionRootResolver(nil)
	t.Cleanup(func() { SetCollectionResolver(nil); SetCollectionRootResolver(nil) })
}

func TestMemoryInjectedForGeneralWithNoResolver(t *testing.T) {
	resetCollectionResolvers(t)
	if _, _, err := collmem.In(paths.ConfigWriteDir()).Save("", collmem.Memory{Name: "tabs", Description: "Use tabs", Type: collmem.TypeFeedback, Body: "Indent with tabs."}); err != nil {
		t.Fatal(err)
	}
	out := collectionCtxBlock("s1", false)
	if !strings.Contains(out, "Indent with tabs.") || !strings.Contains(out, `collection="General"`) {
		t.Fatalf("got:\n%s", out)
	}
}

func TestMemoryFollowsTheSessionsCollectionAndRoot(t *testing.T) {
	resetCollectionResolvers(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	SetCollectionResolver(func(string) string { return "Proxy" })
	SetCollectionRootResolver(func(sid string) string {
		if sid == "alice" {
			return rootA
		}
		return rootB
	})
	if _, _, err := collmem.In(rootA).Save("Proxy", collmem.Memory{Name: "a", Description: "d", Type: collmem.TypeFeedback, Body: "ALICE RULE"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(collectionCtxBlock("alice", false), "ALICE RULE") {
		t.Fatal("alice must see her memory")
	}
	if strings.Contains(collectionCtxBlock("bob", false), "ALICE RULE") {
		t.Fatal("bob must not see alice's memory")
	}
}

func TestRouterGetsIndexUnderRoutingNoteAndNothingWhenEmpty(t *testing.T) {
	resetCollectionResolvers(t)
	if collectionCtxBlock("s", true) != "" {
		t.Fatal("router, no instructions, no memories ⇒ empty")
	}
	if _, _, err := collmem.In(paths.ConfigWriteDir()).Save("", collmem.Memory{Name: "tabs", Description: "Use tabs", Type: collmem.TypeFeedback, Body: "BODY"}); err != nil {
		t.Fatal(err)
	}
	out := collectionCtxBlock("s", true)
	if !strings.HasPrefix(out, routerCollectionCtxNote) || !strings.Contains(out, "- tabs — Use tabs") || strings.Contains(out, "BODY") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestInstructionsBlockPrecedesMemoryAndLegacyIsMigrated(t *testing.T) {
	resetCollectionResolvers(t)
	SetCollectionResolver(func(string) string { return "Proxy" })
	if err := collectionctx.WriteInstructions("Proxy", "Answer in French."); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionctx.Dir("Proxy"), "memory.md"), []byte("Old notes."), 0o644); err != nil {
		t.Fatal(err)
	}
	out := collectionCtxBlock("s", false)
	i, m := strings.Index(out, "Answer in French."), strings.Index(out, "<collection-memory")
	if i < 0 || m < 0 || i > m {
		t.Fatalf("order wrong:\n%s", out)
	}
	if strings.Contains(out, "<memory>") {
		t.Error("the legacy <memory> section must be gone")
	}
	if !strings.Contains(out, "- imported-notes — ") {
		t.Error("legacy memory.md must be migrated on first use")
	}
}

// Every root of every session of a collection calls the migration on each model
// call; concurrent first calls must import the legacy notes exactly once and
// never lose them.
func TestLegacyMigrationIsSafeUnderConcurrentCalls(t *testing.T) {
	resetCollectionResolvers(t)
	SetCollectionResolver(func(string) string { return "Proxy" })
	if err := os.MkdirAll(collectionctx.Dir("Proxy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionctx.Dir("Proxy"), "memory.md"), []byte("Old notes."), 0o644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = collectionCtxBlock("s", false) }()
	}
	wg.Wait()
	mems := collmem.In(paths.ConfigWriteDir()).List("Proxy")
	if len(mems) != 1 || mems[0].Name != "imported-notes" || !strings.Contains(mems[0].Body, "Old notes.") {
		t.Fatalf("want exactly the imported notes, got %+v", mems)
	}
	if _, err := os.Stat(filepath.Join(collectionctx.Dir("Proxy"), "memory.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy file should be removed, stat err=%v", err)
	}
}

// A hidden utility session (Settings assistant, drafting assistants, session
// search) gets its collection's instructions but no memory block.
func TestCollectionCtxBlock_HiddenSessionGetsNoMemory(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Cleanup(func() { SetCollectionResolver(nil); SetHiddenSessionResolver(nil) })
	if err := collectionctx.WriteInstructions("Proj", "Project rules."); err != nil {
		t.Fatal(err)
	}
	SetCollectionResolver(func(string) string { return "Proj" })
	SetHiddenSessionResolver(func(id string) bool { return id == "hidden" })

	for _, router := range []bool{false, true} {
		got := collectionCtxBlock("hidden", router)
		if strings.Contains(got, "<collection-memory") {
			t.Fatalf("router=%v: hidden session must get no memory block, got %q", router, got)
		}
		if !strings.Contains(got, "Project rules.") {
			t.Fatalf("router=%v: hidden session keeps the instructions block, got %q", router, got)
		}
	}
	if got := collectionCtxBlock("visible", false); !strings.Contains(got, "<collection-memory") {
		t.Fatalf("visible session should get the memory block, got %q", got)
	}

	// General + hidden: nothing at all.
	SetCollectionResolver(func(string) string { return "" })
	if got := collectionCtxBlock("hidden", false); got != "" {
		t.Fatalf("hidden General session: want empty, got %q", got)
	}
}
