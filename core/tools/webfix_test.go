package tools

import (
	"strings"
	"testing"
)

// html-to-markdown escapes _ * < in prose; find must still see the text.
func TestFindMatchesEscapedMarkdown(t *testing.T) {
	d := htmlDocFromString(t, `<html><body><article><h2 id="a">A</h2>
<p>Set max_tokens_limit to 5. Use a*b and &lt;div&gt; and foo_bar.</p></article></body></html>`)
	for _, term := range []string{"max_tokens_limit", "foo_bar", "a*b", "<div>"} {
		if out := renderFind(d, d.Root, term, WebFetchLimit); !strings.Contains(out, "1 match,") {
			t.Errorf("find %q: %q", term, out)
		}
	}
}

// Lower-casing can change a rune's byte length; the excerpt must not panic.
func TestFindExcerptAfterCaseChangingRunes(t *testing.T) {
	d := assembleDoc("T", "", []rawSection{{ID: "a", Title: "A", HNum: 2, HIndex: -1,
		Body: strings.Repeat("Ⱥ", 2000) + " needle " + strings.Repeat("İ", 500)}})
	out := renderFind(d, d.Root, "needle", WebFetchLimit)
	if !strings.Contains(out, "needle") {
		t.Errorf("excerpt lost the hit: %.200q", out)
	}
}

func TestContentRootMainHoldsOnlyAnIntro(t *testing.T) {
	html := `<html><body><nav>` + strings.Repeat(`<a href="/x">NavItem</a>`, 50) + `</nav>
<main><h1>Title</h1><p>` + strings.Repeat("intro ", 50) + `</p></main>
<div class="content">` + strings.Repeat("<p>ArticleFact paragraph with real content.</p>", 200) + `</div></body></html>`
	root := contentRoot(docFrom(t, html), false)
	if !strings.Contains(root.Text(), "ArticleFact") || strings.Contains(root.Text(), "NavItem") {
		t.Errorf("article dropped or nav kept (%d chars)", len(root.Text()))
	}
}

func TestContentRootTextInAside(t *testing.T) {
	html := `<html><body><main><p>Hi.</p></main><aside>` + strings.Repeat("AsideText sentence. ", 45) + `</aside></body></html>`
	if root := contentRoot(docFrom(t, html), false); !strings.Contains(root.Text(), "AsideText") {
		t.Errorf("aside text dropped: %q", root.Text())
	}
}

func TestOutlinePointsToALongLead(t *testing.T) {
	d := assembleDoc("Doc", strings.Repeat("lead paragraph text. ", 2400), []rawSection{
		{ID: "end", Title: "End", HNum: 2, HIndex: -1, Body: "short"}})
	out := renderSection(d, d.Root, 0, WebFetchLimit)
	if !strings.Contains(out, "read it with part=1") {
		t.Errorf("no pointer to the lead:\n%.1500s", out)
	}
}
