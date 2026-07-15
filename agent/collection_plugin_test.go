package agent

import "testing"

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
