package compress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// A Pass is a single compression step. It receives the full conversation
// content and returns a (possibly shorter) replacement. Passes must NEVER
// touch the last `keepRecent` turns — that is the model's working window.
type Pass func(contents []*genai.Content, keepRecent int) []*genai.Content

// recentBoundary returns the index from which the "tail" begins. Anything
// at index >= boundary must be preserved verbatim.
func recentBoundary(contents []*genai.Content, keepRecent int) int {
	if keepRecent <= 0 || keepRecent >= len(contents) {
		return len(contents)
	}
	return len(contents) - keepRecent
}

// PassDedupeToolCalls collapses repeated tool invocations with identical
// (name, args) pairs. The most recent call's response is kept; earlier
// responses are replaced with a one-line marker. The original FunctionCall
// parts are always preserved so the model can still see what it tried.
func PassDedupeToolCalls(contents []*genai.Content, keepRecent int) []*genai.Content {
	boundary := recentBoundary(contents, keepRecent)
	// First pass: find the LAST occurrence of each (name, args-hash) call.
	type key struct{ name, hash string }
	lastIdx := map[key]int{} // turn index of most recent call
	for i := 0; i < boundary; i++ {
		c := contents[i]
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p == nil || p.FunctionCall == nil {
				continue
			}
			k := key{name: p.FunctionCall.Name, hash: hashArgs(p.FunctionCall.Args)}
			lastIdx[k] = i
		}
	}
	// Second pass: replace responses for non-last calls.
	for i := 0; i < boundary; i++ {
		c := contents[i]
		if c == nil {
			continue
		}
		// Build a quick lookup of which (name, hash) the previous turn
		// called, so we can pair the response with its call.
		var prevCalls []key
		if i > 0 && contents[i-1] != nil {
			for _, p := range contents[i-1].Parts {
				if p != nil && p.FunctionCall != nil {
					prevCalls = append(prevCalls, key{p.FunctionCall.Name, hashArgs(p.FunctionCall.Args)})
				}
			}
		}
		for j, p := range c.Parts {
			if p == nil || p.FunctionResponse == nil {
				continue
			}
			// Match by tool name against the previous turn's calls.
			var matched *key
			for k := range prevCalls {
				if prevCalls[k].name == p.FunctionResponse.Name {
					m := prevCalls[k]
					matched = &m
					break
				}
			}
			if matched == nil {
				continue
			}
			if last, ok := lastIdx[*matched]; ok && last != i-1 {
				c.Parts[j] = &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name:     p.FunctionResponse.Name,
						ID:       p.FunctionResponse.ID,
						Response: map[string]any{"result": "[deduped: superseded by a later call with identical args]"},
					},
				}
			}
		}
	}
	return contents
}

// PassTruncateToolResults shortens any FunctionResponse whose JSON payload
// exceeds maxBytes. The replacement keeps a head/tail snippet plus a
// marker noting the original size.
func PassTruncateToolResults(maxBytes int) Pass {
	if maxBytes <= 0 {
		maxBytes = 4096
	}
	return func(contents []*genai.Content, keepRecent int) []*genai.Content {
		boundary := recentBoundary(contents, keepRecent)
		for i := 0; i < boundary; i++ {
			c := contents[i]
			if c == nil {
				continue
			}
			for j, p := range c.Parts {
				if p == nil || p.FunctionResponse == nil {
					continue
				}
				payload := renderResponse(p.FunctionResponse.Response)
				if len(payload) <= maxBytes {
					continue
				}
				head := payload[:maxBytes/2]
				tail := payload[len(payload)-maxBytes/2:]
				c.Parts[j] = &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name: p.FunctionResponse.Name,
						ID:   p.FunctionResponse.ID,
						Response: map[string]any{
							"truncated":      true,
							"original_bytes": len(payload),
							"head":           head,
							"tail":           tail,
						},
					},
				}
			}
		}
		return contents
	}
}

// PassDropUnusedSkills removes load_skill / load_skill_resource tool call
// turns (and their paired responses) when the loaded skill name is never
// mentioned again in any later turn — neither as a tool argument, tool
// name, nor as plain text. The model can re-issue load_skill at any time
// if it actually needs the skill.
func PassDropUnusedSkills(contents []*genai.Content, keepRecent int) []*genai.Content {
	boundary := recentBoundary(contents, keepRecent)
	if boundary < 2 {
		return contents
	}
	// Identify load_skill calls and the skill name they loaded.
	type skillCall struct {
		callTurn int    // index of model turn containing FunctionCall
		respTurn int    // index of paired user turn containing FunctionResponse (-1 if none)
		name     string // skill name argument
	}
	var calls []skillCall
	for i := 0; i < boundary; i++ {
		c := contents[i]
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p == nil || p.FunctionCall == nil {
				continue
			}
			if p.FunctionCall.Name != "load_skill" {
				continue
			}
			name, _ := p.FunctionCall.Args["name"].(string)
			if name == "" {
				continue
			}
			respTurn := -1
			if i+1 < boundary && contents[i+1] != nil {
				for _, q := range contents[i+1].Parts {
					if q != nil && q.FunctionResponse != nil && q.FunctionResponse.Name == "load_skill" {
						respTurn = i + 1
						break
					}
				}
			}
			calls = append(calls, skillCall{callTurn: i, respTurn: respTurn, name: name})
		}
	}
	if len(calls) == 0 {
		return contents
	}
	// For each call, scan everything *after* the response turn (or call
	// turn if no response) for any later mention of the skill name.
	used := map[int]bool{} // call index -> still used?
	for k, sc := range calls {
		start := sc.callTurn + 1
		if sc.respTurn >= 0 {
			start = sc.respTurn + 1
		}
		needle := strings.ToLower(sc.name)
		for i := start; i < len(contents); i++ {
			if mentionsSkill(contents[i], needle) {
				used[k] = true
				break
			}
		}
	}
	// Build a set of turn indices to drop entirely.
	drop := map[int]bool{}
	for k, sc := range calls {
		if used[k] {
			continue
		}
		// Only drop the call turn if it ONLY contained that load_skill
		// call (no other parts). Same for the response turn. This is
		// conservative — partial-turn surgery would risk corrupting
		// provider-specific tool-call/response pairing.
		if isSoloLoadSkill(contents[sc.callTurn], sc.name, true) {
			drop[sc.callTurn] = true
		}
		if sc.respTurn >= 0 && isSoloLoadSkill(contents[sc.respTurn], sc.name, false) {
			drop[sc.respTurn] = true
		}
	}
	if len(drop) == 0 {
		return contents
	}
	out := make([]*genai.Content, 0, len(contents)-len(drop))
	for i, c := range contents {
		if drop[i] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// PassSummarizeMiddle is the heavyweight pass: it preserves the first
// `keepHead` turns and the last `keepRecent` turns, and asks `summarise`
// to produce a single-paragraph summary of everything in between. The
// summary is inserted as one synthetic user turn.
//
// If summarise is nil, the middle is replaced by an extractive bullet
// list of file paths and tool names found in the dropped turns.
func PassSummarizeMiddle(keepHead int, summarise func(text string) (string, error)) Pass {
	if keepHead < 0 {
		keepHead = 0
	}
	return func(contents []*genai.Content, keepRecent int) []*genai.Content {
		if keepHead+keepRecent >= len(contents) {
			return contents
		}
		head := contents[:keepHead]
		middle := contents[keepHead : len(contents)-keepRecent]
		tail := contents[len(contents)-keepRecent:]
		if len(middle) == 0 {
			return contents
		}
		var summary string
		if summarise != nil {
			s, err := summarise(renderTranscript(middle))
			if err == nil && strings.TrimSpace(s) != "" {
				summary = s
			}
		}
		if summary == "" {
			summary = extractiveSummary(middle)
		}
		synthetic := &genai.Content{
			Role: "user",
			Parts: []*genai.Part{{Text: "## Compressed history (auto-generated)\n" + summary}},
		}
		out := make([]*genai.Content, 0, len(head)+1+len(tail))
		out = append(out, head...)
		out = append(out, synthetic)
		out = append(out, tail...)
		return out
	}
}

// --- helpers ---

func hashArgs(args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func renderResponse(r map[string]any) string {
	if r == nil {
		return ""
	}
	for _, k := range []string{"result", "output", "content"} {
		if v, ok := r[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

func mentionsSkill(c *genai.Content, needle string) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		if p.Text != "" && strings.Contains(strings.ToLower(p.Text), needle) {
			return true
		}
		if p.FunctionCall != nil {
			if strings.Contains(strings.ToLower(p.FunctionCall.Name), needle) {
				return true
			}
			if b, err := json.Marshal(p.FunctionCall.Args); err == nil {
				if strings.Contains(strings.ToLower(string(b)), needle) {
					return true
				}
			}
		}
		if p.FunctionResponse != nil {
			if b, err := json.Marshal(p.FunctionResponse.Response); err == nil {
				if strings.Contains(strings.ToLower(string(b)), needle) {
					return true
				}
			}
		}
	}
	return false
}

// isSoloLoadSkill reports whether c contains only a single load_skill
// call (or response) for `name` and nothing else worth preserving.
func isSoloLoadSkill(c *genai.Content, name string, isCall bool) bool {
	if c == nil || len(c.Parts) != 1 {
		return false
	}
	p := c.Parts[0]
	if p == nil {
		return false
	}
	if isCall {
		if p.FunctionCall == nil || p.FunctionCall.Name != "load_skill" {
			return false
		}
		got, _ := p.FunctionCall.Args["name"].(string)
		return got == name
	}
	return p.FunctionResponse != nil && p.FunctionResponse.Name == "load_skill"
}

// renderTranscript is a compact textual rendering of contents suitable for
// feeding to a summariser LLM.
func renderTranscript(contents []*genai.Content) string {
	var b strings.Builder
	for _, c := range contents {
		if c == nil {
			continue
		}
		fmt.Fprintf(&b, "\n[%s]\n", c.Role)
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" {
				b.WriteString(p.Text)
				b.WriteByte('\n')
			}
			if p.FunctionCall != nil {
				args, _ := json.Marshal(p.FunctionCall.Args)
				fmt.Fprintf(&b, "<tool_call name=%q args=%s/>\n", p.FunctionCall.Name, string(args))
			}
			if p.FunctionResponse != nil {
				fmt.Fprintf(&b, "<tool_result name=%q>%s</tool_result>\n", p.FunctionResponse.Name, renderResponse(p.FunctionResponse.Response))
			}
		}
	}
	return b.String()
}

// extractiveSummary is the offline fallback when no summariser LLM is
// configured. It lists the unique tool names invoked and any file-path
// looking tokens it spots in the transcript.
func extractiveSummary(contents []*genai.Content) string {
	tools := map[string]int{}
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p == nil || p.FunctionCall == nil {
				continue
			}
			tools[p.FunctionCall.Name]++
		}
	}
	var b strings.Builder
	b.WriteString("Earlier turns (summarised offline):\n")
	if len(tools) == 0 {
		b.WriteString("- (text-only turns, no tool calls recorded)\n")
	} else {
		b.WriteString("- Tools invoked: ")
		first := true
		for name, n := range tools {
			if !first {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s×%d", name, n)
			first = false
		}
		b.WriteByte('\n')
	}
	return b.String()
}
