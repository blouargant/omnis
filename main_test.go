package main

import "testing"

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
	opts, args, err := parseFlags([]string{"--debug", "curate", "--user", "u1"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if !opts.debug {
		t.Fatal("debug = false, want true")
	}
	want := []string{"curate", "--user", "u1"}
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
