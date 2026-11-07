package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// minimalPDF builds a one-page PDF whose text layer is text, with a correct
// xref table so pdftotext accepts it.
func minimalPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
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

func serve(t *testing.T, status int, ctype string, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func fetch(t *testing.T, url string) string {
	t.Helper()
	out, err := runWebFetch(context.Background(), WebFetchIn{URL: url})
	if err != nil {
		t.Fatalf("runWebFetch: %v", err)
	}
	return out
}

// A PDF used to be handed to the HTML parser, which failed with "open stack
// of elements exceeds 512 nodes" — every manual a research agent tried to read.
func TestWebFetchExtractsPDFText(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	pdf := minimalPDF("Notice RF 6700 FP")
	for _, ctype := range []string{"application/pdf", "application/octet-stream", ""} {
		out := fetch(t, serve(t, 200, ctype, pdf))
		if !strings.Contains(out, "Notice RF 6700 FP") {
			t.Errorf("content-type %q: want the PDF text, got %q", ctype, out)
		}
		if strings.HasPrefix(out, "error ") {
			t.Errorf("content-type %q: unexpected error %q", ctype, out)
		}
	}
}

// A 403 "Access Denied" page must not read as content, and the "error "
// prefix keeps it out of the web cache.
func TestWebFetchReportsHTTPErrors(t *testing.T) {
	out := fetch(t, serve(t, 403, "text/html", []byte("<html><body><h1>Access Denied</h1></body></html>")))
	if !strings.HasPrefix(out, "error ") || !strings.Contains(out, "403") {
		t.Fatalf("want an HTTP 403 error, got %q", out)
	}
	if cacheableWebResult(out) {
		t.Fatalf("an HTTP error must not be cached: %q", out)
	}
}

func TestWebFetchPlainTextAndHTML(t *testing.T) {
	if out := fetch(t, serve(t, 200, "text/plain; charset=utf-8", []byte("hello\n\n\n\nworld"))); out != "hello\n\nworld" {
		t.Errorf("plain text: got %q", out)
	}
	if out := fetch(t, serve(t, 200, "text/html; charset=utf-8", []byte("<html><body><h1>Title</h1><p>Body</p></body></html>"))); !strings.Contains(out, "# Title") || !strings.Contains(out, "Body") {
		t.Errorf("html: got %q", out)
	}
}

func TestWebFetchRejectsBinary(t *testing.T) {
	out := fetch(t, serve(t, 200, "image/png", []byte{0x89, 'P', 'N', 'G'}))
	if !strings.HasPrefix(out, "error ") || !strings.Contains(out, "image/png") {
		t.Fatalf("want an unsupported-type error, got %q", out)
	}
}
