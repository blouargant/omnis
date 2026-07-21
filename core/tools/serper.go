package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/adk"
)

// serperEndpoint is the Serper.dev Google web-search endpoint. A var (not a
// const) so tests can point it at a local server.
var serperEndpoint = "https://google.serper.dev/search"

// NewSerperTools returns a WebSearch tool backed by Serper.dev (Google engine).
// Serper is a cheaper drop-in alternative to SerpAPI and is the recommended
// provider. Returns nil when apiKey is empty so the caller can skip
// registration (mirrors NewSerpAPITools) — this keeps a multi-provider tool
// list from registering two tools named "WebSearch", which ADK rejects.
func NewSerperTools(apiKey string) []tool.Tool {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return []tool.Tool{
		mustTool("WebSearch",
			"Search the web using Serper.dev (Google) and return a list of results. "+
				"Arguments: `query` (string, required) — the search query; "+
				"`max_results` (int, optional, default 5, max 10) — number of results to return.",
			func(_ adk.ToolContext, in DDGIn) (DDGOut, error) {
				out, _ := runSerperSearch(context.Background(), apiKey, in)
				return DDGOut{Results: out}, nil
			}),
	}
}

// serperResult is one organic result in a Serper.dev response.
type serperResult struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

type serperResponse struct {
	Organic []serperResult `json:"organic"`
}

// TestSerperKey verifies apiKey authenticates against Serper.dev with one
// minimal search request. Returns nil on success, or an error describing the
// failure (network error, or a non-200 status such as an auth rejection).
// Backs the "Test" button in Settings → Global configuration → External API.
func TestSerperKey(ctx context.Context, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("no API key set")
	}

	payload, err := json.Marshal(map[string]any{"q": "omnis connectivity test", "num": 1})
	if err != nil {
		return err
	}

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodPost, serperEndpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return nil
}

func runSerperSearch(ctx context.Context, apiKey string, in DDGIn) (string, error) {
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	payload, err := json.Marshal(map[string]any{
		"q":   in.Query,
		"num": maxResults,
	})
	if err != nil {
		return fmt.Sprintf("error building request: %v", err), nil
	}

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodPost, serperEndpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Sprintf("error building request: %v", err), nil
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("error calling Serper: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("error calling Serper: HTTP %d", resp.StatusCode), nil
	}

	var data serperResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Sprintf("error parsing results: %v", err), nil
	}

	if len(data.Organic) == 0 {
		return "(no results)", nil
	}

	var sb strings.Builder
	count := 0
	for _, r := range data.Organic {
		if count >= maxResults {
			break
		}
		if r.Title == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		if r.Link != "" {
			fmt.Fprintf(&sb, "[%s](%s)", r.Title, r.Link)
		} else {
			sb.WriteString(r.Title)
		}
		if r.Snippet != "" {
			fmt.Fprintf(&sb, " — %s", r.Snippet)
		}
		count++
	}

	if sb.Len() == 0 {
		return "(no results)", nil
	}
	return sb.String(), nil
}
