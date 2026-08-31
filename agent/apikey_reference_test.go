package agent

import (
	"os"
	"testing"
)

// These tests pin down the contract documented in CLAUDE.md's "Configuration
// precedence" section: api_key/base_url values are resolved as environment
// variable NAMES first, falling back to the literal value.
//
// The defect fixed here: when a value is shaped like an env-var reference
// (e.g. "SERPER_KEY") but that variable is NOT set in the process
// environment, resolveAPIKeyReference used to return the NAME ITSELF — a
// non-empty, garbage "credential" indistinguishable from a real key to every
// caller that only checks `apiKey == ""` to decide whether something is
// configured. That silently broke web search in production: an unexported
// SERPER_KEY resolved to the literal 10-byte string "SERPER_KEY", which is
// non-empty, so fstools.NewSerperTools registered a WebSearch tool that beat
// the working DuckDuckGo fallback and then failed every search with a 401 —
// with nothing logged anywhere.

func TestResolveAPIKeyReferenceNameShapedUnsetReturnsEmpty(t *testing.T) {
	// A unique, never-set var name — confirm it's genuinely absent rather
	// than assuming it.
	const varName = "OMNIS_TEST_UNSET_APIKEY_VAR"
	if _, ok := os.LookupEnv(varName); ok {
		t.Fatalf("precondition failed: %s is set in the test environment", varName)
	}

	got := resolveAPIKeyReference(varName, "serper_key")
	if got != "" {
		t.Fatalf("resolveAPIKeyReference(unset name-shaped ref) = %q, want empty string (treat as unconfigured)", got)
	}
}

func TestResolveAPIKeyReferenceNameShapedSetReturnsEnvValue(t *testing.T) {
	t.Setenv("OMNIS_TEST_SET_APIKEY_VAR", "the-real-secret-value")

	got := resolveAPIKeyReference("OMNIS_TEST_SET_APIKEY_VAR", "serper_key")
	if got != "the-real-secret-value" {
		t.Fatalf("resolveAPIKeyReference(set name-shaped ref) = %q, want %q", got, "the-real-secret-value")
	}
}

func TestResolveAPIKeyReferenceLiteralKeyPassesThroughUnchanged(t *testing.T) {
	// Not name-shaped (mixed case + dashes, like a real API key) — must be
	// returned verbatim regardless of the environment, exactly as before.
	const literal = "sk-abcDEF123-not-an-env-var-name"
	got := resolveAPIKeyReference(literal, "provider api_key")
	if got != literal {
		t.Fatalf("resolveAPIKeyReference(literal) = %q, want unchanged %q", got, literal)
	}
}

func TestResolveAPIKeyReferenceEmptyInputReturnsEmpty(t *testing.T) {
	got := resolveAPIKeyReference("", "serper_key")
	if got != "" {
		t.Fatalf("resolveAPIKeyReference(\"\") = %q, want empty string", got)
	}
}

func TestLooksLikeEnvVarName(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"SERPER_KEY", true},
		{"OPENAI_API_KEY", true},
		{"A", true},
		{"ABC123_DEF", true},
		{"sk-abcDEF123", false},   // mixed case + dash: a real key shape
		{"sk_live_abc123", false}, // lowercase prefix: a real key shape
		{"", false},
		{"1ABC", false},    // must start with a letter
		{"ABC-DEF", false}, // dash is not a valid env-var name character
	}
	for _, c := range cases {
		if got := looksLikeEnvVarName(c.v); got != c.want {
			t.Errorf("looksLikeEnvVarName(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}
