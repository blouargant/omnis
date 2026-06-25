// OpenAI Chat Completions adapter implementing google.golang.org/adk/model.LLM.
// Works against api.openai.com and any OpenAI-compatible endpoint via
// `baseURL` (Ollama, vLLM, Together, Groq, OpenRouter, Mistral, etc.).
package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"path/filepath"
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
	// forceNonStreaming makes GenerateContent ignore a caller's stream=true
	// and use the non-streaming endpoint instead. Set per-model via
	// models.json `disable_streaming` for backends whose streaming output
	// misbehaves (e.g. some quantised models behind vLLM/LiteLLM that run away
	// only when streamed). The non-streaming response is delivered as one
	// final turn, which the web UI renders fine (just not token-by-token).
	forceNonStreaming bool
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
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"` // string | []oaiContentPart | nil
	Name       string        `json:"name,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	// LiteLLM (and some other OpenAI-compat gateways) emit inline image output
	// from image-generation models — e.g. gemini-*-image-preview — as a
	// vendor-extension `images` array alongside a regular string `content`.
	// Each entry mirrors the user-side image_url shape: a data URL embedding
	// the base64 bytes. We capture them here and decode in oaiMsgToContent.
	Images []oaiContentPart `json:"images,omitempty"`
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
				Parameters:  toolParamsJSON(fd),
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
	if o.forceNonStreaming {
		stream = false
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		body, err := json.Marshal(o.buildRequest(req, stream))
		if err != nil {
			yield(nil, err)
			return
		}
		// Derive a cancelable request context so the streaming stall guard can
		// abort a frozen read (see newStallGuard). For non-stream calls the
		// deferred cancel simply releases the request on return.
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var resp *http.Response
		for attempt := 0; attempt < maxGenerateAttempts; attempt++ {
			httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
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
		guard := newStallGuard(reqCtx, resp.Body, cancel, streamStallTimeout())
		o.streamSSE(guard, yield)
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

	// Content may be a plain string OR a multimodal array of parts. Image
	// generation models (e.g. gemini-*-image-preview via LiteLLM) return their
	// output in the array form, or as a vendor-extension `images` field.
	switch v := m.Content.(type) {
	case string:
		if v != "" {
			c.Parts = append(c.Parts, &genai.Part{Text: v})
		}
	case []any:
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := obj["type"].(string)
			switch kind {
			case "text", "":
				if s, _ := obj["text"].(string); s != "" {
					c.Parts = append(c.Parts, &genai.Part{Text: s})
				}
			case "image_url":
				url := imageURLFromMap(obj)
				if path, hint := saveDataURLImage(url); path != "" {
					c.Parts = append(c.Parts, &genai.Part{Text: hint})
				}
			}
		}
	}

	// LiteLLM-style vendor extension carrying generated images.
	for _, img := range m.Images {
		url := ""
		if img.ImageURL != nil {
			url = img.ImageURL.URL
		}
		if path, hint := saveDataURLImage(url); path != "" {
			c.Parts = append(c.Parts, &genai.Part{Text: hint})
		}
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

// imageURLFromMap returns the `image_url.url` field from a multimodal content
// part decoded as a generic map (the path json.Unmarshal takes for `any`).
func imageURLFromMap(obj map[string]any) string {
	switch u := obj["image_url"].(type) {
	case string:
		return u
	case map[string]any:
		s, _ := u["url"].(string)
		return s
	}
	return ""
}

// saveDataURLImage decodes a "data:<mime>;base64,..." URL and writes the bytes
// to $TMPDIR/omnis-images/<random>.<ext>. It returns the absolute file path and
// a human-readable line ("Generated image saved to <path>") that callers can
// embed as a text part so downstream agents — and the Web UI's local-image
// renderer — can reference the file. Returns ("", "") for unrecognised inputs.
func saveDataURLImage(dataURL string) (path, hint string) {
	const prefix = "data:"
	if !strings.HasPrefix(dataURL, prefix) {
		return "", ""
	}
	commaIdx := strings.Index(dataURL, ",")
	if commaIdx < 0 {
		return "", ""
	}
	meta := dataURL[len(prefix):commaIdx]
	payload := dataURL[commaIdx+1:]
	mime := meta
	if i := strings.Index(meta, ";"); i >= 0 {
		mime = meta[:i]
	}
	if !strings.HasPrefix(mime, "image/") {
		return "", ""
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", ""
	}
	dir := filepath.Join(os.TempDir(), "omnis-images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", ""
	}
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	ext := extFromMIME(mime)
	out := filepath.Join(dir, fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), hex.EncodeToString(nonce[:]), ext))
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", ""
	}
	return out, fmt.Sprintf("Generated image saved to %s", out)
}

func extFromMIME(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

// streamSSE drains an SSE stream of oaiChunk events. Text deltas are
// yielded as Partial:true responses; tool-call argument fragments are
// accumulated and emitted as a single FunctionCall on completion.
// Inline image data (array-form content from image-generation models, or the
// LiteLLM-style `images` vendor extension) is accumulated and decoded to disk
// at end-of-stream so its file path becomes part of the final response.
func (o *openAI) streamSSE(body io.Reader, yield func(*model.LLMResponse, error) bool) {
	type pendingCall struct {
		id, name string
		args     strings.Builder
	}
	// bufio.Reader.ReadString has no fixed line cap (unlike bufio.Scanner),
	// which matters here: an SSE `data:` line carrying a base64-encoded image
	// from a generation model routinely exceeds tens of MB.
	reader := bufio.NewReaderSize(body, 64*1024)
	pending := map[int]*pendingCall{}
	var collectedText strings.Builder
	var imageDataURLs []string
	var usage *oaiUsage

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			yield(nil, err)
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			if err == io.EOF {
				break
			}
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
		switch v := delta.Content.(type) {
		case string:
			if v != "" {
				collectedText.WriteString(v)
				if !yield(&model.LLMResponse{
					Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: v}}},
					Partial: true,
				}, nil) {
					return
				}
			}
		case []any:
			for _, item := range v {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				switch kind, _ := obj["type"].(string); kind {
				case "text", "":
					if s, _ := obj["text"].(string); s != "" {
						collectedText.WriteString(s)
						if !yield(&model.LLMResponse{
							Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: s}}},
							Partial: true,
						}, nil) {
							return
						}
					}
				case "image_url":
					if u := imageURLFromMap(obj); u != "" {
						imageDataURLs = append(imageDataURLs, u)
					}
				}
			}
		}
		for _, img := range delta.Images {
			if img.ImageURL != nil && img.ImageURL.URL != "" {
				imageDataURLs = append(imageDataURLs, img.ImageURL.URL)
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
		if err == io.EOF {
			break
		}
	}

	// Final non-partial event with the accumulated content.
	final := &model.LLMResponse{TurnComplete: true}
	c := &genai.Content{Role: "model"}
	if collectedText.Len() > 0 {
		c.Parts = append(c.Parts, &genai.Part{Text: collectedText.String()})
	}
	for _, url := range imageDataURLs {
		if _, hint := saveDataURLImage(url); hint != "" {
			c.Parts = append(c.Parts, &genai.Part{Text: hint})
		}
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
