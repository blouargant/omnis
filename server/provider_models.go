package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// providerModelInfo is a single model entry returned to the browser.
type providerModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

// registerProviderModelsRoute mounts GET /providers/models on the given router group.
// Query params: provider (required), api_key (optional), base_url (optional for openai_compat).
// api_key and base_url are resolved as env-var names first, matching the agent config convention.
func registerProviderModelsRoute(rg *gin.RouterGroup) {
	rg.GET("/providers/models", func(c *gin.Context) {
		provider := strings.TrimSpace(c.Query("provider"))
		if provider == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider query param is required"})
			return
		}

		apiKey := resolveEnvRef(c.Query("api_key"))
		baseURL := resolveEnvRef(c.Query("base_url"))

		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()

		models, err := fetchProviderModels(ctx, provider, apiKey, baseURL)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"models": models})
	})
}

// resolveEnvRef returns the value of the environment variable named val when
// such a variable exists and is non-empty, otherwise returns val unchanged.
// This mirrors the agent config's api_key / base_url resolution logic.
func resolveEnvRef(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	if env := os.Getenv(val); env != "" {
		return env
	}
	return val
}

func fetchProviderModels(ctx context.Context, provider, apiKey, baseURL string) ([]providerModelInfo, error) {
	switch provider {
	case "anthropic":
		return fetchAnthropicModels(ctx, apiKey)
	case "openai":
		return fetchOpenAIStyleModels(ctx, apiKey, "https://api.openai.com")
	case "openai_compat":
		if baseURL == "" {
			return nil, fmt.Errorf("base_url is required for openai_compat provider")
		}
		return fetchOpenAIStyleModels(ctx, apiKey, baseURL)
	case "gemini":
		return fetchGeminiModels(ctx, apiKey)
	default:
		return nil, fmt.Errorf("unsupported provider %q (supported: anthropic, openai, openai_compat, gemini)", provider)
	}
}

// fetchAnthropicModels calls GET https://api.anthropic.com/v1/models.
// Response: { "data": [ { "id": "claude-...", "display_name": "..." }, ... ] }
func fetchAnthropicModels(ctx context.Context, apiKey string) ([]providerModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	out := make([]providerModelInfo, len(result.Data))
	for i, m := range result.Data {
		out[i] = providerModelInfo{ID: m.ID, DisplayName: m.DisplayName}
	}
	return out, nil
}

// fetchOpenAIStyleModels calls GET {baseURL}/v1/models (OpenAI and compatible endpoints).
// Response: { "data": [ { "id": "gpt-4o", ... }, ... ] }
func fetchOpenAIStyleModels(ctx context.Context, apiKey, baseURL string) ([]providerModelInfo, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	// Support base URLs that already include /v1.
	url := baseURL + "/models"
	if !strings.Contains(baseURL, "/v1") {
		url = baseURL + "/v1/models"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	out := make([]providerModelInfo, len(result.Data))
	for i, m := range result.Data {
		out[i] = providerModelInfo{ID: m.ID}
	}
	return out, nil
}

// fetchGeminiModels calls the Generative Language API to list available models.
// Response: { "models": [ { "name": "models/gemini-2.5-flash", "displayName": "..." }, ... ] }
func fetchGeminiModels(ctx context.Context, apiKey string) ([]providerModelInfo, error) {
	url := "https://generativelanguage.googleapis.com/v1beta/models"
	if apiKey != "" {
		url += "?key=" + apiKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	out := make([]providerModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		// name is "models/gemini-xxx"; strip the prefix for the usable ID.
		id := strings.TrimPrefix(m.Name, "models/")
		out = append(out, providerModelInfo{ID: id, DisplayName: m.DisplayName})
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
