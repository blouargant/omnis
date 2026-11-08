package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
)

// webSection is one section of a fetched page: its heading, the text before
// its first sub-section, and its sub-sections.
type webSection struct {
	ID       string // copied from the page, or positional ("s2.3")
	Title    string // clean heading text; the page title for the document root
	Level    int    // 0 = document root, 1 = top-level section
	Own      string // Markdown before the first sub-section, heading line excluded
	Children []*webSection
	Parent   *webSection
	size     int // len(Markdown()), set by finalize
	count    int // number of descendants, set by finalize
}

// Markdown renders the section whole: its heading line, its own text and its
// sub-sections.
func (s *webSection) Markdown() string {
	var b strings.Builder
	s.writeMarkdown(&b)
	return collapseBlankLines(strings.TrimSpace(b.String()))
}

func (s *webSection) writeMarkdown(b *strings.Builder) {
	if s.Level > 0 {
		b.WriteString(strings.Repeat("#", min(s.Level+1, 6)))
		b.WriteString(" ")
		b.WriteString(s.Title)
		b.WriteString("\n\n")
	}
	if s.Own != "" {
		b.WriteString(s.Own)
		b.WriteString("\n\n")
	}
	for _, c := range s.Children {
		c.writeMarkdown(b)
	}
}

// Size is the length of Markdown().
func (s *webSection) Size() int { return s.size }

// Descendants counts the sub-sections at every level.
func (s *webSection) Descendants() int { return s.count }

// webDoc is a fetched page as a tree of sections.
type webDoc struct {
	Title     string
	Root      *webSection
	Pages     []string // PDF only: text per page
	byID      map[string]*webSection
	byHeading map[int]*webSection // data-omnis-h index → section (HTML only)
}

// rawSection is a heading and the text that follows it up to the next
// heading, before the tree is built.
type rawSection struct {
	ID     string
	Title  string
	HNum   int // heading level as written: 1..6
	HIndex int // data-omnis-h of the heading, -1 when not from HTML
	Body   string
}

// headingAttr numbers the headings of a page in document order, so a CSS
// selector that lands on a heading can find that heading's section.
const headingAttr = "data-omnis-h"

const headingSelector = "h1, h2, h3, h4, h5, h6"

// markHeadings numbers every heading of doc (data-omnis-h="0..n").
func markHeadings(doc *goquery.Document) {
	doc.Find(headingSelector).Each(func(i int, s *goquery.Selection) {
		s.SetAttr(headingAttr, strconv.Itoa(i))
	})
}

var sentinelRE = regexp.MustCompile(`OMNISSEC(\d+)END`)

// buildHTMLDoc splits root into sections at its headings. Each heading is
// replaced by a sentinel paragraph, the whole root is converted to Markdown
// once, and the Markdown is cut at the sentinels: converting each section on
// its own would lose the structure of the elements that span headings.
func buildHTMLDoc(root *goquery.Selection, pageTitle string) (*webDoc, error) {
	root = root.Clone()
	var parts []rawSection
	root.Find(headingSelector).Each(func(i int, h *goquery.Selection) {
		hnum, _ := strconv.Atoi(goquery.NodeName(h)[1:])
		hidx := -1
		if v, ok := h.Attr(headingAttr); ok {
			if n, err := strconv.Atoi(v); err == nil {
				hidx = n
			}
		}
		parts = append(parts, rawSection{ID: headingID(h), Title: headingTitle(h), HNum: hnum, HIndex: hidx})
		h.ReplaceWithHtml(fmt.Sprintf("<p>OMNISSEC%dEND</p>", i))
	})
	html, err := root.Html()
	if err != nil {
		return nil, err
	}
	md, err := htmltomarkdown.ConvertString(html)
	if err != nil {
		return nil, fmt.Errorf("converting to markdown: %w", err)
	}
	locs := sentinelRE.FindAllStringSubmatchIndex(md, -1)
	lead := md
	if len(locs) > 0 {
		lead = md[:locs[0][0]]
	}
	for k, loc := range locs {
		i, _ := strconv.Atoi(md[loc[2]:loc[3]])
		end := len(md)
		if k+1 < len(locs) {
			end = locs[k+1][0]
		}
		if i < len(parts) {
			parts[i].Body = md[loc[1]:end]
		}
	}
	return assembleDoc(strings.TrimSpace(pageTitle), lead, parts), nil
}

// headingID is the heading's own id, else the id of the section or div it
// opens (Sphinx and DocBook put the id on a wrapper).
func headingID(h *goquery.Selection) string {
	if id := strings.TrimSpace(h.AttrOr("id", "")); id != "" {
		return id
	}
	id := ""
	h.ParentsFiltered("section, div").EachWithBreak(func(_ int, p *goquery.Selection) bool {
		pid := strings.TrimSpace(p.AttrOr("id", ""))
		if pid == "" {
			return true
		}
		if first := p.Find(headingSelector).First(); first.Length() > 0 && first.Get(0) == h.Get(0) {
			id = pid
		}
		return false
	})
	return id
}

var spaceRE = regexp.MustCompile(`\s+`)

// headingTitle is the heading's text without permalink marks.
func headingTitle(h *goquery.Selection) string {
	c := h.Clone()
	c.Find("a.headerlink, a.anchor, .anchor-link, a.header-anchor").Remove()
	t := spaceRE.ReplaceAllString(strings.TrimSpace(c.Text()), " ")
	return strings.TrimSpace(strings.TrimRight(t, "¶#  "))
}

// assembleDoc builds the section tree from headings in document order. Levels
// are relative (an h4 right under an h2 is its child), a page whose only h1
// opens it becomes the document root, sections without an id get a
// positional one, and repeated ids are suffixed -2, -3.
func assembleDoc(title, lead string, parts []rawSection) *webDoc {
	d := &webDoc{Title: title}
	root := &webSection{Own: cleanBody(lead)}
	d.Root = root

	h1 := 0
	for _, p := range parts {
		if p.HNum == 1 {
			h1++
		}
	}
	if h1 == 1 && len(parts) > 0 && parts[0].HNum == 1 {
		p := parts[0]
		if p.Title != "" {
			d.Title = p.Title
		}
		root.ID = p.ID
		root.Own = cleanBody(lead + "\n\n" + p.Body)
		parts = parts[1:]
	}
	root.Title = d.Title

	type frame struct {
		s    *webSection
		hnum int
	}
	stack := []frame{{root, 0}}
	byHeading := map[int]*webSection{}
	for _, p := range parts {
		for len(stack) > 1 && stack[len(stack)-1].hnum >= p.HNum {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].s
		s := &webSection{ID: p.ID, Title: p.Title, Level: parent.Level + 1, Own: cleanBody(p.Body), Parent: parent}
		if s.Title == "" {
			s.Title = "(untitled)"
		}
		parent.Children = append(parent.Children, s)
		stack = append(stack, frame{s, p.HNum})
		if p.HIndex >= 0 {
			byHeading[p.HIndex] = s
		}
	}
	d.byHeading = byHeading
	assignIDs(root, "")
	d.finalize()
	return d
}

func cleanBody(s string) string { return collapseBlankLines(strings.TrimSpace(s)) }

// assignIDs gives every section without an id its position ("s2.3").
func assignIDs(s *webSection, path string) {
	for i, c := range s.Children {
		p := strconv.Itoa(i + 1)
		if path != "" {
			p = path + "." + p
		}
		if c.ID == "" {
			c.ID = "s" + p
		}
		assignIDs(c, p)
	}
}

// finalize de-duplicates ids, indexes them and computes sizes.
func (d *webDoc) finalize() {
	d.byID = map[string]*webSection{}
	var index func(s *webSection)
	index = func(s *webSection) {
		if s.ID != "" {
			id := s.ID
			for n := 2; d.byID[id] != nil; n++ {
				id = fmt.Sprintf("%s-%d", s.ID, n)
			}
			s.ID = id
			d.byID[id] = s
		}
		for _, c := range s.Children {
			index(c)
		}
	}
	index(d.Root)
	var measure func(s *webSection)
	measure = func(s *webSection) {
		s.count = 0
		for _, c := range s.Children {
			measure(c)
			s.count += 1 + c.count
		}
		s.size = len(s.Markdown())
	}
	measure(d.Root)
}

// Lookup finds a section by id (exact, then ignoring case, with or without a
// leading #), else by a title that only one section has.
func (d *webDoc) Lookup(ref string) (*webSection, bool) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if ref == "" {
		return nil, false
	}
	if s, ok := d.byID[ref]; ok {
		return s, true
	}
	var hit *webSection
	n := 0
	for id, s := range d.byID {
		if strings.EqualFold(id, ref) {
			hit, n = s, n+1
		}
	}
	if n == 1 {
		return hit, true
	}
	hit, n = nil, 0
	var walk func(s *webSection)
	walk = func(s *webSection) {
		for _, c := range s.Children {
			if strings.EqualFold(c.Title, ref) {
				hit, n = c, n+1
			}
			walk(c)
		}
	}
	walk(d.Root)
	if n == 1 {
		return hit, true
	}
	return nil, false
}
