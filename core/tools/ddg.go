package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"google.golang.org/adk/tool"
)

type DDGIn struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}
type DDGOut struct {
	Results string `json:"results"`
}

func NewDDGTools() []tool.Tool {
	return []tool.Tool{
		mustTool("WebSearch",
			"Search the web using DuckDuckGo and return a list of results. "+
				"Arguments: `query` (string, required) — the search query; "+
				"`max_results` (int, optional, default 5, max 10) — number of results to return.",
			func(_ tool.Context, in DDGIn) (DDGOut, error) {
				out, _ := runDDGSearch(context.Background(), in)
				return DDGOut{Results: out}, nil
			}),
	}
}

func runDDGSearch(ctx context.Context, in DDGIn) (string, error) {
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	reqURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(in.Query)

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Sprintf("error building request: %v", err), nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("error fetching results: %v", err), nil
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Sprintf("error parsing results: %v", err), nil
	}

	var sb strings.Builder
	count := 0
	doc.Find(".result").Each(func(_ int, s *goquery.Selection) {
		if count >= maxResults {
			return
		}
		title := strings.TrimSpace(s.Find(".result__title").Text())
		href, _ := s.Find(".result__url").Attr("href")
		if href == "" {
			href, _ = s.Find(".result__a").Attr("href")
		}
		snippet := strings.TrimSpace(s.Find(".result__snippet").Text())
		if title == "" {
			return
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		if href != "" {
			fmt.Fprintf(&sb, "[%s](%s)", title, href)
		} else {
			sb.WriteString(title)
		}
		if snippet != "" {
			fmt.Fprintf(&sb, " — %s", snippet)
		}
		count++
	})

	if sb.Len() == 0 {
		return "(no results)", nil
	}
	return sb.String(), nil
}
