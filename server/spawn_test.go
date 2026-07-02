package main

import (
	"strings"
	"testing"
)

func TestSpawnLabel(t *testing.T) {
	if got := spawnLabel("  nightly audit  ", "petname-1"); got != "nightly audit" {
		t.Fatalf("spawnLabel(name) = %q, want trimmed name", got)
	}
	if got := spawnLabel("", "petname-1"); got != "petname-1" {
		t.Fatalf("spawnLabel(empty) = %q, want the id fallback", got)
	}
	if got := spawnLabel("   ", "petname-1"); got != "petname-1" {
		t.Fatalf("spawnLabel(blank) = %q, want the id fallback", got)
	}
}

func TestFormatSpawnResultNotice(t *testing.T) {
	notice := formatSpawnResultNotice("test", "how to enable prompt caching", "  set prompt_cache: true  ")
	// Names the spawned session, carries the task + trimmed result, and tells the
	// leader not to reply back (keeps delivery one-way).
	for _, want := range []string{`"test"`, "how to enable prompt caching", "set prompt_cache: true", "do not need to reply"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice missing %q:\n%s", want, notice)
		}
	}
	// A blank label falls back to a generic phrase (never an empty %q).
	if n := formatSpawnResultNotice("", "", "done"); !strings.Contains(n, "a spawned session") {
		t.Fatalf("blank-label notice = %q, want generic label", n)
	}
}
