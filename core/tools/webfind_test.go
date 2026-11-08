package tools

import (
	"fmt"
	"strings"
	"testing"
)

func findDoc() *webDoc {
	table := "| option | meaning |\n| --- | --- |\n| --alpha | first |\n| --max-model-len | context size |\n| --gamma | third |"
	var code []string
	for i := 0; i < 200; i++ {
		code = append(code, fmt.Sprintf("line %d", i))
	}
	code[100] = "CODEHIT here"
	return assembleDoc("Find Page", "lead text", []rawSection{
		{ID: "a", Title: "A", HNum: 2, HIndex: -1, Body: "nothing here\n\n" + table},
		{ID: "b", Title: "B", HNum: 3, HIndex: -1, Body: "The Needle is in this paragraph.\n\nliteral a.b here"},
		{ID: "c", Title: "C", HNum: 2, HIndex: -1, Body: "```\n" + strings.Join(code, "\n") + "\n```\n\naxb only"},
	})
}

func TestRenderFind(t *testing.T) {
	d := findDoc()
	out := renderFind(d, d.Root, "needle", WebFetchLimit)
	if !strings.HasPrefix(out, `[find "needle" in the page: 1 match, showing 1.]`) || !strings.Contains(out, `--- A > B (section="b")`) {
		t.Errorf("paragraph match:\n%s", out)
	}

	row := renderFind(d, d.Root, "--max-model-len", WebFetchLimit)
	if !strings.Contains(row, "| option | meaning |\n| --- | --- |\n| --max-model-len | context size |") || strings.Contains(row, "--alpha") {
		t.Errorf("table match:\n%s", row)
	}

	cm := renderFind(d, d.Root, "codehit", WebFetchLimit)
	_, ex, _ := strings.Cut(cm, `(section="c")`+"\n")
	if n := strings.Count(strings.TrimSpace(ex), "\n") + 1; n > 9 || !strings.HasPrefix(ex, "```") || !strings.Contains(ex, "line 97") || strings.Contains(ex, "line 96\n") {
		t.Errorf("code excerpt (%d lines):\n%s", n, ex)
	}

	lit := renderFind(d, d.Root, "a.b", WebFetchLimit)
	if !strings.Contains(lit, "1 match,") || strings.Contains(lit, "axb") {
		t.Errorf("literal match:\n%s", lit)
	}

	scoped := renderFind(d, mustLookup(t, d, "c"), "needle", WebFetchLimit)
	if !strings.HasPrefix(scoped, `[find "needle" in section "C": no match.]`) {
		t.Errorf("scoped find: %q", scoped)
	}
	if strings.Contains(scoped, "too long") {
		t.Error("small section should not append an outline")
	}

	top := renderFind(d, d.Root, "lead", WebFetchLimit)
	if !strings.Contains(top, "--- (top of page)") {
		t.Errorf("top of page label:\n%s", top)
	}
}

func TestRenderFindManyMatches(t *testing.T) {
	var paras []string
	for i := 0; i < 30; i++ {
		paras = append(paras, fmt.Sprintf("paragraph %d mentions token here. %s", i, strings.Repeat("filler ", 300)))
	}
	d := assembleDoc("Many", "", []rawSection{{ID: "m", Title: "M", HNum: 2, HIndex: -1, Body: strings.Join(paras, "\n\n")}})
	out := renderFind(d, d.Root, "token", WebFetchLimit)
	if !strings.HasPrefix(out, `[find "token" in the page: 30 matches, showing 20.]`) || len(out) > WebFetchLimit {
		t.Errorf("many matches: %d chars, %.80q", len(out), out)
	}
}

func TestRenderFindPDFAndNoMatchOutline(t *testing.T) {
	p := buildPDFDoc([]string{"one", "two", "the target is on page three"})
	if out := renderFind(p, p.Root, "target", WebFetchLimit); !strings.Contains(out, "--- page 3\n") {
		t.Errorf("pdf label:\n%s", out)
	}
	big := genDoc("Big", []int{3}, 15000)
	out := renderFind(big, big.Root, "absent", WebFetchLimit)
	if !strings.HasPrefix(out, `[find "absent" in the page: no match.]`+"\n\n[Page too long") {
		t.Errorf("no-match outline: %.200q", out)
	}
}
