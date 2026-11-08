package tools

import (
	"strings"
	"testing"
)

func TestBuildMarkdownDoc(t *testing.T) {
	md := "# T\nintro\n## A\nfirst a\n```\n# not a heading\n```\n## A\nsecond a\n"
	d := buildMarkdownDoc(md, "file.md")
	if d.Title != "T" {
		t.Errorf("title = %q", d.Title)
	}
	if len(d.Root.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(d.Root.Children))
	}
	a, a1 := d.Root.Children[0], d.Root.Children[1]
	if a.ID != "a" || a1.ID != "a-1" {
		t.Errorf("ids = %q, %q", a.ID, a1.ID)
	}
	if !strings.Contains(a.Own, "# not a heading") {
		t.Errorf("fenced line left A: %q", a.Own)
	}
	if !strings.Contains(d.Root.Own, "intro") {
		t.Errorf("root own = %q", d.Root.Own)
	}
}

func TestBuildMarkdownDocSlugs(t *testing.T) {
	d := buildMarkdownDoc("## Hello, World!\nx\n## `--max-model-len`\ny\n", "")
	if d.Root.Children[0].ID != "hello-world" || d.Root.Children[1].ID != "--max-model-len" {
		t.Errorf("ids = %q, %q", d.Root.Children[0].ID, d.Root.Children[1].ID)
	}
	if d.Root.Children[1].Title != "--max-model-len" {
		t.Errorf("title = %q", d.Root.Children[1].Title)
	}
}

func TestBuildTextAndPDFDoc(t *testing.T) {
	d := buildTextDoc("plain text\n\nmore")
	if d.Root.Own != "plain text\n\nmore" || len(d.Root.Children) != 0 || d.Root.Size() == 0 {
		t.Errorf("text doc = %+v", d.Root)
	}
	p := buildPDFDoc([]string{"page one", "page two"})
	if len(p.Pages) != 2 || !strings.Contains(p.Root.Own, "page two") {
		t.Errorf("pdf doc = %+v", p.Root)
	}
}
