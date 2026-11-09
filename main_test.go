package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseFlagsDebugShort(t *testing.T) {
	opts, args, err := parseFlags([]string{"-d", "tui"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if !opts.debug {
		t.Fatal("debug = false, want true")
	}
	if len(args) != 1 || args[0] != "tui" {
		t.Fatalf("args = %#v, want [tui]", args)
	}
}

func TestParseFlagsLongFlagsKeepSubcommandArgs(t *testing.T) {
	opts, args, err := parseFlags([]string{"--debug", "reindex-docs", "--user", "u1"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if !opts.debug {
		t.Fatal("debug = false, want true")
	}
	want := []string{"reindex-docs", "--user", "u1"}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i, v := range want {
		if args[i] != v {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], v)
		}
	}
}

func TestParseFlagsBarePromptArgs(t *testing.T) {
	// Bare-prompt invocation: flags first, then arbitrary text. The text
	// must come through untouched so runCLI can join it as the prompt.
	_, args, err := parseFlags([]string{"summarize", "the", "main.go", "file"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	want := []string{"summarize", "the", "main.go", "file"}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i, v := range want {
		if args[i] != v {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], v)
		}
	}
}

// -softskills and -curator-enabled went away with soft-skills but stay
// registered as no-ops, so existing command lines keep parsing.
func TestRemovedFlagsAreAccepted(t *testing.T) {
	_, args, err := parseFlags([]string{"-softskills", "/x", "-curator-enabled", "true", "tui"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if len(args) != 1 || args[0] != "tui" {
		t.Fatalf("args = %#v, want [tui]", args)
	}
}

// "curate" and "reindex-precedents" were removed with soft-skills. They must
// fail fast, before any config or model work, rather than being read as a
// prompt and spending a real model call.
func TestRemovedSubcommandsFailFast(t *testing.T) {
	t.Setenv("OMNIS_CONFIG_PATH", "/nonexistent/agents.json")
	for _, args := range [][]string{
		{"curate", "--user", "u", "--session", "s"},
		{"reindex-precedents"},
		{"CURATE"},
	} {
		err := run(context.Background(), options{configPath: "/nonexistent/agents.json"}, args)
		if err == nil {
			t.Fatalf("run(%v) = nil, want an error", args)
		}
		want := "\"omnis " + strings.ToLower(args[0]) + "\" was removed with soft-skills"
		if err.Error() != want {
			t.Fatalf("run(%v) error = %q, want %q", args, err.Error(), want)
		}
	}
}
