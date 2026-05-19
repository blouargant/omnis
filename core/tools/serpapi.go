package tools

import (
	"fmt"
	"strings"

	serpapi "github.com/serpapi/serpapi-golang"
	"google.golang.org/adk/tool"
)

// NewSerpAPITools returns a web_search tool backed by SerpAPI (Google engine).
// Returns nil when apiKey is empty so the caller can skip registration.
func NewSerpAPITools(apiKey string) []tool.Tool {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return []tool.Tool{
		mustTool("WebSearch",
			"Search the web using SerpAPI (Google) and return a list of results. "+
				"Arguments: `query` (string, required) — the search query; "+
				"`max_results` (int, optional, default 5, max 10) — number of results to return.",
			func(_ tool.Context, in DDGIn) (DDGOut, error) {
				out, _ := runSerpAPISearch(apiKey, in)
				return DDGOut{Results: out}, nil
			}),
	}
}

func runSerpAPISearch(apiKey string, in DDGIn) (string, error) {
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	setting := serpapi.NewSerpApiClientSetting(apiKey)
	client := serpapi.NewClient(setting)

	data, err := client.Search(map[string]string{
		"engine": "google",
		"q":      in.Query,
		"num":    fmt.Sprintf("%d", maxResults),
	})
	if err != nil {
		return fmt.Sprintf("error calling SerpAPI: %v", err), nil
	}

	organic, _ := data["organic_results"].([]interface{})
	if len(organic) == 0 {
		return "(no results)", nil
	}

	var sb strings.Builder
	count := 0
	for _, item := range organic {
		if count >= maxResults {
			break
		}
		r, _ := item.(map[string]interface{})
		if r == nil {
			continue
		}
		title, _ := r["title"].(string)
		link, _ := r["link"].(string)
		snippet, _ := r["snippet"].(string)
		if title == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		if link != "" {
			fmt.Fprintf(&sb, "[%s](%s)", title, link)
		} else {
			sb.WriteString(title)
		}
		if snippet != "" {
			fmt.Fprintf(&sb, " — %s", snippet)
		}
		count++
	}

	if sb.Len() == 0 {
		return "(no results)", nil
	}
	return sb.String(), nil
}
