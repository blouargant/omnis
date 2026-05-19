package filter

import "testing"

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"plain":               "plain",
		"\x1b[31mred\x1b[0m":  "red",
		"a\x1b[1;32mb\x1b[mc": "abc",
		"":                    "",
	}
	for in, want := range cases {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCompactPath(t *testing.T) {
	cases := map[string]string{
		"src/foo/bar.go": "foo/bar.go",
		"lib/baz":        "baz",
		"internal/x/y":   "x/y",
		"pkg/p":          "p",
		"vendor/v/v.go":  "v/v.go",
		"cmd/main.go":    "cmd/main.go", // no known prefix
		"":               "",
	}
	for in, want := range cases {
		if got := CompactPath(in); got != want {
			t.Errorf("CompactPath(%q)=%q want %q", in, got, want)
		}
	}
}

func TestFilterHasStream(t *testing.T) {
	def := &Filter{}
	if !def.HasStream("stdout") {
		t.Error("default filter must include stdout")
	}
	if def.HasStream("stderr") {
		t.Error("default filter must NOT include stderr")
	}

	both := &Filter{Streams: []string{"stdout", "stderr"}}
	if !both.HasStream("stdout") || !both.HasStream("stderr") {
		t.Error("explicit streams not honoured")
	}
	if both.HasStream("other") {
		t.Error("unknown stream must not match")
	}
}

func TestRegistryHasAnyFilterAndCommands(t *testing.T) {
	r := NewRegistry(nil)
	if r.HasAnyFilter("kubectl", "") {
		t.Error("empty registry should report no filter")
	}

	r.byKey["kubectl"] = []Filter{{Name: "k-base"}}
	r.byKey["kubectl:get"] = []Filter{{Name: "k-get"}}
	r.byKey["helm"] = []Filter{{Name: "h"}}

	if !r.HasAnyFilter("kubectl", "get") {
		t.Error("kubectl:get must be reported as present")
	}
	if !r.HasAnyFilter("kubectl", "missing") {
		t.Error("fallback to base command should report present")
	}
	if r.HasAnyFilter("docker", "") {
		t.Error("unknown command must not report present")
	}

	cmds := r.Commands()
	if len(cmds) != 2 || cmds[0] != "helm" || cmds[1] != "kubectl" {
		t.Errorf("Commands()=%v want [helm kubectl]", cmds)
	}
}

func TestRegistryShouldInject(t *testing.T) {
	r := NewRegistry(nil)

	// no Inject section → passthrough
	f := &Filter{}
	out, injected := r.ShouldInject(f, []string{"a", "b"})
	if injected || len(out) != 2 {
		t.Errorf("nil Inject must passthrough, got injected=%v out=%v", injected, out)
	}

	// skip_if_present matches → passthrough
	f = &Filter{Inject: &Inject{Args: []string{"-x"}, SkipIfPresent: []string{"-x"}}}
	out, injected = r.ShouldInject(f, []string{"cmd", "-x"})
	if injected {
		t.Errorf("skip_if_present must inhibit injection, got %v", out)
	}

	// inject after subcommand (no `--`)
	f = &Filter{Inject: &Inject{Args: []string{"--inject"}}}
	out, injected = r.ShouldInject(f, []string{"get", "pods"})
	if !injected || len(out) != 3 || out[1] != "--inject" {
		t.Errorf("inject after subcommand failed: %v", out)
	}

	// inject before `--`
	f = &Filter{Inject: &Inject{Args: []string{"--inject"}}}
	out, _ = r.ShouldInject(f, []string{"get", "--", "x"})
	if out[1] != "--inject" || out[2] != "--" {
		t.Errorf("inject must precede --, got %v", out)
	}

	// inject into empty args
	f = &Filter{Inject: &Inject{Args: []string{"--only"}}}
	out, _ = r.ShouldInject(f, nil)
	if len(out) != 1 || out[0] != "--only" {
		t.Errorf("inject into empty args failed: %v", out)
	}
}
