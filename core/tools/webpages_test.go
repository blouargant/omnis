package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePageSpec(t *testing.T) {
	cases := []struct {
		spec string
		want []int
	}{
		{"3", []int{3}},
		{"3-5", []int{3, 4, 5}},
		{"7-9, 1,4", []int{1, 4, 7, 8, 9}},
		{"3–4", []int{3, 4}},
		{"9-", []int{9, 10}},
		{"8-40", []int{8, 9, 10}}, // clipped to the last page
		{"2,2,2-3", []int{2, 3}},
	}
	for _, c := range cases {
		got, err := parsePageSpec(c.spec, 10)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePageSpec(%q) = %v, %v; want %v", c.spec, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "x", "0", "5-3", "11", "a-b"} {
		if got, err := parsePageSpec(bad, 10); err == nil {
			t.Errorf("parsePageSpec(%q) = %v, want an error", bad, got)
		}
	}
	if got := formatPageList([]int{1, 2, 3, 7, 9, 10}); got != "1-3, 7, 9-10" {
		t.Errorf("formatPageList = %q", got)
	}
}

func TestRenderPages(t *testing.T) {
	d := buildPDFDoc([]string{"alpha", "bravo", "charlie", "delta"})
	out := renderPages(d, []int{2, 3}, WebFetchLimit)
	if !strings.HasPrefix(out, "[Pages 2-3 of 4]") || !strings.Contains(out, "[page 2]\n\nbravo") ||
		!strings.Contains(out, "[page 3]\n\ncharlie") || strings.Contains(out, "alpha") {
		t.Errorf("pages 2-3:\n%s", out)
	}

	big := strings.Repeat("x", 20000)
	d = buildPDFDoc([]string{big, big, "tail"})
	out = renderPages(d, []int{1, 2, 3}, WebFetchLimit)
	if !strings.HasPrefix(out, `[Pages 1 of 3 shown; the rest did not fit: fetch pages="2-3" next.]`) || len(out) > WebFetchLimit {
		t.Errorf("overflow: %d chars, %.120q", len(out), out)
	}

	d = buildPDFDoc([]string{strings.Repeat("y", 50000), "two"})
	out = renderPages(d, []int{1, 2}, WebFetchLimit)
	if !strings.HasPrefix(out, `[Page 1 of 2, cut at the limit`) || !strings.Contains(out, `Then fetch pages="2".`) || len(out) > WebFetchLimit {
		t.Errorf("long page: %d chars, %.200q", len(out), out)
	}
}

// Whole PDFs and their parts carry page markers, so a value can be cited
// with its page.
func TestPDFTextHasPageMarkers(t *testing.T) {
	d := buildPDFDoc([]string{"one", "two"})
	if got := d.Root.Markdown(); !strings.Contains(got, "[page 1]\n\none\n\n[page 2]\n\ntwo") {
		t.Errorf("whole PDF:\n%s", got)
	}
	if got := buildPDFDoc([]string{"single"}).Root.Markdown(); strings.Contains(got, "[page") {
		t.Errorf("one-page PDF should carry no marker: %q", got)
	}
	parts := pdfParts([]string{"one", "two"}, 1000)
	if len(parts) != 1 || !strings.HasPrefix(parts[0].Text, "[page 1]") {
		t.Errorf("parts: %+v", parts)
	}
}

func TestWebFetchPages(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	url := serve(t, 200, "application/pdf", multiPagePDF([][]string{{"first page"}, {"second page"}, {"third page holds the rate"}}))
	got, _ := runWebFetch(context.Background(), WebFetchIn{URL: url, Pages: "2-3"})
	if !strings.HasPrefix(got, "[Pages 2-3 of 3]") || !strings.Contains(got, "second page") ||
		!strings.Contains(got, "third page") || strings.Contains(got, "first page") {
		t.Errorf("pages 2-3:\n%s", got)
	}
	got, _ = runWebFetch(context.Background(), WebFetchIn{URL: url, Pages: "1-2", Find: "rate"})
	if !strings.Contains(got, "no match") {
		t.Errorf("find restricted to pages 1-2 should not see page 3:\n%s", got)
	}
	got, _ = runWebFetch(context.Background(), WebFetchIn{URL: url, Pages: "3", Find: "rate"})
	if !strings.Contains(got, "--- page 3\n") {
		t.Errorf("find on page 3:\n%s", got)
	}
	for _, in := range []WebFetchIn{
		{URL: url, Pages: "9"},
		{URL: url, Pages: "1", Section: "x"},
		{URL: url, Pages: "1", Part: 2},
		{URL: serve(t, 200, "text/html", []byte("<p>hi</p>")), Pages: "1"},
	} {
		if got, _ := runWebFetch(context.Background(), in); !strings.HasPrefix(got, "error ") {
			t.Errorf("%+v: want an error, got %q", in, got)
		}
	}
}

// A Word document is read through pandoc, with its headings as sections.
func TestWebFetchReadsDocx(t *testing.T) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not installed")
	}
	dir := t.TempDir()
	src, docx := filepath.Join(dir, "in.md"), filepath.Join(dir, "out.docx")
	if err := os.WriteFile(src, []byte("# Rates\n\nThe rate is 46.1 %.\n\n# Notes\n\nnone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("pandoc", src, "-o", docx).CombinedOutput(); err != nil {
		t.Fatalf("pandoc: %v %s", err, out)
	}
	body, err := os.ReadFile(docx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctype := range []string{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/octet-stream"} {
		url := serve(t, 200, ctype, body) + "/report.docx"
		got, _ := runWebFetch(context.Background(), WebFetchIn{URL: url, Section: "rates"})
		if !strings.Contains(got, "46.1") || strings.Contains(got, "none") {
			t.Errorf("%s: section rates:\n%s", ctype, got)
		}
	}
}
