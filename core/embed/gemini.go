package embed

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// geminiEmbedder wraps the genai EmbedContent API.
type geminiEmbedder struct {
	model  string
	dim    int
	client *genai.Client
}

func newGemini(ctx context.Context, model, apiKey string, dim int) (*geminiEmbedder, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("embed: gemini client: %w", err)
	}
	return &geminiEmbedder{model: model, dim: dim, client: client}, nil
}

func (e *geminiEmbedder) Model() string { return e.model }
func (e *geminiEmbedder) Dim() int      { return e.dim }

func (e *geminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	contents := make([]*genai.Content, len(texts))
	for i, t := range texts {
		contents[i] = genai.NewContentFromText(t, genai.RoleUser)
	}
	var cfg *genai.EmbedContentConfig
	if e.dim > 0 && e.dim != DefaultDim {
		d := int32(e.dim)
		cfg = &genai.EmbedContentConfig{OutputDimensionality: &d}
	}
	resp, err := e.client.Models.EmbedContent(ctx, e.model, contents, cfg)
	if err != nil {
		return nil, fmt.Errorf("embed: gemini EmbedContent: %w", err)
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: gemini returned %d embeddings for %d inputs", len(resp.Embeddings), len(texts))
	}
	out := make([][]float32, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		if emb == nil || len(emb.Values) == 0 {
			return nil, fmt.Errorf("embed: gemini empty embedding at %d", i)
		}
		vec := make([]float32, len(emb.Values))
		copy(vec, emb.Values)
		out[i] = normalize(vec)
	}
	if e.dim <= 0 && len(out[0]) > 0 {
		e.dim = len(out[0])
	}
	return out, nil
}
