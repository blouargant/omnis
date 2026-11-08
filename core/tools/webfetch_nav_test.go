package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "webfetch", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// longMkDocsPage is a documentation page shaped like a MkDocs site: a long
// navigation sidebar, then an article of six sections of 20k characters.
func longMkDocsPage() []byte {
	var b strings.Builder
	b.WriteString("<html><head><title>Long Docs</title></head><body><header><nav>menu</nav></header><main>")
	b.WriteString(`<div class="md-sidebar"><nav><ul>`)
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, `<li><a href="/p%d">NavLink%d</a></li>`, i, i)
	}
	b.WriteString("</ul></nav></div><article><h1>Long Docs</h1><p>The lead paragraph of the page.</p>")
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&b, `<h2 id="sec%d">Section %d</h2>`, i, i)
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&b, "<p>BodyText of section %d paragraph %d %s</p>", i, j, strings.Repeat("lorem ", 80))
		}
		if i == 3 {
			b.WriteString(`<h3 id="layer-behavior-with-ep-enabled">Layer Behavior with EP Enabled</h3><p>The EP layer text.</p>`)
		}
	}
	b.WriteString("</article></main></body></html>")
	return []byte(b.String())
}

func fetchIn(t *testing.T, in WebFetchIn) string {
	t.Helper()
	out, err := runWebFetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > WebFetchLimit {
		t.Errorf("result %d chars > limit", len(out))
	}
	return out
}

func TestWebFetchSmallPageWhole(t *testing.T) {
	out := fetchIn(t, WebFetchIn{URL: serve(t, 200, "text/html", fixtureBytes(t, "mkdocs.html"))})
	if !strings.Contains(out, "Beta paragraph") || strings.Contains(out, "SideLink3") || strings.Contains(out, "too long") {
		t.Errorf("small page:\n%s", out)
	}
}

func TestWebFetchLongPageOutline(t *testing.T) {
	out := fetchIn(t, WebFetchIn{URL: serve(t, 200, "text/html", longMkDocsPage())})
	if !strings.HasPrefix(out, "[Page too long") || strings.Contains(out, "BodyText") || strings.Contains(out, "NavLink") {
		t.Errorf("outline:\n%s", out)
	}
	if !strings.Contains(out, "(layer-behavior-with-ep-enabled)") {
		t.Errorf("outline misses the sub-section:\n%s", out)
	}
}

func TestWebFetchSectionAndFragment(t *testing.T) {
	url := serve(t, 200, "text/html", longMkDocsPage())
	bySection := fetchIn(t, WebFetchIn{URL: url, Section: "sec2"})
	if !strings.Contains(bySection, "BodyText of section 2") || strings.Contains(bySection, "section 3 ") {
		t.Errorf("section sec2: %.200q", bySection)
	}
	if byFrag := fetchIn(t, WebFetchIn{URL: url + "#sec2"}); byFrag != bySection {
		t.Error("fragment differs from section")
	}
	if up := fetchIn(t, WebFetchIn{URL: url, Section: "#SEC2"}); up != bySection {
		t.Error("case / # not tolerated")
	}
	if title := fetchIn(t, WebFetchIn{URL: url, Section: "Layer Behavior with EP Enabled"}); !strings.Contains(title, "The EP layer text.") {
		t.Errorf("title lookup: %.200q", title)
	}
	if e := fetchIn(t, WebFetchIn{URL: url, Section: "nope"}); !strings.HasPrefix(e, `error section "nope" not found on this page.`) || !strings.Contains(e, "(sec1)") {
		t.Errorf("unknown section: %.300q", e)
	}
	if f := fetchIn(t, WebFetchIn{URL: url + "#nope"}); !strings.HasPrefix(f, "[#nope is not a section of this page; showing the whole page.]") || !strings.Contains(f, "[Page too long") {
		t.Errorf("unknown fragment: %.300q", f)
	}
}

func TestWebFetchSelector(t *testing.T) {
	url := serve(t, 200, "text/html", fixtureBytes(t, "mkdocs.html"))
	if out := fetchIn(t, WebFetchIn{URL: url, Selector: "#b"}); !strings.Contains(out, "Beta paragraph") {
		t.Errorf("selector on a heading: %q", out)
	}
	if out := fetchIn(t, WebFetchIn{URL: url, Selector: ".missing"}); !strings.HasPrefix(out, `error selector ".missing" matched nothing on this page.`) {
		t.Errorf("selector matching nothing: %q", out)
	}
	if out := fetchIn(t, WebFetchIn{URL: url, Selector: "aside.note"}); !strings.Contains(out, "KEEP-ASIDE") || strings.Contains(out, "Alpha") {
		t.Errorf("selector on an element: %q", out)
	}
	if out := fetchIn(t, WebFetchIn{URL: url, Selector: "#b", Section: "b"}); out != "error section and selector cannot be used together" {
		t.Errorf("selector + section: %q", out)
	}
	if out := fetchIn(t, WebFetchIn{URL: serve(t, 200, "text/plain", []byte("x")), Selector: "p"}); out != "error selector needs an HTML page" {
		t.Errorf("selector on text: %q", out)
	}
}

func TestWebFetchMarkdownFile(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Readme\n\nintro\n\n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "## Part %d\n\n%s\n\n", i, strings.Repeat("markdown text ", 800))
	}
	out := fetchIn(t, WebFetchIn{URL: serve(t, 200, "text/plain", []byte(b.String())) + "/README.md"})
	if !strings.HasPrefix(out, "[Page too long") || !strings.Contains(out, "(part-3)") {
		t.Errorf("markdown outline:\n%.500s", out)
	}
}

// multiPagePDF builds a PDF with one page per entry, each page holding its
// lines.
func multiPagePDF(pages [][]string) []byte {
	var objs []string
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>", "") // pages object filled below
	var kids []string
	for _, lines := range pages {
		var s strings.Builder
		s.WriteString("BT /F1 6 Tf 20 770 Td 8 TL")
		for _, l := range lines {
			fmt.Fprintf(&s, " (%s) '", l)
		}
		s.WriteString(" ET")
		page := len(objs) + 1
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", page+1, page+2),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", s.Len(), s.String()),
			"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(b.String())
}

func TestWebFetchPDFParts(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	var pages [][]string
	for p := 0; p < 6; p++ {
		var lines []string
		for i := 0; i < 90; i++ {
			lines = append(lines, fmt.Sprintf("page %d line %d %s", p+1, i, strings.Repeat("abc ", 20)))
		}
		pages = append(pages, lines)
	}
	url := serve(t, 200, "application/pdf", multiPagePDF(pages))
	out := fetchIn(t, WebFetchIn{URL: url, Part: 2})
	if !strings.HasPrefix(out, "[Part 2 of ") || !strings.Contains(out, " of 6)") {
		t.Errorf("pdf part 2: %.200q", out)
	}
	if f := fetchIn(t, WebFetchIn{URL: url, Find: "page 5 line 7 "}); !strings.Contains(f, "--- page 5\n") {
		t.Errorf("pdf find: %.300q", f)
	}
}

func TestWebFetchNavigationReusesDownload(t *testing.T) {
	var hits atomic.Int32
	page := longMkDocsPage()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(page)
	}))
	defer srv.Close()
	pc := newPageCache(webCacheTTL, 64<<20)
	ctx := context.Background()
	outline := webFetchWith(ctx, WebFetchIn{URL: srv.URL}, pc, "s1")
	a := webFetchWith(ctx, WebFetchIn{URL: srv.URL, Section: "sec1"}, pc, "s1")
	b := webFetchWith(ctx, WebFetchIn{URL: srv.URL + "#sec4"}, pc, "s1")
	if hits.Load() != 1 {
		t.Errorf("downloads = %d, want 1", hits.Load())
	}
	if !strings.HasPrefix(outline, "[Page too long") || !strings.Contains(a, "section 1 ") || !strings.Contains(b, "section 4 ") {
		t.Error("navigation results wrong")
	}
	webFetchWith(ctx, WebFetchIn{URL: srv.URL}, pc, "other")
	if hits.Load() != 2 {
		t.Errorf("another session reused the page: downloads = %d", hits.Load())
	}
}
