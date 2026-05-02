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
//	GOAGENT_PROVIDER  → one of the names above (default: openai_compat)
//	GOAGENT_MODEL     → provider-specific model id (defaults below)
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

// New returns an ADK LLM selected by GOAGENT_PROVIDER.
func New(ctx context.Context) (model.LLM, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("GOAGENT_PROVIDER")))
	if provider == "" {
		provider = "openai_compat"
	}
	modelName := os.Getenv("GOAGENT_MODEL")
	if modelName == "" {
		modelName = defaultModel[provider]
	}
	if modelName == "" {
		return nil, fmt.Errorf("llm: GOAGENT_MODEL must be set for provider %q", provider)
	}

	switch provider {
	case "gemini":
		key := firstEnv("GOOGLE_API_KEY", "GEMINI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("llm: gemini requires GOOGLE_API_KEY or GEMINI_API_KEY")
		}
		return gemini.NewModel(ctx, modelName, &genai.ClientConfig{APIKey: key})

	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("llm: anthropic requires ANTHROPIC_API_KEY")
		}
		return NewAnthropic(modelName, key, ""), nil

	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("llm: openai requires OPENAI_API_KEY")
		}
		return NewOpenAI(modelName, key, ""), nil

	case "openai_compat":
		base := os.Getenv("OPENAI_BASE_URL")
		if base == "" {
			return nil, fmt.Errorf("llm: openai_compat requires OPENAI_BASE_URL")
		}
		return NewOpenAI(modelName, os.Getenv("OPENAI_API_KEY"), base), nil

	default:
		return nil, fmt.Errorf("llm: unknown provider %q (want gemini|anthropic|openai|openai_compat)", provider)
	}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
