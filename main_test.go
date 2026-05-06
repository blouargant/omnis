package main

import "testing"

func TestParseFlagsDebugShort(t *testing.T) {
	opts, args, err := parseFlags([]string{"-d", "console"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if !opts.debug {
		t.Fatal("debug = false, want true")
	}
	if len(args) != 1 || args[0] != "console" {
		t.Fatalf("args = %#v, want [console]", args)
	}
}

func TestParseFlagsDebugLongKeepsLauncherArgs(t *testing.T) {
	opts, args, err := parseFlags([]string{"--debug", "--skills", "custom-skills", "web", "webui"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if !opts.debug {
		t.Fatal("debug = false, want true")
	}
	if opts.skillsDir != "custom-skills" {
		t.Fatalf("skillsDir = %q, want custom-skills", opts.skillsDir)
	}
	if len(args) != 2 || args[0] != "web" || args[1] != "webui" {
		t.Fatalf("args = %#v, want [web webui]", args)
	}
}
