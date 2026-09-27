package llm

import (
	"context"
	"iter"
	"log"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// toolTextMarkers are the tag openings a model's native tool-call syntax uses
// when it leaks into the text channel instead of being parsed into a function
// call by the serving stack (Qwen's XML format, emitted by vLLM's qwen3_coder
// parser family). Observed live on Scaleway's qwen3.6-35b-a3b: an
// AskUserQuestion call came back as the text
// "<parameter=kind>\ntext\n</parameter>\n</function>\n</tool_call>", the turn
// ended with no tool call, and the user was shown the markup.
var toolTextMarkers = []string{
	"<tool_call", "</tool_call",
	"<function=", "</function",
	"<parameter=", "</parameter",
}

// ToolTextNudge is appended as a user turn when the guard retries a response
// whose tool call leaked as text. It names what went wrong, because a vague
// "try again" reproduces the failure: the model believes it DID call the tool.
const ToolTextNudge = "Your previous reply wrote a tool call as plain text (markup such as <tool_call>, <function=…> or <parameter=…>) instead of calling the tool. Make the call now through the function-calling interface. Do not write any tool-call markup in your reply."

// ToolTextFallback replaces a response whose tool call leaked as text twice.
// Never the raw markup, and never an empty turn.
const ToolTextFallback = "⚠️ The model returned a malformed tool call twice, so no tool ran. Please rephrase your request or try again."

// GuardToolText wraps an LLM so a tool call the model writes as TEXT — instead
// of emitting a real function call — is caught host-side: the markup is never
// exposed, the request is retried once with ToolTextNudge, and a second leak
// ends with ToolTextFallback. It is inert for requests that declare no tools
// (a model explaining the format is not a leak) and never alters ordinary
// text, including a "<" that does not open tool markup.
//
// omnis already salvaged written tool calls for the router hop only
// (Manager.ResolveRouterHopText); this covers every agent that calls tools,
// squad roots and sub-agents alike, whatever the provider.
func GuardToolText(inner model.LLM) model.LLM {
	if inner == nil {
		return nil
	}
	if _, already := inner.(*toolTextGuard); already {
		return inner
	}
	return &toolTextGuard{inner: inner}
}

type toolTextGuard struct {
	inner model.LLM
}

func (g *toolTextGuard) Name() string { return g.inner.Name() }

func (g *toolTextGuard) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if !declaresTools(req) {
		return g.inner.GenerateContent(ctx, req, stream)
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		leaked, stopped := g.attempt(ctx, req, stream, yield)
		if !leaked || stopped {
			return
		}
		log.Printf("llm: %s wrote a tool call as text; retrying once", g.inner.Name())
		retry := *req
		retry.Contents = append(append([]*genai.Content(nil), req.Contents...), &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{{Text: ToolTextNudge}},
		})
		leaked, stopped = g.attempt(ctx, &retry, stream, yield)
		if !leaked || stopped {
			return
		}
		log.Printf("llm: %s wrote a tool call as text again; giving up", g.inner.Name())
		yield(&model.LLMResponse{
			TurnComplete: true,
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: ToolTextFallback}}},
		}, nil)
	}
}

// attempt relays one inner call, withholding any tool markup. It reports
// whether the response leaked a tool call as text (nothing of the leak — and
// no final response — is yielded then), and whether the consumer stopped.
func (g *toolTextGuard) attempt(ctx context.Context, req *model.LLMRequest, stream bool, yield func(*model.LLMResponse, error) bool) (leaked, stopped bool) {
	var held string
	for resp, err := range g.inner.GenerateContent(ctx, req, stream) {
		if err != nil {
			return false, !yield(nil, err)
		}
		if resp == nil || resp.Content == nil {
			if !yield(resp, nil) {
				return false, true
			}
			continue
		}
		if resp.Partial {
			out, leak := filterPartial(resp, &held)
			if leak {
				return true, false
			}
			if out != nil && !yield(out, nil) {
				return false, true
			}
			continue
		}
		// Final (or non-streamed) response: the authoritative check.
		if !hasFunctionCall(resp.Content) && containsToolMarkup(contentText(resp.Content)) {
			return true, false
		}
		if held != "" {
			if !yield(textPartial(resp.Content.Role, held), nil) {
				return false, true
			}
			held = ""
		}
		if !yield(resp, nil) {
			return false, true
		}
	}
	return false, false
}

// filterPartial passes a streamed chunk's text through, holding back a "<"
// until it is known whether it opens tool markup. It returns the (possibly
// trimmed) partial to yield — nil when there is nothing to show yet — and
// whether the chunk revealed leaked markup.
func filterPartial(resp *model.LLMResponse, held *string) (*model.LLMResponse, bool) {
	var parts []*genai.Part
	for _, p := range resp.Content.Parts {
		if p == nil || p.Text == "" || p.Thought {
			parts = append(parts, p)
			continue
		}
		shown, leak := scanText(held, p.Text)
		if leak {
			return nil, true
		}
		if shown != "" {
			parts = append(parts, &genai.Part{Text: shown})
		}
	}
	if len(parts) == 0 {
		return nil, false
	}
	out := *resp
	c := *resp.Content
	c.Parts = parts
	out.Content = &c
	return &out, false
}

// scanText appends chunk to the held-back text and returns what can be shown
// now. Text before a "<" is shown; from a "<" on, the text is held until it
// either opens tool markup (leak) or provably does not (shown).
func scanText(held *string, chunk string) (string, bool) {
	s := *held + chunk
	*held = ""
	var shown strings.Builder
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			shown.WriteString(s)
			return shown.String(), false
		}
		shown.WriteString(s[:i])
		switch classifyMarkup(s[i:]) {
		case markupLeak:
			return shown.String(), true
		case markupMaybe:
			*held = s[i:]
			return shown.String(), false
		default:
			shown.WriteByte('<')
			s = s[i+1:]
		}
	}
}

type markupClass int

const (
	markupNone markupClass = iota
	markupMaybe
	markupLeak
)

// classifyMarkup says whether text starting with "<" opens tool markup, might
// still (it is a prefix of a marker), or cannot.
func classifyMarkup(s string) markupClass {
	class := markupNone
	for _, m := range toolTextMarkers {
		if strings.HasPrefix(s, m) {
			return markupLeak
		}
		if strings.HasPrefix(m, s) {
			class = markupMaybe
		}
	}
	return class
}

func containsToolMarkup(s string) bool {
	for _, m := range toolTextMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func declaresTools(req *model.LLMRequest) bool {
	if req == nil {
		return false
	}
	if len(req.Tools) > 0 {
		return true
	}
	return req.Config != nil && len(req.Config.Tools) > 0
}

func hasFunctionCall(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p != nil && p.FunctionCall != nil {
			return true
		}
	}
	return false
}

func contentText(c *genai.Content) string {
	var b strings.Builder
	for _, p := range c.Parts {
		if p != nil && !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func textPartial(role, text string) *model.LLMResponse {
	if role == "" {
		role = "model"
	}
	return &model.LLMResponse{Partial: true, Content: &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}}}
}
