package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"google.golang.org/adk/tool"
)

type WebFetchIn struct {
	URL      string `json:"url"`
	Selector string `json:"selector,omitempty"`
}
type WebFetchOut struct {
	Content string `json:"content"`
}

type HTMLToMarkdownIn struct {
	HTML string `json:"html"`
}
type HTMLToMarkdownOut struct {
	Markdown string `json:"markdown"`
}

func NewWebTools() []tool.Tool {
	return []tool.Tool{
		mustTool("WebFetch",
			"Fetch a web page and return its content as Markdown. "+
				"Arguments: `url` (string, required) — the URL to fetch; "+
				"`selector` (string, optional) — CSS selector to extract a specific page section (e.g. \"main\", \"article\", \"#content\").",
			func(_ tool.Context, in WebFetchIn) (WebFetchOut, error) {
				out, _ := runWebFetch(context.Background(), in)
				return WebFetchOut{Content: out}, nil
			}),
		mustTool("html_to_markdown",
			"Convert an HTML string to Markdown. "+
				"Arguments: `html` (string, required) — the HTML content to convert.",
			func(_ tool.Context, in HTMLToMarkdownIn) (HTMLToMarkdownOut, error) {
				md, err := htmltomarkdown.ConvertString(in.HTML)
				if err != nil {
					return HTMLToMarkdownOut{Markdown: fmt.Sprintf("error: %v", err)}, nil
				}
				return HTMLToMarkdownOut{Markdown: md}, nil
			}),
	}
}

func runWebFetch(ctx context.Context, in WebFetchIn) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return fmt.Sprintf("error building request: %v", err), nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("error fetching URL: %v", err), nil
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Sprintf("error parsing HTML: %v", err), nil
	}

	var htmlContent string
	if in.Selector != "" {
		sel := doc.Find(in.Selector)
		if sel.Length() > 0 {
			htmlContent, _ = sel.First().Html()
		} else {
			htmlContent, _ = doc.Find("body").Html()
		}
	} else {
		htmlContent, _ = doc.Find("body").Html()
	}

	md, err := htmltomarkdown.ConvertString(htmlContent)
	if err != nil {
		return fmt.Sprintf("error converting to markdown: %v", err), nil
	}

	// Remove excessive blank lines
	for strings.Contains(md, "\n\n\n") {
		md = strings.ReplaceAll(md, "\n\n\n", "\n\n")
	}
	return truncate(md), nil
}
