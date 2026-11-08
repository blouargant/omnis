package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func loadFixture(t *testing.T, name string) *goquery.Document {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "webfetch", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := goquery.NewDocumentFromReader(f)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestContentRoot(t *testing.T) {
	cases := []struct {
		fixture string
		raw     bool
		want    []string
		reject  []string
		check   func(t *testing.T, root *goquery.Selection)
	}{
		{fixture: "mkdocs.html", want: []string{"Alpha paragraph", "Beta paragraph", "KEEP-ASIDE"},
			reject: []string{"SideLink3", "TocEntryA", "SiteMenuHome", "MkFooter", "var x"}},
		{fixture: "mkdocs.html", raw: true, want: []string{"SideLink3", "Alpha paragraph"}},
		{fixture: "docsy.html", want: []string{"DocsyX paragraph", "DocsyIntro"},
			reject: []string{"FOOTER-TEXT", "SideLink3", "DocsyMenu"}},
		{fixture: "sphinx.html", want: []string{"SphinxSub paragraph"}, reject: []string{"SideLink3"},
			check: func(t *testing.T, root *goquery.Selection) {
				if root.Find("section#top").Length() != 1 {
					t.Errorf("sphinx root should hold section#top")
				}
			}},
		{fixture: "docbook.html", want: []string{"ReadCommitted paragraph", "IsoIntro"}},
		{fixture: "nav-only.html", check: func(t *testing.T, root *goquery.Selection) {
			if n := len(strings.TrimSpace(root.Text())); n < 1000 {
				t.Errorf("nav-only fallback text = %d chars, want >= 1000", n)
			}
		}},
	}
	for _, c := range cases {
		doc := loadFixture(t, c.fixture)
		root := contentRoot(doc, c.raw)
		text := root.Text()
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s raw=%v: missing %q", c.fixture, c.raw, w)
			}
		}
		for _, r := range c.reject {
			if strings.Contains(text, r) {
				t.Errorf("%s raw=%v: should not contain %q", c.fixture, c.raw, r)
			}
		}
		if c.check != nil {
			c.check(t, root)
		}
	}
}

func TestContentRootDoesNotMutateDocument(t *testing.T) {
	doc := loadFixture(t, "mkdocs.html")
	first := contentRoot(doc, false).Text()
	if !strings.Contains(doc.Text(), "SideLink3") {
		t.Fatal("contentRoot removed nodes from the document")
	}
	if second := contentRoot(doc, false).Text(); second != first {
		t.Error("second call returned a different root")
	}
}
