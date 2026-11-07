package cli

import (
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/llm"
)

func TestPrematureStop(t *testing.T) {
	cases := map[string]bool{
		"Done. All 91 tests pass and the work is committed on feature/x.":                                        false,
		"Working on the parser.\n\n" + llm.TruncationNoticePrefix + " after 32000 tokens. Raise …]":              true,
		"<analysis>\nLet me chronologically analyze the conversation.\n</analysis>\n<summary>\n1. …\n</summary>": true,
		"Next I will add tests.\n</summary>":                                                                     true,
		// a marker early in a turn that then went on is not a stop
		llm.TruncationNoticePrefix + "]\n" + strings.Repeat("then I finished the work. ", 300): false,
		"The <summary> element of the HTML page now renders the title.":                        false,
	}
	for text, want := range cases {
		if got := prematureStop(text) != ""; got != want {
			t.Errorf("prematureStop(%.60q) = %v, want %v", text, got, want)
		}
	}
	if !strings.Contains(continuationPrompt("x"), "Do not summarise") {
		t.Fatal("the continuation must tell the model not to summarise again")
	}
}
