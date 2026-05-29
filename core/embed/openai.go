package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openaiEmbedder talks to an OpenAI-compatible /v1/embeddings endpoint. This
// covers OpenAI proper, Ollama, vLLM, Together, Voyage, and any other service
// exposing the same request/response shape.
type openaiEmbedder struct {
	model   string
	apiKey  string
	baseURL string
	dim     int
	client  *http.Client
}

func newOpenAI(model, apiKey, baseURL string, dim int) *openaiEmbedder {
	return &openaiEmbedder{
		model:   model,
		apiKey:  apiKey,
		baseURL: baseURL,
		dim:     dim,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *openaiEmbedder) Model() string { return e.model }
func (e *openaiEmbedder) Dim() int      { return e.dim }

type openaiEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type openaiEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// embedsURL returns the embeddings endpoint, tolerating base URLs that already
// include the /v1 segment (as OpenAI's canonical base does).
func embedsURL(baseURL string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(b, "/v1") || strings.Contains(b, "/v1/") {
		return b + "/embeddings"
	}
	return b + "/v1/embeddings"
}

func (e *openaiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const batch = 96
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *openaiEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := openaiEmbedRequest{Model: e.model, Input: texts}
	// OpenAI's text-embedding-3-* models honour an explicit dimensions param.
	// Compat servers (Ollama et al.) ignore unknown fields, so it's safe to
	// pass when the caller pinned a non-default dimension.
	if e.dim > 0 && e.dim != DefaultDim && strings.HasPrefix(e.model, "text-embedding-3") {
		reqBody.Dimensions = e.dim
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		vecs, retry, err := e.doRequest(ctx, payload)
		if err == nil {
			return vecs, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("embed: openai request failed after retries: %w", lastErr)
}

func (e *openaiEmbedder) doRequest(ctx context.Context, payload []byte) (vecs [][]float32, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embedsURL(e.baseURL), bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, true, fmt.Errorf("embed: openai status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("embed: openai status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed openaiEmbedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, fmt.Errorf("embed: decode openai response: %w", err)
	}
	if parsed.Error != nil {
		return nil, false, fmt.Errorf("embed: openai error: %s", parsed.Error.Message)
	}
	if len(parsed.Data) == 0 {
		return nil, false, fmt.Errorf("embed: openai returned no embeddings")
	}

	out := make([][]float32, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, false, fmt.Errorf("embed: openai response index %d out of range", d.Index)
		}
		out[d.Index] = normalize(d.Embedding)
	}
	for i := range out {
		if out[i] == nil {
			return nil, false, fmt.Errorf("embed: openai response missing vector %d", i)
		}
	}
	// Learn the dimension from the first response when it wasn't pinned.
	if e.dim <= 0 && len(out[0]) > 0 {
		e.dim = len(out[0])
	}
	return out, false, nil
}
