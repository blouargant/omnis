package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitParts(t *testing.T) {
	const max = 32000
	var paras []string
	for i := 0; i < 100; i++ {
		paras = append(paras, strings.Repeat("p", 999)+string(rune('a'+i%26)))
	}
	in := strings.Join(paras, "\n\n")
	parts := splitParts(in, max)
	for i, p := range parts {
		if len(p) > max {
			t.Errorf("paragraphs part %d = %d chars", i, len(p))
		}
	}
	if strings.Join(parts, "\n\n") != strings.Join(mdBlocks(in), "\n\n") {
		t.Error("paragraph parts do not rejoin to the input")
	}

	var rows []string
	rows = append(rows, "| name | value |", "| --- | --- |")
	for i := 0; len(strings.Join(rows, "\n")) < 50000; i++ {
		rows = append(rows, "| row "+strings.Repeat("x", 40)+" | "+strings.Repeat("y", 40)+" |")
	}
	for i, p := range splitParts(strings.Join(rows, "\n"), max) {
		if len(p) > max || !strings.HasPrefix(p, "| name | value |\n| --- | --- |\n") {
			t.Errorf("table part %d: %d chars, starts %q", i, len(p), p[:min(40, len(p))])
		}
	}

	code := "```go\n" + strings.Repeat("fmt.Println(\"a line of code here\")\n", 1500) + "```"
	cparts := splitParts(code, max)
	if len(cparts) < 2 {
		t.Fatalf("code parts = %d", len(cparts))
	}
	for i, p := range cparts {
		if len(p) > max || !strings.HasPrefix(p, "```go\n") || !strings.HasSuffix(p, "\n```") {
			t.Errorf("code part %d not a complete fence (%d chars)", i, len(p))
		}
	}

	line := strings.Repeat("é", 20000)
	lparts := splitParts(line, max)
	if len(lparts) < 2 || strings.Join(lparts, "") != line {
		t.Errorf("long line parts = %d", len(lparts))
	}
	for i, p := range lparts {
		if !utf8.ValidString(p) || len(p) > max {
			t.Errorf("line part %d invalid or too long", i)
		}
	}
}

func TestPDFParts(t *testing.T) {
	var pages []string
	for i := 0; i < 10; i++ {
		pages = append(pages, strings.Repeat("w", 8000))
	}
	parts := pdfParts(pages, 32000)
	want := [][2]int{{1, 3}, {4, 6}, {7, 9}, {10, 10}}
	if len(parts) != len(want) {
		t.Fatalf("parts = %d, want %d", len(parts), len(want))
	}
	for i, w := range want {
		if parts[i].First != w[0] || parts[i].Last != w[1] || len(parts[i].Text) > 32000 {
			t.Errorf("part %d = %d–%d (%d chars)", i, parts[i].First, parts[i].Last, len(parts[i].Text))
		}
	}

	big := strings.Repeat("a line of text in one big page\n", 1300)
	bp := pdfParts([]string{big}, 32000)
	if len(bp) < 2 {
		t.Fatalf("big page parts = %d", len(bp))
	}
	for i, p := range bp {
		if p.First != 1 || p.Last != 1 || len(p.Text) > 32000 {
			t.Errorf("big part %d = %d–%d (%d chars)", i, p.First, p.Last, len(p.Text))
		}
	}
}
