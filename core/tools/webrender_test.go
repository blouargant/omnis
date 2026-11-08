package tools

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// genDoc builds a doc with the given fan-out per level and size of text per
// leaf section.
func genDoc(title string, fanout []int, leaf int) *webDoc {
	var parts []rawSection
	var walk func(depth int, path string)
	walk = func(depth int, path string) {
		if depth == len(fanout) {
			return
		}
		for i := 0; i < fanout[depth]; i++ {
			p := fmt.Sprintf("%s%d", path, i+1)
			body := ""
			if depth == len(fanout)-1 {
				body = strings.Repeat("word ", leaf/5)
			}
			parts = append(parts, rawSection{ID: "id" + p, Title: "Title " + p, HNum: depth + 2, HIndex: -1, Body: body})
			walk(depth+1, p+"-")
		}
	}
	walk(0, "")
	return assembleDoc(title, "The lead of the page.", parts)
}

func TestFmtSize(t *testing.T) {
	for n, want := range map[int]string{850: "850", 2140: "2.1k", 96000: "96k", 2000: "2k"} {
		if got := fmtSize(n); got != want {
			t.Errorf("fmtSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRenderSectionFits(t *testing.T) {
	d := genDoc("Small", []int{3}, 100)
	if got := renderSection(d, d.Root, 0, WebFetchLimit); got != d.Root.Markdown() {
		t.Errorf("small page not returned whole: %q", got)
	}
}

func TestRenderOutlineDepth(t *testing.T) {
	d := genDoc("Deep", []int{3, 16, 15}, 400)
	out := renderSection(d, d.Root, 0, WebFetchLimit)
	if !strings.HasPrefix(out, "[Page too long to return whole: ") {
		t.Fatalf("no outline header: %.100q", out)
	}
	header, rest, _ := strings.Cut(out, "\n\n")
	_ = header
	if len(rest) > outlineBudget+leadMax+200 {
		t.Errorf("outline body = %d chars", len(rest))
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(out, fmt.Sprintf("- Title %d (id%d) — ", i, i)) {
			t.Errorf("top-level %d missing", i)
		}
	}
	if !strings.Contains(out, "sub-sections") {
		t.Error("folded entries carry no sub-section count")
	}
	if strings.Contains(out, "word word") {
		t.Error("outline contains section text")
	}

	small := genDoc("Shallow", []int{3, 2}, 7000)
	so := renderSection(small, small.Root, 0, WebFetchLimit)
	if !strings.Contains(so, "  - Title 3-2 (id3-2) — ") || strings.Contains(so, "sub-sections") {
		t.Errorf("shallow outline should show every level:\n%s", so)
	}
}

func TestRenderOutlineOverflow(t *testing.T) {
	d := genDoc("Wide", []int{2000}, 50)
	out := renderSection(d, d.Root, 0, WebFetchLimit)
	if len(out) > WebFetchLimit {
		t.Errorf("outline = %d chars", len(out))
	}
	if !strings.HasSuffix(out, "more top-level sections not shown: use find to locate one.") {
		t.Errorf("no overflow line: %q", out[len(out)-120:])
	}
}

func TestRenderSectionOutlineRecursive(t *testing.T) {
	d := genDoc("Rec", []int{1, 5}, 20000)
	top := d.Root.Children[0]
	out := renderSection(d, top, 0, WebFetchLimit)
	if !strings.HasPrefix(out, `[Section "Title 1" too long to return whole: `) || !strings.Contains(out, "\nPath: Title 1\n") {
		t.Errorf("section outline:\n%.300s", out)
	}
	child := top.Children[1]
	if got := renderSection(d, child, 0, WebFetchLimit); got != child.Markdown() {
		t.Error("a child that fits is not returned whole")
	}
}

func TestRenderSectionParts(t *testing.T) {
	d := genDoc("Parts", []int{1}, 70000)
	leaf := d.Root.Children[0]
	p1 := renderSection(d, leaf, 0, WebFetchLimit)
	if !strings.HasPrefix(p1, `[Part 1 of 3 of "Title 1" — fetch part=2 for the next.]`) {
		t.Errorf("part 1 header: %.100q", p1)
	}
	p3 := renderSection(d, leaf, 3, WebFetchLimit)
	if !strings.HasPrefix(p3, `[Part 3 of 3 of "Title 1" — last part.]`) {
		t.Errorf("part 3 header: %.100q", p3)
	}
	if e := renderSection(d, leaf, 4, WebFetchLimit); e != `error part 4 out of range: "Title 1" has 3 parts` {
		t.Errorf("part 4 = %q", e)
	}
	for _, r := range []string{p1, p3} {
		if len(r) > WebFetchLimit {
			t.Errorf("part = %d chars", len(r))
		}
	}
	big := genDoc("Big", []int{2, 3}, 15000)
	p2 := renderSection(big, big.Root, 2, WebFetchLimit)
	if !strings.HasPrefix(p2, "[Part 2 of ") || strings.Contains(p2, "too long to return whole") {
		t.Errorf("part on a section with sub-sections should be content: %.100q", p2)
	}
}

func TestRenderPDFParts(t *testing.T) {
	var pages []string
	for i := 0; i < 6; i++ {
		pages = append(pages, strings.Repeat("pdf text ", 1500))
	}
	d := buildPDFDoc(pages)
	out := renderSection(d, d.Root, 2, WebFetchLimit)
	if !strings.HasPrefix(out, "[Part 2 of ") || !strings.Contains(out, "(pages ") || !strings.Contains(out, " of 6)") {
		t.Errorf("pdf part header: %.120q", out)
	}
}

func TestRenderLeadUTF8(t *testing.T) {
	d := assembleDoc("Lead", strings.Repeat("é", 2000), []rawSection{
		{ID: "a", Title: "A", HNum: 2, HIndex: -1, Body: strings.Repeat("x ", 20000)},
		{ID: "b", Title: "B", HNum: 2, HIndex: -1, Body: strings.Repeat("y ", 20000)},
	})
	out := renderSection(d, d.Root, 0, WebFetchLimit)
	if !utf8.ValidString(out) {
		t.Fatal("invalid UTF-8")
	}
	_, after, _ := strings.Cut(out, "# Lead\n")
	lead, _, _ := strings.Cut(after, "\n\n")
	if !strings.HasSuffix(lead, "…") || len(lead) > leadMax+len("…") {
		t.Errorf("lead = %d chars, ends %q", len(lead), lead[len(lead)-6:])
	}
}
