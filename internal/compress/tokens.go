package compress

import (
	"encoding/json"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	"google.golang.org/genai"
)

// Token estimation uses the cl100k_base encoding shared by GPT-4 / GPT-4o /
// GPT-3.5-turbo. It is highly accurate for those models and a conservative
// over-estimate for Anthropic Claude (Claude's own tokenizer typically
// produces 5–15% fewer tokens for the same prompt). Over-counting is
// preferable here: it just means compression fires slightly earlier,
// keeping headroom for the model's response.

var (
	encOnce sync.Once
	encoder *tiktoken.Tiktoken
	encErr  error
)

func enc() (*tiktoken.Tiktoken, error) {
	encOnce.Do(func() {
		encoder, encErr = tiktoken.GetEncoding("cl100k_base")
	})
	return encoder, encErr
}

// CountText returns the tiktoken count of s, or a 4-chars-per-token
// fallback if the encoder is unavailable.
func CountText(s string) int {
	if s == "" {
		return 0
	}
	e, err := enc()
	if err != nil || e == nil {
		return (len(s) + 3) / 4
	}
	return len(e.Encode(s, nil, nil))
}

// CountContents counts tokens across an entire []*genai.Content, including
// text parts, function calls (name + JSON args) and function responses
// (JSON payload). Role names contribute a small constant per turn to
// reflect the per-message overhead the providers add when serialising.
func CountContents(contents []*genai.Content) int {
	total := 0
	for _, c := range contents {
		if c == nil {
			continue
		}
		total += 4 // per-turn overhead (role + delimiters)
		total += CountText(c.Role)
		for _, p := range c.Parts {
			total += CountPart(p)
		}
	}
	return total
}

// CountPart counts a single Part: text, function call, or function response.
func CountPart(p *genai.Part) int {
	if p == nil {
		return 0
	}
	n := 0
	if p.Text != "" {
		n += CountText(p.Text)
	}
	if p.FunctionCall != nil {
		n += CountText(p.FunctionCall.Name)
		if len(p.FunctionCall.Args) > 0 {
			if b, err := json.Marshal(p.FunctionCall.Args); err == nil {
				n += CountText(string(b))
			}
		}
	}
	if p.FunctionResponse != nil {
		n += CountText(p.FunctionResponse.Name)
		if len(p.FunctionResponse.Response) > 0 {
			if b, err := json.Marshal(p.FunctionResponse.Response); err == nil {
				n += CountText(string(b))
			}
		}
	}
	return n
}
