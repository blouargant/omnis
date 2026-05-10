// Anthropic Messages API adapter implementing google.golang.org/adk/model.LLM.
//
// Wire: https://docs.anthropic.com/en/api/messages
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
	"strings"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

const (
	defaultAnthropicBase    = "https://api.anthropic.com/v1"
	anthropicVersionHeader  = "2023-06-01"
	anthropicDefaultMaxToks = 4096
)

type anthropic struct {
	model   string
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewAnthropic returns an LLM. baseURL may be empty for the official endpoint.
func NewAnthropic(modelName, apiKey, baseURL string) model.LLM {
	if baseURL == "" {
		baseURL = defaultAnthropicBase
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &anthropic{
		model:   modelName,
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (a *anthropic) Name() string { return a.model }

// ── Wire types ───────────────────────────────────────────────────────────

type antMessage struct {
	Role    string          `json:"role"` // "user" | "assistant"
	Content []antContentBlk `json:"content"`
}

type antContentBlk struct {
	Type string `json:"type"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=image
	Source *antImageSource `json:"source,omitempty"`

	// type=tool_use (assistant)
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// type=tool_result (user)
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Anthropic accepts either a string or a list of content blocks here.
	// We always send a string for simplicity.
	ResultContent string `json:"content,omitempty"`
	IsError       bool   `json:"is_error,omitempty"`
}

type antImageSource struct {
	Type      string `json:"type"`       // always "base64"
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data"`       // base64-encoded bytes
}

type antTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type antRequest struct {
	Model       string       `json:"model"`
	System      string       `json:"system,omitempty"`
	Messages    []antMessage `json:"messages"`
	Tools       []antTool    `json:"tools,omitempty"`
	MaxTokens   int32        `json:"max_tokens"`
	Temperature *float32     `json:"temperature,omitempty"`
	TopP        *float32     `json:"top_p,omitempty"`
	Stop        []string     `json:"stop_sequences,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

type antResponse struct {
	Content    []antContentBlk `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *antUsage       `json:"usage,omitempty"`
}

type antUsage struct {
	InputTokens              int32 `json:"input_tokens"`
	OutputTokens             int32 `json:"output_tokens"`
	CacheReadInputTokens     int32 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int32 `json:"cache_creation_input_tokens,omitempty"`
}

// CacheCreationModality is the synthetic MediaModality value used to surface
// Anthropic's `cache_creation_input_tokens` count via
// genai.GenerateContentResponseUsageMetadata.CacheTokensDetails. genai has no
// dedicated field for this, so consumers (e.g. the TUI cost calculation) must
// look it up by modality string.
const CacheCreationModality genai.MediaModality = "cache_creation"

// anthropicUsageToGenai maps an antUsage onto the generic genai usage
// metadata, normalising semantics to "PromptTokenCount = total prompt
// tokens" (Anthropic's `input_tokens` is fresh-only, so we add cached read
// + creation back in). Cache-read tokens are exposed via
// CachedContentTokenCount; cache-creation tokens piggyback on
// CacheTokensDetails using CacheCreationModality.
func anthropicUsageToGenai(u *antUsage) *genai.GenerateContentResponseUsageMetadata {
	if u == nil {
		return nil
	}
	totalPrompt := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	md := &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        totalPrompt,
		CandidatesTokenCount:    u.OutputTokens,
		TotalTokenCount:         totalPrompt + u.OutputTokens,
		CachedContentTokenCount: u.CacheReadInputTokens,
	}
	if u.CacheCreationInputTokens > 0 {
		md.CacheTokensDetails = []*genai.ModalityTokenCount{{
			Modality:   CacheCreationModality,
			TokenCount: u.CacheCreationInputTokens,
		}}
	}
	return md
}

// ── Conversion: genai.Content → antMessage ───────────────────────────────

func (a *anthropic) toMessages(req *model.LLMRequest) []antMessage {
	var msgs []antMessage
	for _, c := range req.Contents {
		role := "user"
		if c.Role == "model" || c.Role == "assistant" {
			role = "assistant"
		}
		var blocks []antContentBlk
		// Anthropic requires tool_result blocks to live in user messages.
		// If a 'user' Content carries function responses, those become
		// tool_result blocks inside that single user message.
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionResponse != nil:
				blocks = append(blocks, antContentBlk{
					Type:          "tool_result",
					ToolUseID:     firstNonEmpty(p.FunctionResponse.ID, p.FunctionResponse.Name),
					ResultContent: renderFunctionResponse(p.FunctionResponse),
				})
			case p.FunctionCall != nil:
				blocks = append(blocks, antContentBlk{
					Type:  "tool_use",
					ID:    firstNonEmpty(p.FunctionCall.ID, p.FunctionCall.Name),
					Name:  p.FunctionCall.Name,
					Input: p.FunctionCall.Args,
				})
			case p.InlineData != nil:
				blocks = append(blocks, antContentBlk{
					Type: "image",
					Source: &antImageSource{
						Type:      "base64",
						MediaType: p.InlineData.MIMEType,
						Data:      base64.StdEncoding.EncodeToString(p.InlineData.Data),
					},
				})
			case p.Text != "":
				blocks = append(blocks, antContentBlk{Type: "text", Text: p.Text})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		msgs = append(msgs, antMessage{Role: role, Content: blocks})
	}
	return msgs
}

func (a *anthropic) toTools(req *model.LLMRequest) []antTool {
	var out []antTool
	for _, fd := range toolDecls(req.Config) {
		out = append(out, antTool{
			Name:        fd.Name,
			Description: fd.Description,
			InputSchema: toolParamsJSON(fd),
		})
	}
	return out
}

func (a *anthropic) buildRequest(req *model.LLMRequest, stream bool) antRequest {
	r := antRequest{
		Model:     firstNonEmpty(req.Model, a.model),
		Messages:  a.toMessages(req),
		Tools:     a.toTools(req),
		MaxTokens: anthropicDefaultMaxToks,
		Stream:    stream,
	}
	if req.Config != nil {
		if sys := systemText(req.Config.SystemInstruction); sys != "" {
			r.System = sys
		}
		r.Temperature = req.Config.Temperature
		r.TopP = req.Config.TopP
		if req.Config.MaxOutputTokens > 0 {
			r.MaxTokens = req.Config.MaxOutputTokens
		}
		if len(req.Config.StopSequences) > 0 {
			r.Stop = req.Config.StopSequences
		}
	}
	return r
}

// ── ADK entry point ──────────────────────────────────────────────────────

func (a *anthropic) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		body, err := json.Marshal(a.buildRequest(req, stream))
		if err != nil {
			yield(nil, err)
			return
		}
		var resp *http.Response
		for attempt := 0; attempt < maxGenerateAttempts; attempt++ {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
				a.baseURL+"/messages", bytes.NewReader(body))
			if err != nil {
				yield(nil, err)
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("x-api-key", a.apiKey)
			httpReq.Header.Set("anthropic-version", anthropicVersionHeader)
			if stream {
				httpReq.Header.Set("Accept", "text/event-stream")
			}
			resp, err = a.client.Do(httpReq)
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
			yield(nil, fmt.Errorf("anthropic %s: %s", resp.Status, bodyText))
			return
		}
		defer resp.Body.Close()
		if !stream {
			var out antResponse
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				yield(nil, err)
				return
			}
			yield(a.fromResponse(&out), nil)
			return
		}
		a.streamSSE(resp.Body, yield)
	}
}

func (a *anthropic) fromResponse(r *antResponse) *model.LLMResponse {
	c := &genai.Content{Role: "model"}
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			c.Parts = append(c.Parts, &genai.Part{Text: b.Text})
		case "tool_use":
			c.Parts = append(c.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   b.ID,
					Name: b.Name,
					Args: b.Input,
				},
			})
		}
	}
	out := &model.LLMResponse{Content: c, TurnComplete: true}
	if r.Usage != nil {
		out.UsageMetadata = anthropicUsageToGenai(r.Usage)
	}
	return out
}

// streamSSE consumes Anthropic Messages API SSE events.
// Reference event types: message_start, content_block_start,
// content_block_delta (text_delta | input_json_delta), content_block_stop,
// message_delta, message_stop.
func (a *anthropic) streamSSE(body io.Reader, yield func(*model.LLMResponse, error) bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	blocks := map[int]*pending{}
	var usage antUsage
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			a.handleEvent(eventType, data, blocks, &usage, yield)
		}
	}
	if err := scanner.Err(); err != nil {
		yield(nil, err)
		return
	}

	// Build final consolidated content.
	c := &genai.Content{Role: "model"}
	// Iterate by index in order.
	maxIdx := -1
	for i := range blocks {
		if i > maxIdx {
			maxIdx = i
		}
	}
	for i := 0; i <= maxIdx; i++ {
		p := blocks[i]
		if p == nil {
			continue
		}
		switch p.blockType {
		case "text":
			if p.text.Len() > 0 {
				c.Parts = append(c.Parts, &genai.Part{Text: p.text.String()})
			}
		case "tool_use":
			c.Parts = append(c.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   p.toolID,
					Name: p.toolName,
					Args: argsFromJSON(p.toolArgs.String()),
				},
			})
		}
	}
	final := &model.LLMResponse{Content: c, TurnComplete: true}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		final.UsageMetadata = anthropicUsageToGenai(&usage)
	}
	yield(final, nil)
}

// handleEvent updates per-block state and yields incremental text deltas.
func (a *anthropic) handleEvent(eventType, data string, blocks map[int]*pending, usage *antUsage, yield func(*model.LLMResponse, error) bool) {
	switch eventType {
	case "content_block_start":
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		blocks[ev.Index] = &pending{
			blockType: ev.ContentBlock.Type,
			toolID:    ev.ContentBlock.ID,
			toolName:  ev.ContentBlock.Name,
		}
	case "content_block_delta":
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		p := blocks[ev.Index]
		if p == nil {
			return
		}
		switch ev.Delta.Type {
		case "text_delta":
			p.text.WriteString(ev.Delta.Text)
			yield(&model.LLMResponse{
				Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: ev.Delta.Text}}},
				Partial: true,
			}, nil)
		case "input_json_delta":
			p.toolArgs.WriteString(ev.Delta.PartialJSON)
		}
	case "message_delta":
		var ev struct {
			Usage antUsage `json:"usage"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		if ev.Usage.OutputTokens > 0 {
			usage.OutputTokens = ev.Usage.OutputTokens
		}
	case "message_start":
		var ev struct {
			Message struct {
				Usage antUsage `json:"usage"`
			} `json:"message"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		usage.InputTokens = ev.Message.Usage.InputTokens
		usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
		usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
	}
}

// pending is the per-content-block accumulator used by streamSSE.
type pending struct {
	blockType string
	text      strings.Builder
	toolID    string
	toolName  string
	toolArgs  strings.Builder
}
