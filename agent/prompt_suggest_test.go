package agent

import (
	"context"
	"strings"
	"testing"
)

func TestCleanSuggestion(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "Can you add tests for it?", "Can you add tests for it?"},
		{"trims", "  Show me the diff \n", "Show me the diff"},
		{"double quotes", `"Apply the fix"`, "Apply the fix"},
		{"french quotes", "« Applique la correction »", "Applique la correction"},
		{"backticks", "`Run the tests`", "Run the tests"},
		{"label", "Suggestion: Explain step 2", "Explain step 2"},
		{"label user", "User: What about Windows?", "What about Windows?"},
		{"label user hyphen spaced", "User - What about Windows?", "What about Windows?"},
		{"hyphenated compound not a label", "User-facing bug is critical", "User-facing bug is critical"},
		{"first line only", "Deploy it\nor maybe not", "Deploy it"},
		{"leading blank lines", "\n\n  Next step?\n", "Next step?"},
		{"none", "NONE", ""},
		{"none lower punct", "none.", ""},
		{"empty", "   ", ""},
		{"slash command", "/compress", ""},
		{"shell escape", "!rm -rf build", ""},
		{"memory write", "# remember this", ""},
		{"too long", strings.Repeat("word ", 40), ""},
	}
	for _, c := range cases {
		if got := cleanSuggestion(c.in); got != c.want {
			t.Errorf("%s: cleanSuggestion(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func requestText(t *testing.T, turns []Exchange) string {
	t.Helper()
	req := buildSuggestRequest(turns)
	if req.Config == nil || req.Config.SystemInstruction == nil {
		t.Fatal("missing system instruction")
	}
	if len(req.Contents) != 1 || req.Contents[0].Role != "user" {
		t.Fatalf("want one user content, got %+v", req.Contents)
	}
	return req.Contents[0].Parts[0].Text
}

func TestBuildSuggestRequestKeepsLastThreeTurns(t *testing.T) {
	turns := []Exchange{
		{User: "q1", Assistant: "a1"},
		{User: "q2", Assistant: "a2"},
		{User: "q3", Assistant: "a3"},
		{User: "q4", Assistant: "a4"},
	}
	txt := requestText(t, turns)
	if strings.Contains(txt, "q1") || strings.Contains(txt, "a1") {
		t.Errorf("oldest turn should be dropped:\n%s", txt)
	}
	for _, s := range []string{"USER: q2", "ASSISTANT: a2", "USER: q4", "ASSISTANT: a4"} {
		if !strings.Contains(txt, s) {
			t.Errorf("missing %q in:\n%s", s, txt)
		}
	}
}

func TestBuildSuggestRequestCapsKeepingTail(t *testing.T) {
	long := strings.Repeat("x", suggestTranscriptCap*2)
	txt := requestText(t, []Exchange{{User: "START", Assistant: long + "THE-END"}})
	if !strings.Contains(txt, "THE-END") {
		t.Error("tail must be kept")
	}
	if strings.Contains(txt, "START") {
		t.Error("head should be cut")
	}
	if n := len([]rune(txt)); n > suggestTranscriptCap+200 {
		t.Errorf("transcript not capped: %d runes", n)
	}
}

func TestSuggestNextPromptNilManager(t *testing.T) {
	var m *Manager
	if s, ok := m.SuggestNextPrompt(context.Background(), "x", []Exchange{{User: "a"}}); s != "" || ok {
		t.Errorf("nil manager: got (%q,%v), want (\"\",false)", s, ok)
	}
}

// TestSuggestNextPromptDoesNotPinUnpinnedSession guards against a refcount
// leak: SuggestNextPrompt is a read-only path with no matching Release, so
// looking up the session with Manager.Lookup (which auto-pins an unpinned
// session to the current generation) would pin forever — reachable when the
// session was archived/deleted between the HTTP handler's registry snapshot
// and this call. It must use the non-pinning Peek instead, falling back to
// Current() exactly as before.
//
// No model provider is configured here (env cleared below), so the eval-model
// build fails fast and synchronously — no network call — and the method
// returns ok=false; what this test asserts is that the session was never
// pinned as a side effect of the lookup.
func TestSuggestNextPromptDoesNotPinUnpinnedSession(t *testing.T) {
	t.Setenv("OMNIS_PROVIDER", "")
	t.Setenv("OMNIS_MODEL", "")
	t.Setenv("OMNIS_BASE_URL", "")
	t.Setenv("OMNIS_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	inst := newTestInstance(1)
	m := NewManager(nil, inst)

	if _, ok := m.SuggestNextPrompt(context.Background(), "leaky-session", []Exchange{{User: "hi"}}); ok {
		t.Fatalf("expected ok=false with no model provider configured")
	}
	if gen := m.PinnedGeneration("leaky-session"); gen != 0 {
		t.Fatalf("SuggestNextPrompt must not pin an unpinned session: PinnedGeneration = %d, want 0 (refcount leak)", gen)
	}
	if gens := m.Generations(); gens[1] != 0 {
		t.Fatalf("SuggestNextPrompt must not bump the generation refcount: gens[1] = %d, want 0", gens[1])
	}
}
