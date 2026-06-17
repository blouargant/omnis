package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/embed"
	"github.com/gin-gonic/gin"
)

// providerHealth is the connectivity verdict for one configured provider,
// returned by GET /providers/health. base_url is reported for display (it is
// not a secret); the API key value is never returned — only whether one is set.
type providerHealth struct {
	Ref       string `json:"ref"`
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url,omitempty"`
	HasAPIKey bool   `json:"has_api_key"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// providerModelInfo is a single model entry returned to the browser.
// Pricing / context / dim / mode fields are optional and forwarded only when
// the upstream provider exposes them (e.g. a LiteLLM proxy via /v1/model/info).
// Plain OpenAI-style /v1/models endpoints return ids only, in which case just
// ID is set and the UI leaves the other fields for the user to fill.
type providerModelInfo struct {
	ID                              string  `json:"id"`
	DisplayName                     string  `json:"display_name,omitempty"`
	ContextLength                   int     `json:"context_length,omitempty"`
	InputTokenPricePerMillion       float64 `json:"input_token_price_per_million,omitempty"`
	CachedInputTokenPricePerMillion float64 `json:"cached_input_token_price_per_million,omitempty"`
	OutputTokenPricePerMillion      float64 `json:"output_token_price_per_million,omitempty"`
	Dim                             int     `json:"dim,omitempty"`
	Mode                            string  `json:"mode,omitempty"`
	Embedding                       bool    `json:"embedding,omitempty"`
}

// registerProviderModelsRoute mounts GET /providers/models on the given router group.
// Query params:
//   - provider_ref: resolves credentials from models.json (preferred — no secrets cross the wire).
//   - provider, api_key, base_url: explicit overrides, used when no provider_ref is set or when
//     test-driving a new provider before it is saved. api_key and base_url are resolved as
//     env-var names first, matching the agent config convention.
func registerProviderModelsRoute(rg *gin.RouterGroup) {
	rg.GET("/providers/models", func(c *gin.Context) {
		providerKind, apiKey, baseURL, status, err := resolveProviderConn(c)
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()

		models, err := fetchProviderModels(ctx, providerKind, apiKey, baseURL)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"models": models})
	})

	// GET /providers/embedding-dim probes a single embeddings request against
	// the resolved provider and reports the output vector length, so the Models
	// editor can auto-fill the DIM field instead of asking the user to look it
	// up. Credentials resolve the same way as /providers/models (provider_ref
	// preferred; provider/api_key/base_url overrides for test-driving). The
	// model id is required and must name an embeddings-capable model.
	rg.GET("/providers/embedding-dim", func(c *gin.Context) {
		model := strings.TrimSpace(c.Query("model"))
		if model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model query param is required"})
			return
		}
		providerKind, apiKey, baseURL, status, err := resolveProviderConn(c)
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		dim, err := probeEmbeddingDim(ctx, providerKind, apiKey, baseURL, model)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"dim": dim})
	})

	// GET /providers/health probes every configured provider for reachability +
	// credential validity by listing its models (the same call that backs the
	// Models editor's combobox), so the web UI can warn the user up-front when a
	// model connection is broken and offer to fix the base URL / API key. Probes
	// run concurrently, each with its own timeout. Credentials come from the live
	// models.json catalogue (already env-resolved), so no secrets are accepted on
	// the wire. Response: { ok, providers: [providerHealth, …] } where the
	// top-level ok is true only when every probed provider connected.
	rg.GET("/providers/health", func(c *gin.Context) {
		settings, err := agent.ResolveRuntimeSettings(agent.Options{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("resolve runtime settings: %v", err)})
			return
		}

		refs := make([]string, 0, len(settings.Providers))
		for ref := range settings.Providers {
			refs = append(refs, ref)
		}
		sort.Strings(refs)

		results := make([]providerHealth, len(refs))
		var wg sync.WaitGroup
		for i, ref := range refs {
			p := settings.Providers[ref]
			results[i] = providerHealth{
				Ref:       ref,
				Kind:      p.Kind,
				BaseURL:   p.BaseURL,
				HasAPIKey: strings.TrimSpace(p.APIKey) != "",
			}
			wg.Add(1)
			go func(i int, kind, apiKey, baseURL string) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
				defer cancel()
				if _, perr := fetchProviderModels(ctx, kind, apiKey, baseURL); perr != nil {
					results[i].OK = false
					results[i].Error = truncate(perr.Error(), 300)
				} else {
					results[i].OK = true
				}
			}(i, p.Kind, p.APIKey, p.BaseURL)
		}
		wg.Wait()

		allOK := true
		for _, h := range results {
			if !h.OK {
				allOK = false
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": allOK, "providers": results})
	})

	// POST /providers/test probes a single connection with the values the user
	// is editing, without saving anything — backing the "Test" button in the
	// connection popup. The body is { ref, kind, base_url, api_key }; any field
	// left blank falls back to the saved provider named by ref (so a blank API
	// key tests the real stored credentials rather than an empty one). Typed
	// base_url / api_key are resolved as env-var names first, matching the agent
	// config convention. A POST body (not a query string) keeps a typed key out
	// of access logs. Response: { ok, model_count } or { ok:false, error }.
	rg.POST("/providers/test", func(c *gin.Context) {
		var req struct {
			Ref     string `json:"ref"`
			Kind    string `json:"kind"`
			BaseURL string `json:"base_url"`
			APIKey  string `json:"api_key"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}

		kind := strings.TrimSpace(req.Kind)
		baseURL := strings.TrimSpace(req.BaseURL)
		apiKey := strings.TrimSpace(req.APIKey)

		// Fall back to the saved provider for any blank field (already env-resolved).
		var saved agent.RuntimeProviderConfig
		hasSaved := false
		if strings.TrimSpace(req.Ref) != "" {
			if settings, err := agent.ResolveRuntimeSettings(agent.Options{}); err == nil {
				if p, ok := settings.Providers[strings.ToLower(strings.TrimSpace(req.Ref))]; ok {
					saved, hasSaved = p, true
				}
			}
		}
		if kind == "" && hasSaved {
			kind = saved.Kind
		}
		if baseURL == "" {
			if hasSaved {
				baseURL = saved.BaseURL
			}
		} else {
			baseURL = resolveEnvRef(baseURL)
		}
		if apiKey == "" {
			if hasSaved {
				apiKey = saved.APIKey
			}
		} else {
			apiKey = resolveEnvRef(apiKey)
		}

		if kind == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider kind is required"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
		defer cancel()
		models, err := fetchProviderModels(ctx, kind, apiKey, baseURL)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": truncate(err.Error(), 300)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "model_count": len(models)})
	})
}

// resolveProviderConn extracts the provider kind + resolved credentials from a
// request, honouring provider_ref (looked up in the live models.json catalogue,
// so no secrets cross the wire) or the explicit provider/api_key/base_url
// overrides. The returned status is the HTTP code to use when err is non-nil.
func resolveProviderConn(c *gin.Context) (providerKind, apiKey, baseURL string, status int, err error) {
	if ref := strings.TrimSpace(c.Query("provider_ref")); ref != "" {
		settings, err := agent.ResolveRuntimeSettings(agent.Options{})
		if err != nil {
			return "", "", "", http.StatusInternalServerError, fmt.Errorf("resolve runtime settings: %v", err)
		}
		p, ok := settings.Providers[strings.ToLower(ref)]
		if !ok {
			return "", "", "", http.StatusNotFound, fmt.Errorf("unknown provider_ref %q", ref)
		}
		return p.Kind, p.APIKey, p.BaseURL, http.StatusOK, nil
	}
	providerKind = strings.TrimSpace(c.Query("provider"))
	if providerKind == "" {
		return "", "", "", http.StatusBadRequest, fmt.Errorf("provider or provider_ref query param is required")
	}
	return providerKind, resolveEnvRef(c.Query("api_key")), resolveEnvRef(c.Query("base_url")), http.StatusOK, nil
}

// probeEmbeddingDim builds an embedder for the given connection and performs a
// single tiny embeddings request, returning the dimension of the vector the
// provider produced. The model's native dimension is reported (the embedder
// only pins a non-default dimension for OpenAI's text-embedding-3-* family,
// which is not what we want to discover here).
func probeEmbeddingDim(ctx context.Context, providerKind, apiKey, baseURL, model string) (int, error) {
	emb, err := embed.NewWithSelection(ctx, embed.Selection{
		Provider: providerKind,
		Model:    model,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	})
	if err != nil {
		return 0, err
	}
	vecs, err := emb.Embed(ctx, []string{"yoke embedding dimension probe"})
	if err != nil {
		return 0, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return 0, fmt.Errorf("provider returned no embedding vector")
	}
	return len(vecs[0]), nil
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

	const url = "https://api.anthropic.com/v1/models"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic API (%s) returned HTTP %d: %s", url, resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, explainNonJSON(url, body)
	}
	out := make([]providerModelInfo, len(result.Data))
	for i, m := range result.Data {
		out[i] = providerModelInfo{ID: m.ID, DisplayName: m.DisplayName}
	}
	return out, nil
}

// fetchOpenAIStyleModels lists models from an OpenAI-compatible endpoint.
//
// LiteLLM proxies (the shape behind ChapsVision's gateways) additionally expose
// GET /v1/model/info with per-model metadata — context window, per-token
// pricing, cache pricing, embedding vector size, and mode (chat/embedding). When
// that endpoint answers we use it so the Models editor can prefill those fields
// on selection. For plain OpenAI / Ollama / vLLM endpoints (no /model/info) we
// fall back to GET /v1/models, which returns ids only.
func fetchOpenAIStyleModels(ctx context.Context, apiKey, baseURL string) ([]providerModelInfo, error) {
	if infos, err := fetchLiteLLMModelInfo(ctx, apiKey, baseURL); err == nil && len(infos) > 0 {
		return infos, nil
	}

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
		return nil, fmt.Errorf("request to %s failed: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d: %s", url, resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, explainNonJSON(url, body)
	}
	out := make([]providerModelInfo, len(result.Data))
	for i, m := range result.Data {
		out[i] = providerModelInfo{ID: m.ID}
	}
	return out, nil
}

// fetchLiteLLMModelInfo queries a LiteLLM proxy's GET /v1/model/info endpoint
// and maps each entry to a providerModelInfo with whatever metadata the proxy
// records. Returns an error (so the caller falls back to /v1/models) when the
// endpoint is absent, unauthorised, or not LiteLLM-shaped. Per-token costs are
// converted to the per-million units the editor stores; null/zero fields are
// left unset so the UI only prefills what the proxy actually knows.
func fetchLiteLLMModelInfo(ctx context.Context, apiKey, baseURL string) ([]providerModelInfo, error) {
	root := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	root = strings.TrimSuffix(root, "/v1")
	url := root + "/v1/model/info"

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
		return nil, fmt.Errorf("model/info returned %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				Mode                    string  `json:"mode"`
				MaxInputTokens          float64 `json:"max_input_tokens"`
				MaxTokens               float64 `json:"max_tokens"`
				InputCostPerToken       float64 `json:"input_cost_per_token"`
				OutputCostPerToken      float64 `json:"output_cost_per_token"`
				CacheReadInputTokenCost float64 `json:"cache_read_input_token_cost"`
				OutputVectorSize        float64 `json:"output_vector_size"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse model/info: %w", err)
	}

	// A LiteLLM proxy can list several deployments under one public alias;
	// the editor only needs one row per model id, so dedup by model_name.
	seen := make(map[string]struct{}, len(result.Data))
	out := make([]providerModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ModelName == "" {
			continue
		}
		if _, dup := seen[m.ModelName]; dup {
			continue
		}
		seen[m.ModelName] = struct{}{}

		mi := m.ModelInfo
		ctxLen := mi.MaxInputTokens
		if ctxLen == 0 {
			ctxLen = mi.MaxTokens
		}
		out = append(out, providerModelInfo{
			ID:                              m.ModelName,
			ContextLength:                   int(ctxLen),
			InputTokenPricePerMillion:       perMillion(mi.InputCostPerToken),
			CachedInputTokenPricePerMillion: perMillion(mi.CacheReadInputTokenCost),
			OutputTokenPricePerMillion:      perMillion(mi.OutputCostPerToken),
			Dim:                             int(mi.OutputVectorSize),
			Mode:                            mi.Mode,
			Embedding:                       mi.Mode == "embedding",
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("model/info returned no models")
	}
	return out, nil
}

// perMillion converts a LiteLLM per-token cost to a per-million-token price,
// rounded to 4 decimals to shed the float noise LiteLLM stores (e.g.
// 5.249999958e-06 → 5.25). Zero stays zero so the UI leaves the field blank.
func perMillion(costPerToken float64) float64 {
	if costPerToken <= 0 {
		return 0
	}
	return math.Round(costPerToken*1e6*1e4) / 1e4
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

	// displayURL omits the ?key= query so the API key never leaks into an error.
	const displayURL = "https://generativelanguage.googleapis.com/v1beta/models"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", displayURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API (%s) returned HTTP %d: %s", displayURL, resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, explainNonJSON(displayURL, body)
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

// explainNonJSON turns a JSON-decode failure on a 2xx response into a
// human-readable diagnostic. The usual cause is a base URL that points at a web
// page or an SSO/login portal instead of the API endpoint, in which case the
// body is HTML (starts with '<') rather than the expected JSON model list. url
// must not contain secrets (callers pass a key-free display URL for providers
// that put the key in the query string).
func explainNonJSON(url string, body []byte) error {
	trimmed := bytes.TrimSpace(body)
	switch {
	case len(trimmed) == 0:
		return fmt.Errorf("%s returned an empty body where a JSON model list was expected — check the base URL points at the API endpoint", url)
	case trimmed[0] == '<':
		return fmt.Errorf("%s returned an HTML page, not JSON — the base URL is probably pointing at a web page or login portal instead of the API endpoint (an OpenAI-style base URL usually needs to end in /v1). First bytes: %q", url, truncate(string(trimmed), 80))
	default:
		return fmt.Errorf("%s returned a non-JSON response — check the base URL and API key. First bytes: %q", url, truncate(string(trimmed), 80))
	}
}
