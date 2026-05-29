// Package embed turns text into dense vectors using the same provider model
// the rest of yoke already talks to. It mirrors core/llm's provider-selection
// shape (Selection + NewWithSelection) but produces embeddings instead of an
// ADK model.LLM.
//
// Supported providers:
//
//   - openai / openai_compat: POST {BaseURL}/v1/embeddings — covers OpenAI,
//     Ollama (nomic-embed-text), vLLM, Together, Voyage and friends.
//   - gemini: the genai EmbedContent API (text-embedding-004 and newer).
//   - anthropic: no native embeddings endpoint — returns ErrUnsupported.
//     Point YOKE_EMBED_* at Voyage/OpenAI via openai_compat instead.
//
// All embedders L2-normalise their output so callers (go-turbovec with
// UnitNorm) get cosine similarity for free. Selection env:
//
//	YOKE_EMBED_PROVIDER → provider name (default: YOKE_PROVIDER, else openai_compat)
//	YOKE_EMBED_MODEL    → model id     (default: text-embedding-3-small)
//	YOKE_EMBED_BASE_URL → endpoint     (default: YOKE_BASE_URL / OPENAI_BASE_URL)
//	YOKE_EMBED_API_KEY  → credential   (default: provider key env)
//	YOKE_EMBED_DIM      → expected output dimension (default: 1536, or learned)
package embed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// DefaultModel is used when no embedding model is configured.
const DefaultModel = "text-embedding-3-small"

// DefaultDim is the output dimension of DefaultModel.
const DefaultDim = 1536

// ErrUnsupported is returned when the selected provider has no embeddings
// endpoint (currently: anthropic). Callers treat it like "no embedder
// configured" and fall back to non-semantic behaviour.
var ErrUnsupported = errors.New("embed: provider has no embeddings endpoint")

// ErrNoEmbedder is returned by graceful-degradation handles that were opened
// without a working embedder. Callers fall back to glob/grep paths.
var ErrNoEmbedder = errors.New("embed: no embedder configured")

// Embedder turns text into L2-normalised dense vectors.
type Embedder interface {
	// Embed returns one vector per input text, in input order. Vectors are
	// L2-normalised.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim returns the embedding dimension (0 until learned from the first
	// response when not explicitly configured).
	Dim() int
	// Model returns the underlying model id (used for cache keys + manifests).
	Model() string
}

// Selection captures explicit embedder connection settings, mirroring
// llm.Selection plus the embedding dimension.
type Selection struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
	Dim      int
}

// New returns an Embedder selected from the YOKE_EMBED_* environment, falling
// back to the YOKE_PROVIDER / YOKE_BASE_URL / YOKE_API_KEY family so a single
// generation config also drives embeddings when no embed-specific overrides
// are present.
func New(ctx context.Context) (Embedder, error) {
	dim := 0
	if raw := strings.TrimSpace(os.Getenv("YOKE_EMBED_DIM")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			dim = v
		}
	}
	return NewWithSelection(ctx, Selection{
		Provider: firstNonEmpty(os.Getenv("YOKE_EMBED_PROVIDER"), os.Getenv("YOKE_PROVIDER")),
		Model:    os.Getenv("YOKE_EMBED_MODEL"),
		BaseURL:  firstNonEmpty(os.Getenv("YOKE_EMBED_BASE_URL"), os.Getenv("YOKE_BASE_URL")),
		APIKey:   firstNonEmpty(os.Getenv("YOKE_EMBED_API_KEY"), os.Getenv("YOKE_API_KEY")),
		Dim:      dim,
	})
}

// NewWithSelection returns an Embedder using an explicit selection. Empty
// values fall back to provider-specific environment variables and defaults.
// The returned embedder is wrapped in a content-hash cache so unchanged text
// is never re-embedded across sessions.
func NewWithSelection(ctx context.Context, sel Selection) (Embedder, error) {
	base, err := newBase(ctx, sel)
	if err != nil {
		return nil, err
	}
	return newCachingEmbedder(base), nil
}

func newBase(ctx context.Context, sel Selection) (Embedder, error) {
	provider := strings.ToLower(strings.TrimSpace(sel.Provider))
	if provider == "" {
		provider = "openai_compat"
	}
	model := strings.TrimSpace(sel.Model)
	if model == "" {
		model = DefaultModel
	}
	dim := sel.Dim
	if dim <= 0 {
		dim = DefaultDim
	}
	baseURL := strings.TrimSpace(sel.BaseURL)
	apiKey := strings.TrimSpace(sel.APIKey)

	if apiKey == "" {
		switch provider {
		case "gemini":
			apiKey = firstNonEmpty(os.Getenv("GOOGLE_API_KEY"), os.Getenv("GEMINI_API_KEY"))
		case "anthropic":
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		case "openai", "openai_compat":
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
	}
	if baseURL == "" {
		switch provider {
		case "openai", "openai_compat":
			baseURL = os.Getenv("OPENAI_BASE_URL")
		}
	}

	switch provider {
	case "gemini":
		if apiKey == "" {
			return nil, fmt.Errorf("embed: gemini requires GOOGLE_API_KEY or GEMINI_API_KEY")
		}
		return newGemini(ctx, model, apiKey, dim)

	case "anthropic":
		return nil, fmt.Errorf("%w: anthropic — use Voyage/OpenAI via openai_compat for YOKE_EMBED_*", ErrUnsupported)

	case "openai":
		if apiKey == "" {
			return nil, fmt.Errorf("embed: openai requires OPENAI_API_KEY")
		}
		return newOpenAI(model, apiKey, baseURL, dim), nil

	case "openai_compat":
		if baseURL == "" {
			return nil, fmt.Errorf("embed: openai_compat requires a base URL (YOKE_EMBED_BASE_URL / OPENAI_BASE_URL)")
		}
		return newOpenAI(model, apiKey, baseURL, dim), nil

	default:
		return nil, fmt.Errorf("embed: unknown provider %q (want gemini|openai|openai_compat)", provider)
	}
}

// normalize L2-normalises v in place and returns it. A zero vector is left
// unchanged (no NaNs).
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
