// Package llm picks an LLM provider based on environment and exposes a
// common ADK model.LLM. Supported providers:
//
//   - gemini         (uses google.golang.org/adk/model/gemini)
//   - anthropic      (Anthropic Messages API)
//   - openai         (OpenAI Chat Completions API)
//   - openai_compat  (any OpenAI-compatible endpoint, e.g. Ollama, vLLM,
//     Together, Groq, Mistral; requires OPENAI_BASE_URL)
//
// Selection env:
//
//	YOKE_PROVIDER  → one of the names above (default: openai_compat)
//	YOKE_MODEL     → provider-specific model id (defaults below)
//
// Auth env (per provider):
//
//	gemini:        GOOGLE_API_KEY or GEMINI_API_KEY
//	anthropic:     ANTHROPIC_API_KEY
//	openai:        OPENAI_API_KEY
//	openai_compat: OPENAI_API_KEY (optional) + OPENAI_BASE_URL (required)
package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
)

// Default model id per provider.
var defaultModel = map[string]string{
	"gemini":        "gemini-2.5-flash",
	"anthropic":     "claude-sonnet-4-5",
	"openai":        "gpt-4o-mini",
	"openai_compat": "gpt-4o-mini",
}

// Selection captures explicit model/provider connection settings.
type Selection struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
	// DisableStreaming forces the adapter to use the non-streaming endpoint
	// even when the runner requests SSE streaming. Set per-model via
	// models.json `disable_streaming` for backends whose streamed output
	// misbehaves. No effect on the gemini provider.
	DisableStreaming bool
}

// New returns an ADK LLM selected by YOKE_PROVIDER.
func New(ctx context.Context) (model.LLM, error) {
	return NewWithSelection(ctx, Selection{
		Provider: os.Getenv("YOKE_PROVIDER"),
		Model:    os.Getenv("YOKE_MODEL"),
		BaseURL:  os.Getenv("YOKE_BASE_URL"),
		APIKey:   os.Getenv("YOKE_API_KEY"),
	})
}

// NewWith returns an ADK LLM using an explicit provider/model selection.
// Empty values fall back to the package defaults, matching New().
func NewWith(ctx context.Context, provider, modelName string) (model.LLM, error) {
	return NewWithSelection(ctx, Selection{Provider: provider, Model: modelName})
}

// NewWithSelection returns an ADK LLM using explicit provider/model/baseURL/
// apiKey settings. Empty values fall back to provider-specific environment
// variables and defaults.
func NewWithSelection(ctx context.Context, sel Selection) (model.LLM, error) {
	provider, modelName, err := resolveProviderModel(sel.Provider, sel.Model)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimSpace(sel.BaseURL)
	apiKey := strings.TrimSpace(sel.APIKey)

	if apiKey == "" {
		switch provider {
		case "gemini":
			apiKey = firstEnv("GOOGLE_API_KEY", "GEMINI_API_KEY")
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
		case "anthropic":
			baseURL = os.Getenv("ANTHROPIC_BASE_URL")
		}
	}

	switch provider {
	case "gemini":
		if apiKey == "" {
			return nil, fmt.Errorf("llm: gemini requires GOOGLE_API_KEY or GEMINI_API_KEY")
		}
		return gemini.NewModel(ctx, modelName, &genai.ClientConfig{APIKey: apiKey})

	case "anthropic":
		if apiKey == "" {
			return nil, fmt.Errorf("llm: anthropic requires ANTHROPIC_API_KEY")
		}
		return withStreamPref(NewAnthropic(modelName, apiKey, baseURL), sel.DisableStreaming), nil

	case "openai":
		if apiKey == "" {
			return nil, fmt.Errorf("llm: openai requires OPENAI_API_KEY")
		}
		return withStreamPref(NewOpenAI(modelName, apiKey, baseURL), sel.DisableStreaming), nil

	case "openai_compat":
		if baseURL == "" {
			return nil, fmt.Errorf("llm: openai_compat requires OPENAI_BASE_URL")
		}
		return withStreamPref(NewOpenAI(modelName, apiKey, baseURL), sel.DisableStreaming), nil

	default:
		return nil, fmt.Errorf("llm: unknown provider %q (want gemini|anthropic|openai|openai_compat)", provider)
	}
}

// withStreamPref flips the per-model non-streaming flag on the concrete
// adapter when disable is set. Unknown adapter types (e.g. gemini) pass
// through unchanged.
func withStreamPref(m model.LLM, disable bool) model.LLM {
	if disable {
		switch v := m.(type) {
		case *openAI:
			v.forceNonStreaming = true
		case *anthropic:
			v.forceNonStreaming = true
		}
	}
	return m
}

func resolveProviderModel(provider, modelName string) (string, string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "openai_compat"
	}
	if _, ok := defaultModel[provider]; !ok {
		return "", "", fmt.Errorf("llm: unknown provider %q (want gemini|anthropic|openai|openai_compat)", provider)
	}

	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = defaultModel[provider]
	}
	if modelName == "" {
		return "", "", fmt.Errorf("llm: YOKE_MODEL must be set for provider %q", provider)
	}
	return provider, modelName, nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
