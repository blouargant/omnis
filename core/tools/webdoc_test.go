package tools

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func htmlDocFromFixture(t *testing.T, name string) *webDoc {
	t.Helper()
	doc := loadFixture(t, name)
	markHeadings(doc)
	d, err := buildHTMLDoc(contentRoot(doc, false), doc.Find("title").Text())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func htmlDocFromString(t *testing.T, html string) *webDoc {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	markHeadings(doc)
	d, err := buildHTMLDoc(contentRoot(doc, false), doc.Find("title").Text())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustLookup(t *testing.T, d *webDoc, ref string) *webSection {
	t.Helper()
	s, ok := d.Lookup(ref)
	if !ok {
		t.Fatalf("Lookup(%q) not found", ref)
	}
	return s
}

func TestBuildHTMLDocIDs(t *testing.T) {
	mk := htmlDocFromFixture(t, "mkdocs.html")
	b := mustLookup(t, mk, "b")
	if b.Title != "Sub B" {
		t.Errorf("mkdocs b title = %q, want %q", b.Title, "Sub B")
	}
	if !strings.Contains(b.Own, "Beta paragraph") {
		t.Errorf("mkdocs b own = %q", b.Own)
	}

	db := htmlDocFromFixture(t, "docbook.html")
	iso := mustLookup(t, db, "TRANSACTION-ISO")
	if len(iso.Children) != 1 || iso.Children[0].ID != "XACT-READ-COMMITTED" {
		t.Errorf("TRANSACTION-ISO children = %+v", iso.Children)
	}

	sp := htmlDocFromFixture(t, "sphinx.html")
	mustLookup(t, sp, "top")
	if sub := mustLookup(t, sp, "sub1"); sub.Title != "Sphinx Sub" {
		t.Errorf("sphinx sub1 title = %q", sub.Title)
	}
}

func TestBuildHTMLDocStructure(t *testing.T) {
	d := htmlDocFromFixture(t, "noids.html")
	first := mustLookup(t, d, "s1")
	if first.Title != "First" {
		t.Fatalf("s1 = %q", first.Title)
	}
	deep := mustLookup(t, d, "s1.1")
	if deep.Parent != first || deep.Title != "Deep" || deep.Level != 2 {
		t.Errorf("Deep: parent=%v title=%q level=%d", deep.Parent == first, deep.Title, deep.Level)
	}
	if !strings.Contains(first.Own, "First own paragraph") || strings.Contains(first.Own, "Deep own") {
		t.Errorf("First.Own = %q", first.Own)
	}
	if !strings.Contains(deep.Own, "Deep own paragraph") || !strings.Contains(deep.Own, "Still in deep") {
		t.Errorf("Deep.Own = %q", deep.Own)
	}
	if s := mustLookup(t, d, "s2"); s.Title != "Second" {
		t.Errorf("s2 = %q", s.Title)
	}
	if mustLookup(t, d, "dup").Title != "Dup One" || mustLookup(t, d, "dup-2").Title != "Dup Two" {
		t.Error("duplicate ids not suffixed")
	}
	found := false
	for _, s := range first.Children {
		if s.Title == "--gpu-memory-utilization" {
			found = true
		}
	}
	if !found {
		t.Errorf("code heading title not clean: %+v", first.Children)
	}
	if !strings.Contains(d.Root.Own, "Lead text") {
		t.Errorf("root own = %q", d.Root.Own)
	}
}

func TestBuildHTMLDocSingleH1Promoted(t *testing.T) {
	d := htmlDocFromString(t, `<html><head><title>Tab</title></head><body><main>
<h1>Real Title</h1><p>Before the first h2.</p><h2>One</h2><p>one</p><h2>Two</h2><p>two</p></main></body></html>`)
	if d.Title != "Real Title" {
		t.Errorf("title = %q", d.Title)
	}
	if len(d.Root.Children) != 2 || d.Root.Children[0].Title != "One" || d.Root.Children[0].Level != 1 {
		t.Errorf("children = %+v", d.Root.Children)
	}
	if !strings.Contains(d.Root.Own, "Before the first h2") {
		t.Errorf("own = %q", d.Root.Own)
	}
}

func TestSectionSizes(t *testing.T) {
	d := htmlDocFromFixture(t, "noids.html")
	if d.Root.Size() != len(d.Root.Markdown()) {
		t.Errorf("root size %d != len(markdown) %d", d.Root.Size(), len(d.Root.Markdown()))
	}
	var walk func(s *webSection) int
	walk = func(s *webSection) int {
		sum, n := 0, 0
		for _, c := range s.Children {
			sum += c.Size()
			n += 1 + walk(c)
		}
		if s.Size() < sum {
			t.Errorf("%q size %d < children %d", s.Title, s.Size(), sum)
		}
		if s.Descendants() != n {
			t.Errorf("%q descendants %d, want %d", s.Title, s.Descendants(), n)
		}
		return n
	}
	walk(d.Root)
	if d.Root.Descendants() != 6 {
		t.Errorf("root descendants = %d, want 6", d.Root.Descendants())
	}
}

func TestLookup(t *testing.T) {
	d := htmlDocFromString(t, `<html><body><article>
<h2 id="xact-read-committed">Read Committed</h2><p>a</p>
<h2 id="Layer">Layer Behavior with EP Enabled</h2><p>b</p>
<h2>Same</h2><p>c</p><h2>same</h2><p>d</p></article></body></html>`)
	for _, ref := range []string{"xact-read-committed", "XACT-READ-COMMITTED", "#xact-read-committed", "layer behavior with ep enabled", "Layer"} {
		if _, ok := d.Lookup(ref); !ok {
			t.Errorf("Lookup(%q) not found", ref)
		}
	}
	if _, ok := d.Lookup("same"); ok {
		t.Error("ambiguous title should not be found")
	}
	if _, ok := d.Lookup("nothing"); ok {
		t.Error("unknown ref found")
	}
}
