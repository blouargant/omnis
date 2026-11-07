package compress

import "strings"

// overflowMarkers are the phrasings providers and gateways use when a
// request does not fit the model's context window (OpenAI, Anthropic, vLLM,
// LiteLLM, Gemini, Mistral). Matched case-insensitively.
var overflowMarkers = []string{
	"context_length_exceeded",
	"contextwindowexceeded",
	"maximum context length",
	"context length",
	"context window",
	"prompt is too long",
	"input is too long",
	"too many input tokens",
	"input tokens exceed",
	"exceeds the maximum number of tokens",
	"reduce the length of the messages",
}

// IsContextOverflow reports whether err says the request exceeded the
// model's context window.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range overflowMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
