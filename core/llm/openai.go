// OpenAI Chat Completions adapter implementing google.golang.org/adk/model.LLM.
// Works against api.openai.com and any OpenAI-compatible endpoint via
// `baseURL` (Ollama, vLLM, Together, Groq, OpenRouter, Mistral, etc.).
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"sort"
	"strings"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

const defaultOpenAIBase = "https://api.openai.com/v1"

type openAI struct {
	model   string
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewOpenAI returns an LLM. baseURL may be empty for the official endpoint.
func NewOpenAI(modelName, apiKey, baseURL string) model.LLM {
	if baseURL == "" {
		baseURL = defaultOpenAIBase
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &openAI{
		model:   modelName,
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *openAI) Name() string { return o.model }

// ── Wire types ───────────────────────────────────────────────────────────

type oaiMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"` // string | nil
	Name       string         `json:"name,omitempty"`
	ToolCalls  []oaiToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	_          map[string]any `json:"-"`
}

type oaiToolCall struct {
	// Index is set on streaming deltas to identify which tool call a
	// fragment belongs to (each chunk usually carries a single entry, so
	// the slice position is not enough to disambiguate parallel calls).
	Index    *int            `json:"index,omitempty"`
	ID       string          `json:"id"`
	Type     string          `json:"type"` // always "function"
	Function oaiToolFuncCall `json:"function"`
}

type oaiToolFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string         `json:"type"` // "function"
	Function oaiToolFuncDef `json:"function"`
}

type oaiToolFuncDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// oaiContentPart is one element of an array-form message content, used when a
// user message contains both text and images.
type oaiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"` // "data:<mime>;base64,<data>"
}

type oaiRequest struct {
	Model         string            `json:"model"`
	Messages      []oaiMessage      `json:"messages"`
	Tools         []oaiTool         `json:"tools,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
	Temperature   *float32          `json:"temperature,omitempty"`
	MaxTokens     *int32            `json:"max_tokens,omitempty"`
	TopP          *float32          `json:"top_p,omitempty"`
	Stop          []string          `json:"stop,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message      oaiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage,omitempty"`
}

type oaiUsage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
	// DeepSeek-style legacy field: number of prompt tokens served from cache.
	PromptCacheHit int32 `json:"prompt_cache_hit_tokens,omitempty"`
	// OpenAI-native breakdown introduced with prompt caching.
	PromptTokensDetails *struct {
		CachedTokens int32 `json:"cached_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
}

// cachedRead returns the number of prompt tokens served from the provider's
// prompt cache, regardless of which field name the server populated.
func (u *oaiUsage) cachedRead() int32 {
	if u == nil {
		return 0
	}
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		return u.PromptTokensDetails.CachedTokens
	}
	return u.PromptCacheHit
}

// Streaming chunk.
type oaiChunk struct {
	Choices []struct {
		Delta        oaiMessage `json:"delta"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage,omitempty"`
}

// ── Conversion: genai.Content → oaiMessage ───────────────────────────────

func (o *openAI) toMessages(req *model.LLMRequest) []oaiMessage {
	var msgs []oaiMessage
	if sys := systemTextFromReq(req); sys != "" {
		msgs = append(msgs, oaiMessage{Role: "system", Content: sys})
	}
	for _, c := range req.Contents {
		// Group consecutive non-tool parts into one message; tool responses
		// must be standalone "tool" messages.
		role := mapRoleOAI(c.Role)
		var text strings.Builder
		var calls []oaiToolCall
		var imgParts []oaiContentPart
		for _, p := range c.Parts {
			switch {
			case p == nil:
				continue
			case p.FunctionResponse != nil:
				// Flush pending text/calls/images first.
				if text.Len() > 0 || len(calls) > 0 || len(imgParts) > 0 {
					msgs = append(msgs, buildUserMsg(role, text.String(), calls, imgParts))
					text.Reset()
					calls = nil
					imgParts = nil
				}
				msgs = append(msgs, oaiMessage{
					Role:       "tool",
					ToolCallID: firstNonEmpty(p.FunctionResponse.ID, p.FunctionResponse.Name),
					Content:    renderFunctionResponse(p.FunctionResponse),
				})
			case p.FunctionCall != nil:
				calls = append(calls, oaiToolCall{
					ID:   firstNonEmpty(p.FunctionCall.ID, p.FunctionCall.Name),
					Type: "function",
					Function: oaiToolFuncCall{
						Name:      p.FunctionCall.Name,
						Arguments: jsonString(p.FunctionCall.Args),
					},
				})
			case p.InlineData != nil:
				imgParts = append(imgParts, oaiContentPart{
					Type: "image_url",
					ImageURL: &oaiImageURL{
						URL: fmt.Sprintf("data:%s;base64,%s",
							p.InlineData.MIMEType,
							base64.StdEncoding.EncodeToString(p.InlineData.Data)),
					},
				})
			case p.Text != "":
				text.WriteString(p.Text)
			}
		}
		if text.Len() > 0 || len(calls) > 0 || len(imgParts) > 0 {
			msgs = append(msgs, buildUserMsg(role, text.String(), calls, imgParts))
		}
	}
	return msgs
}

// buildUserMsg constructs an oaiMessage for any role. When imgParts is
// non-empty the content is sent as an array of typed parts (text + images)
// so vision-capable models can process the inline image data.
func buildUserMsg(role, text string, calls []oaiToolCall, imgParts []oaiContentPart) oaiMessage {
	m := oaiMessage{Role: role}
	if len(imgParts) > 0 {
		var parts []oaiContentPart
		if text != "" {
			parts = append(parts, oaiContentPart{Type: "text", Text: text})
		}
		parts = append(parts, imgParts...)
		m.Content = parts
	} else if text != "" {
		m.Content = text
	}
	if len(calls) > 0 {
		m.ToolCalls = calls
	}
	return m
}

// buildAssistantMsg constructs a text/tool-call message (no image support
// needed for assistant turns).
func buildAssistantMsg(role, text string, calls []oaiToolCall) oaiMessage {
	return buildUserMsg(role, text, calls, nil)
}

func mapRoleOAI(r string) string {
	switch r {
	case "model", "assistant":
		return "assistant"
	case "user", "":
		return "user"
	case "system":
		return "system"
	default:
		return "user"
	}
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func (o *openAI) toTools(req *model.LLMRequest) []oaiTool {
	var out []oaiTool
	for _, fd := range toolDecls(req.Config) {
		out = append(out, oaiTool{
			Type: "function",
			Function: oaiToolFuncDef{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  schemaToJSON(fd.Parameters),
			},
		})
	}
	return out
}

func (o *openAI) buildRequest(req *model.LLMRequest, stream bool) oaiRequest {
	r := oaiRequest{
		Model:    firstNonEmpty(req.Model, o.model),
		Messages: o.toMessages(req),
		Tools:    o.toTools(req),
		Stream:   stream,
	}
	if stream {
		// Ask the server to emit a final chunk carrying usage metadata.
		// Without this, OpenAI omits prompt/completion token counts in
		// streaming mode and the harness reports "0/0 tok".
		r.StreamOptions = &oaiStreamOptions{IncludeUsage: true}
	}
	if req.Config != nil {
		r.Temperature = req.Config.Temperature
		r.TopP = req.Config.TopP
		if req.Config.MaxOutputTokens > 0 {
			n := req.Config.MaxOutputTokens
			r.MaxTokens = &n
		}
		if len(req.Config.StopSequences) > 0 {
			r.Stop = req.Config.StopSequences
		}
	}
	return r
}

// ── ADK entry point ──────────────────────────────────────────────────────

func (o *openAI) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		body, err := json.Marshal(o.buildRequest(req, stream))
		if err != nil {
			yield(nil, err)
			return
		}
		var resp *http.Response
		for attempt := 0; attempt < maxGenerateAttempts; attempt++ {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
				o.baseURL+"/chat/completions", bytes.NewReader(body))
			if err != nil {
				yield(nil, err)
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			if o.apiKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
			}
			if stream {
				httpReq.Header.Set("Accept", "text/event-stream")
			}
			resp, err = o.client.Do(httpReq)
			if err != nil {
				if attempt < maxGenerateAttempts-1 && !contextDone(ctx) && waitBeforeRetry(ctx, retryDelay(nil, attempt)) {
					continue
				}
				yield(nil, err)
				return
			}
			if resp.StatusCode < 400 {
				break
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			bodyText := string(b)
			if attempt < maxGenerateAttempts-1 && shouldRetryHTTPError(resp.StatusCode, bodyText) && waitBeforeRetry(ctx, retryDelay(resp, attempt)) {
				continue
			}
			yield(nil, fmt.Errorf("openai %s: %s", resp.Status, bodyText))
			return
		}
		defer resp.Body.Close()
		if !stream {
			var out oaiResponse
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				yield(nil, err)
				return
			}
			yield(o.fromResponse(&out), nil)
			return
		}
		o.streamSSE(resp.Body, yield)
	}
}

func (o *openAI) fromResponse(r *oaiResponse) *model.LLMResponse {
	var content *genai.Content
	if len(r.Choices) > 0 {
		content = oaiMsgToContent(r.Choices[0].Message)
	}
	out := &model.LLMResponse{Content: content, TurnComplete: true}
	if r.Usage != nil {
		out.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        r.Usage.PromptTokens,
			CandidatesTokenCount:    r.Usage.CompletionTokens,
			TotalTokenCount:         r.Usage.TotalTokens,
			CachedContentTokenCount: r.Usage.cachedRead(),
		}
	}
	return out
}

func oaiMsgToContent(m oaiMessage) *genai.Content {
	c := &genai.Content{Role: "model"}
	if s, ok := m.Content.(string); ok && s != "" {
		c.Parts = append(c.Parts, &genai.Part{Text: s})
	}
	for _, tc := range m.ToolCalls {
		c.Parts = append(c.Parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: argsFromJSON(tc.Function.Arguments),
			},
		})
	}
	return c
}

// streamSSE drains an SSE stream of oaiChunk events. Text deltas are
// yielded as Partial:true responses; tool-call argument fragments are
// accumulated and emitted as a single FunctionCall on completion.
func (o *openAI) streamSSE(body io.Reader, yield func(*model.LLMResponse, error) bool) {
	type pendingCall struct {
		id, name string
		args     strings.Builder
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	pending := map[int]*pendingCall{}
	var collectedText strings.Builder
	var usage *oaiUsage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var ch oaiChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue
		}
		if ch.Usage != nil {
			usage = ch.Usage
		}
		if len(ch.Choices) == 0 {
			continue
		}
		delta := ch.Choices[0].Delta
		if s, ok := delta.Content.(string); ok && s != "" {
			collectedText.WriteString(s)
			if !yield(&model.LLMResponse{
				Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: s}}},
				Partial: true,
			}, nil) {
				return
			}
		}
		for i, tc := range delta.ToolCalls {
			idx := i
			if tc.Index != nil {
				idx = *tc.Index
			}
			p, ok := pending[idx]
			if !ok {
				p = &pendingCall{}
				pending[idx] = p
			}
			if tc.ID != "" {
				p.id = tc.ID
			}
			if tc.Function.Name != "" {
				p.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				p.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		yield(nil, err)
		return
	}

	// Final non-partial event with the accumulated content.
	final := &model.LLMResponse{TurnComplete: true}
	c := &genai.Content{Role: "model"}
	if collectedText.Len() > 0 {
		c.Parts = append(c.Parts, &genai.Part{Text: collectedText.String()})
	}
	keys := make([]int, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		p := pending[k]
		if p == nil {
			continue
		}
		c.Parts = append(c.Parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   p.id,
				Name: p.name,
				Args: argsFromJSON(p.args.String()),
			},
		})
	}
	final.Content = c
	if usage != nil {
		final.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        usage.PromptTokens,
			CandidatesTokenCount:    usage.CompletionTokens,
			TotalTokenCount:         usage.TotalTokens,
			CachedContentTokenCount: usage.cachedRead(),
		}
	}
	yield(final, nil)
}
