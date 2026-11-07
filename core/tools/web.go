package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
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
			"Fetch a web page and return its content as Markdown (PDFs are returned as their extracted text). "+
				"Arguments: `url` (string, required) — the URL to fetch; "+
				"`selector` (string, optional) — CSS selector to extract a specific page section (e.g. \"main\", \"article\", \"#content\").",
			func(tc adk.ToolContext, in WebFetchIn) (WebFetchOut, error) {
				out := sharedWebCache.get(webCacheScope(tc, tc.SessionID()), "fetch\x00"+in.URL+"\x00"+in.Selector, func() string {
					out, _ := runWebFetch(context.Background(), in)
					return out
				})
				return WebFetchOut{Content: out}, nil
			}),
		mustTool("html_to_markdown",
			"Convert an HTML string to Markdown. "+
				"Arguments: `html` (string, required) — the HTML content to convert.",
			func(_ adk.ToolContext, in HTMLToMarkdownIn) (HTMLToMarkdownOut, error) {
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

	// A blocked or missing page is not content: an "Access Denied" 403
	// rendered as Markdown reads like a source, and the "error " prefix
	// also keeps it out of the web cache.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("error fetching URL: HTTP %s", resp.Status), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBytes+1))
	if err != nil {
		return fmt.Sprintf("error reading response: %v", err), nil
	}
	if len(body) > maxWebFetchBytes {
		return fmt.Sprintf("error fetching URL: response larger than %d bytes", maxWebFetchBytes), nil
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	switch {
	case mediaType == "application/pdf" || bytes.HasPrefix(body, []byte("%PDF-")):
		return pdfToText(cctx, body), nil
	case isHTMLMediaType(mediaType):
		// Fall through to the HTML → Markdown path below.
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" ||
		strings.HasSuffix(mediaType, "+json"):
		return truncate(collapseBlankLines(string(body))), nil
	default:
		return fmt.Sprintf("error fetching URL: unsupported content type %q (%d bytes); "+
			"download it with Bash (curl -o) and use a tool suited to that format", mediaType, len(body)), nil
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
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

	return truncate(collapseBlankLines(md)), nil
}

// maxWebFetchBytes bounds how much of a response WebFetch reads. Large enough
// for a product manual PDF (the Delta Dore notices that motivated PDF support
// are 1–2 MB), small enough that a runaway download cannot exhaust memory.
const maxWebFetchBytes = 32 << 20

// isHTMLMediaType reports whether a response should go through the HTML
// parser. An absent Content-Type is treated as HTML, as before.
func isHTMLMediaType(t string) bool {
	switch t {
	case "", "text/html", "application/xhtml+xml", "application/xml", "text/xml":
		return true
	}
	return false
}

// pdfToText extracts a PDF's text layer with pdftotext (poppler). Before this
// existed WebFetch handed PDF bytes to the HTML parser, which failed with
// "open stack of elements exceeds 512 nodes" on every manual a research agent
// tried to read.
func pdfToText(ctx context.Context, data []byte) string {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "error fetching URL: the page is a PDF and pdftotext (poppler-utils) is not installed; " +
			"download it with Bash (curl -sL -o file.pdf URL) and extract its text with another tool"
	}
	f, err := os.CreateTemp("", "omnis-webfetch-*.pdf")
	if err != nil {
		return fmt.Sprintf("error extracting PDF text: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Sprintf("error extracting PDF text: %v", err)
	}
	f.Close()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-layout", "-enc", "UTF-8", f.Name(), "-")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Sprintf("error extracting PDF text: %s", msg)
	}
	lines := strings.Split(strings.ReplaceAll(stdout.String(), "\f", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	text := strings.TrimSpace(collapseBlankLines(strings.Join(lines, "\n")))
	if text == "" {
		return "error extracting PDF text: the PDF has no text layer (probably scanned); it needs OCR"
	}
	return truncate(text)
}

// collapseBlankLines squeezes runs of blank lines down to one.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
