package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

type WebFetchIn struct {
	URL      string `json:"url" jsonschema:"required"`
	Section  string `json:"section,omitempty" jsonschema:"an id from the page outline"`
	Find     string `json:"find,omitempty" jsonschema:"text to search for in the page or section"`
	Part     int    `json:"part,omitempty" jsonschema:"1-based part of a long section"`
	Pages    string `json:"pages,omitempty" jsonschema:"PDF only: pages to read, e.g. 3 or 3-5 or 1,4,7-9 (combine with find to search only those pages)"`
	Selector string `json:"selector,omitempty" jsonschema:"CSS selector (advanced)"`
	Raw      bool   `json:"raw,omitempty" jsonschema:"keep the page navigation"`
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
			"Fetch a web page or document as Markdown: HTML, PDF (text, with [page N] markers), Word/OpenDocument/EPUB/RTF, plain text, JSON, CSV. "+
				"A page longer than the limit returns its outline instead of its content: "+
				"then fetch one part with section (an id from the outline), search it with find, or read a long section part by part. "+
				"For a PDF, read given pages with pages (e.g. \"3-4\"). Use this tool rather than curl.",
			func(tc adk.ToolContext, in WebFetchIn) (WebFetchOut, error) {
				scope := webCacheScope(tc, tc.SessionID())
				out := sharedWebCache.get(scope, webFetchResultKey(in), func() string {
					return webFetchWith(context.Background(), in, sharedPageCache, scope)
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

// runWebFetch fetches without the page cache (tests and one-off callers).
func runWebFetch(ctx context.Context, in WebFetchIn) (string, error) {
	return webFetchWith(ctx, in, nil, ""), nil
}

// fetchedPage is a downloaded page turned into a section tree. html keeps the
// raw HTML so a CSS selector can run on it.
type fetchedPage struct {
	doc  *webDoc
	html []byte
	size int
}

// fetchPage downloads url and builds its document.
func fetchPage(ctx context.Context, url string, raw bool) (*fetchedPage, string) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Sprintf("error building request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Sprintf("error fetching URL: %v", err)
	}
	defer resp.Body.Close()

	// A blocked or missing page is not content: an "Access Denied" 403
	// rendered as Markdown reads like a source, and the "error " prefix
	// also keeps it out of the web cache.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Sprintf("error fetching URL: HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBytes+1))
	if err != nil {
		return nil, fmt.Sprintf("error reading response: %v", err)
	}
	if len(body) > maxWebFetchBytes {
		return nil, fmt.Sprintf("error fetching URL: response larger than %d bytes", maxWebFetchBytes)
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	page := &fetchedPage{}
	switch {
	case mediaType == "application/pdf" || bytes.HasPrefix(body, []byte("%PDF-")):
		pages, e := pdfPages(cctx, body)
		if e != "" {
			return nil, e
		}
		page.doc = buildPDFDoc(pages)
	case officeFormat(mediaType, url) != "":
		md, e := pandocMarkdown(cctx, body, officeFormat(mediaType, url))
		if e != "" {
			return nil, e
		}
		page.doc = buildMarkdownDoc(md, "")
	case isHTMLMediaType(mediaType):
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Sprintf("error parsing HTML: %v", err)
		}
		markHeadings(doc)
		d, err := buildHTMLDoc(contentRoot(doc, raw), doc.Find("title").First().Text())
		if err != nil {
			return nil, fmt.Sprintf("error converting to markdown: %v", err)
		}
		page.doc, page.html = d, body
	case mediaType == "text/markdown" || mediaType == "text/x-markdown" ||
		(strings.HasPrefix(mediaType, "text/") && isMarkdownPath(url)):
		page.doc = buildMarkdownDoc(string(body), "")
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" ||
		strings.HasSuffix(mediaType, "+json"):
		page.doc = buildTextDoc(string(body))
	default:
		return nil, fmt.Sprintf("error fetching URL: unsupported content type %q (%d bytes); "+
			"download it with Bash (curl -o) and use a tool suited to that format", mediaType, len(body))
	}
	page.size = len(page.html) + page.doc.Root.Size()
	return page, ""
}

func isMarkdownPath(rawURL string) bool {
	p := rawURL
	if u, err := neturl.Parse(rawURL); err == nil {
		p = u.Path
	}
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".markdown")
}

// webFetchResultKey is the result cache key: the URL without its fragment and
// every argument that changes the result.
func webFetchResultKey(in WebFetchIn) string {
	url, frag := splitFragment(in.URL)
	section := in.Section
	if section == "" && in.Selector == "" {
		section = frag
	}
	return strings.Join([]string{"fetch", url, in.Selector, section, in.Find, strconv.Itoa(in.Part), strings.TrimSpace(in.Pages), strconv.FormatBool(in.Raw)}, "\x00")
}

// splitFragment cuts the #fragment off a URL (the server never sees it).
func splitFragment(rawURL string) (string, string) {
	i := strings.Index(rawURL, "#")
	if i < 0 {
		return rawURL, ""
	}
	frag := rawURL[i+1:]
	if f, err := neturl.PathUnescape(frag); err == nil {
		frag = f
	}
	return rawURL[:i], frag
}

// webFetchWith fetches a page (through pages when given) and returns the part
// the arguments ask for, within WebFetchLimit.
func webFetchWith(ctx context.Context, in WebFetchIn, pages *pageCache, scope string) string {
	if strings.TrimSpace(in.Section) != "" && strings.TrimSpace(in.Selector) != "" {
		return "error section and selector cannot be used together"
	}
	url, frag := splitFragment(strings.TrimSpace(in.URL))
	page, e := pages.get(scope, url+"\x00"+strconv.FormatBool(in.Raw), func() (*fetchedPage, string) {
		return fetchPage(ctx, url, in.Raw)
	})
	if e != "" {
		return e
	}
	d := page.doc
	if spec := strings.TrimSpace(in.Pages); spec != "" {
		if len(d.Pages) == 0 {
			return "error pages applies to PDF documents only; on this page use section, find or part"
		}
		if strings.TrimSpace(in.Section) != "" || strings.TrimSpace(in.Selector) != "" || in.Part > 0 {
			return "error pages cannot be combined with section, selector or part (find is allowed)"
		}
		nums, err := parsePageSpec(spec, len(d.Pages))
		if err != nil {
			return "error " + err.Error()
		}
		if in.Find != "" {
			return renderFindIn(d, d.Root, in.Find, WebFetchLimit, nums)
		}
		return renderPages(d, nums, WebFetchLimit)
	}
	sel := d.Root
	prefix := ""
	switch {
	case strings.TrimSpace(in.Selector) != "":
		if page.html == nil {
			return "error selector needs an HTML page"
		}
		hd, err := goquery.NewDocumentFromReader(bytes.NewReader(page.html))
		if err != nil {
			return fmt.Sprintf("error parsing HTML: %v", err)
		}
		markHeadings(hd)
		m := hd.Find(in.Selector).First()
		if m.Length() == 0 {
			msg := fmt.Sprintf("error selector %q matched nothing on this page.", in.Selector)
			return withOutline(msg, d, WebFetchLimit)
		}
		if idx, err := strconv.Atoi(m.AttrOr(headingAttr, "")); err == nil && d.byHeading[idx] != nil {
			sel = d.byHeading[idx]
			break
		}
		sd, err := buildHTMLDoc(m, d.Title)
		if err != nil {
			return fmt.Sprintf("error converting to markdown: %v", err)
		}
		d, sel = sd, sd.Root
	default:
		ref := strings.TrimSpace(in.Section)
		explicit := ref != ""
		if !explicit {
			ref = frag
		}
		if ref == "" {
			break
		}
		if s, ok := d.Lookup(ref); ok {
			sel = s
		} else if explicit {
			return withOutline(fmt.Sprintf("error section %q not found on this page.", ref), d, WebFetchLimit)
		} else {
			prefix = fmt.Sprintf("[#%s is not a section of this page; showing the whole page.]\n\n", frag)
		}
	}
	limit := WebFetchLimit - len(prefix)
	if in.Find != "" {
		return prefix + renderFind(d, sel, in.Find, limit)
	}
	return prefix + renderSection(d, sel, in.Part, limit)
}

// withOutline appends the page's sections to an error so the model can pick a
// valid id: the outline when the page is long, else the list of sections.
func withOutline(msg string, d *webDoc, limit int) string {
	if len(d.Root.Children) == 0 {
		return msg
	}
	room := limit - len(msg) - 2
	if d.Root.Size() > limit {
		return msg + "\n\n" + renderOutline(d, d.Root, room)
	}
	list := "Sections of this page:\n" + strings.Join(outlineEntries(d.Root, subtreeHeight(d.Root)), "\n")
	return msg + "\n\n" + truncateAt(list, room)
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

// pdfPages extracts a PDF's text layer with pdftotext (poppler), one string
// per page. Before PDF support WebFetch handed PDF bytes to the HTML parser,
// which failed with "open stack of elements exceeds 512 nodes" on every
// manual a research agent tried to read.
func pdfPages(ctx context.Context, data []byte) ([]string, string) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return nil, "error fetching URL: the page is a PDF and pdftotext (poppler-utils) is not installed; " +
			"download it with Bash (curl -sL -o file.pdf URL) and extract its text with another tool"
	}
	f, err := os.CreateTemp("", "omnis-webfetch-*.pdf")
	if err != nil {
		return nil, fmt.Sprintf("error extracting PDF text: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, fmt.Sprintf("error extracting PDF text: %v", err)
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
		return nil, fmt.Sprintf("error extracting PDF text: %s", msg)
	}
	raw := strings.Split(stdout.String(), "\f")
	if len(raw) > 1 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1] // pdftotext ends every page with a form feed
	}
	pages := make([]string, len(raw))
	any := false
	for i, p := range raw {
		lines := strings.Split(p, "\n")
		for j, l := range lines {
			lines[j] = strings.TrimRight(l, " \t")
		}
		pages[i] = strings.TrimSpace(collapseBlankLines(strings.Join(lines, "\n")))
		any = any || pages[i] != ""
	}
	if !any {
		return nil, "error extracting PDF text: the PDF has no text layer (probably scanned); it needs OCR"
	}
	return pages, ""
}

// collapseBlankLines squeezes runs of blank lines down to one.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// officeFormats maps document media types pandoc can read to its input format.
var officeFormats = map[string]string{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/vnd.oasis.opendocument.text":                                 "odt",
	"application/epub+zip":                                                    "epub",
	"application/rtf":                                                         "rtf",
	"text/rtf":                                                                "rtf",
}

// officeExtensions is the fallback when a server sends a generic type.
var officeExtensions = map[string]string{".docx": "docx", ".odt": "odt", ".epub": "epub", ".rtf": "rtf"}

// officeFormat returns the pandoc input format of a document, or "".
func officeFormat(mediaType, rawURL string) string {
	if f, ok := officeFormats[mediaType]; ok {
		return f
	}
	switch mediaType {
	case "", "application/octet-stream", "binary/octet-stream", "application/zip", "application/x-zip-compressed":
	default:
		return ""
	}
	p := rawURL
	if u, err := neturl.Parse(rawURL); err == nil {
		p = u.Path
	}
	p = strings.ToLower(p)
	for ext, f := range officeExtensions {
		if strings.HasSuffix(p, ext) {
			return f
		}
	}
	return ""
}

// pandocMarkdown converts a document to Markdown with pandoc.
func pandocMarkdown(ctx context.Context, data []byte, format string) (string, string) {
	bin, err := exec.LookPath("pandoc")
	if err != nil {
		return "", fmt.Sprintf("error fetching URL: the document is %s and pandoc is not installed to convert it", format)
	}
	f, err := os.CreateTemp("", "omnis-webfetch-*."+format)
	if err != nil {
		return "", fmt.Sprintf("error converting %s: %v", format, err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", fmt.Sprintf("error converting %s: %v", format, err)
	}
	f.Close()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-f", format, "-t", "gfm", "--wrap=none", f.Name())
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Sprintf("error converting %s: %s", format, truncateAt(msg, 300))
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", fmt.Sprintf("error converting %s: the document has no text", format)
	}
	return string(out), ""
}
