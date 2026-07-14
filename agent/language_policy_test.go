package agent

import (
	"strings"
	"testing"
)

// TestLanguagePolicyBlockCoordinator pins the three rules a coordinating root
// must carry: reply in the user's language, delegate in English, and quote the
// user's request verbatim rather than paraphrasing its scope away.
func TestLanguagePolicyBlockCoordinator(t *testing.T) {
	b := languagePolicyBlock(true)

	for _, want := range []string{
		"## Language",
		"language they wrote in",
		"sub-agents in English",
		"never paraphrase the user's request",
		"verbatim, in their language",
		"Do not translate evidence",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("coordinating root instruction missing %q", want)
		}
	}
}

// TestLanguagePolicyBlockLeaderless verifies a leaderless root (the Helper, the
// single-specialist squads) is told to mirror the user and not to translate
// evidence, but is NOT given delegation rules it cannot act on — it has no
// sub-agents.
func TestLanguagePolicyBlockLeaderless(t *testing.T) {
	b := languagePolicyBlock(false)

	for _, want := range []string{
		"## Language",
		"language they wrote in",
		"Do not translate evidence",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("leaderless root instruction missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"sub-agents in English",
		"when you delegate",
		"A sub-agent searching the web",
	} {
		if strings.Contains(b, unwanted) {
			t.Errorf("leaderless root instruction must not mention delegation, got %q", unwanted)
		}
	}
}
