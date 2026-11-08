package tools

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// findMaxMatches caps how many passages a find returns.
const findMaxMatches = 20

// findExcerptMax caps the excerpt of one matching paragraph.
const findExcerptMax = 1500

// findContext is how many lines around a hit a code excerpt shows.
const findContext = 3

type findHit struct {
	label   string
	excerpt string
}

// renderFind returns the passages of s that contain term (literal, ignoring
// case), each labelled with the section it is in. It never returns the
// selection itself, even when it would fit.
func renderFind(d *webDoc, s *webSection, term string, limit int) string {
	scope := "the page"
	if s != d.Root {
		scope = fmt.Sprintf("section %q", s.Title)
	}
	needle, _ := foldForFind(term)
	var hits []findHit
	if s == d.Root && len(d.Pages) > 0 {
		for i, p := range d.Pages {
			label := fmt.Sprintf("page %d", i+1)
			for _, b := range mdBlocks(p) {
				hits = append(hits, blockHits(b, needle, label)...)
			}
		}
	} else {
		var walk func(x *webSection)
		walk = func(x *webSection) {
			label := "(top of page)"
			if x.Level > 0 {
				label = fmt.Sprintf("%s (section=%q)", breadcrumb(x), x.ID)
			}
			for _, b := range mdBlocks(x.Own) {
				hits = append(hits, blockHits(b, needle, label)...)
			}
			for _, c := range x.Children {
				walk(c)
			}
		}
		walk(s)
	}

	if len(hits) == 0 {
		out := fmt.Sprintf("[find %q in %s: no match.]", term, scope)
		if s.Size() > limit && len(s.Children) > 0 {
			out += "\n\n" + renderOutline(d, s, limit-len(out)-2)
		}
		return out
	}

	var body strings.Builder
	shown := 0
	room := limit - 120
	for _, h := range hits {
		piece := "\n\n--- " + h.label + "\n" + h.excerpt
		if shown == findMaxMatches || body.Len()+len(piece) > room {
			break
		}
		body.WriteString(piece)
		shown++
	}
	noun := "matches"
	if len(hits) == 1 {
		noun = "match"
	}
	return fmt.Sprintf("[find %q in %s: %d %s, showing %d.]", term, scope, len(hits), noun, shown) + body.String()
}

// breadcrumb is the titles from the top-level section down to s.
func breadcrumb(s *webSection) string {
	var path []string
	for p := s; p != nil && p.Level > 0; p = p.Parent {
		path = append([]string{p.Title}, path...)
	}
	return strings.Join(path, " > ")
}

// blockHits returns the matches of needle in one block: a table row with its
// header, a few lines of code around the hit, or the paragraph itself.
func blockHits(b, needle, label string) []findHit {
	folded, offsets := foldForFind(b)
	at := strings.Index(folded, needle)
	if needle == "" || at < 0 {
		return nil
	}
	lines := strings.Split(b, "\n")
	lowLines := make([]string, len(lines))
	for i, l := range lines {
		lowLines[i], _ = foldForFind(l)
	}
	switch {
	case len(lines) > 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "|") && isTableSeparator(lines[1]):
		var out []findHit
		for i := 2; i < len(lines); i++ {
			if strings.Contains(lowLines[i], needle) {
				out = append(out, findHit{label, lines[0] + "\n" + lines[1] + "\n" + lines[i]})
			}
		}
		if len(out) == 0 { // the hit is in the header
			out = append(out, findHit{label, lines[0] + "\n" + lines[1]})
		}
		return out
	case len(lines) > 1 && isFence(lines[0]):
		fence := strings.TrimSpace(lines[0])
		var out []findHit
		covered := -1
		for i := 1; i < len(lines); i++ {
			if i <= covered || isFence(lines[i]) || !strings.Contains(lowLines[i], needle) {
				continue
			}
			from, to := max(1, i-findContext), min(len(lines)-1, i+findContext+1)
			if isFence(lines[to-1]) {
				to--
			}
			covered = to - 1
			out = append(out, findHit{label, fence + "\n" + strings.Join(lines[from:to], "\n") + "\n" + fence[:3]})
		}
		return out
	}
	return []findHit{{label, excerptAround(b, offsets[at])}}
}

// markdownEntities are the entities html-to-markdown leaves in text.
var markdownEntities = map[string]string{"&lt;": "<", "&gt;": ">", "&amp;": "&", "&quot;": "\"", "&#39;": "'", "&nbsp;": " "}

// foldForFind returns s as find compares it: lower case, without the
// backslashes Markdown puts before punctuation (html-to-markdown writes
// max\_tokens for max_tokens) and with the common entities decoded. offsets[i]
// is the byte of s that folded byte i comes from, so an excerpt can be cut
// from the original text even where lower-casing changes a rune's length.
func foldForFind(s string) (string, []int) {
	var b strings.Builder
	offsets := make([]int, 0, len(s)+1)
	emit := func(r rune, from int) {
		n := b.Len()
		b.WriteRune(r)
		for i := n; i < b.Len(); i++ {
			offsets = append(offsets, from)
		}
	}
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] < utf8.RuneSelf && (unicode.IsPunct(rune(s[i+1])) || unicode.IsSymbol(rune(s[i+1]))) {
			i++
			continue
		}
		if s[i] == '&' {
			if j := strings.IndexByte(s[i:min(len(s), i+7)], ';'); j > 0 {
				if dec, ok := markdownEntities[s[i:i+j+1]]; ok {
					emit([]rune(dec)[0], i)
					i += j + 1
					continue
				}
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		emit(unicode.ToLower(r), i)
		i += size
	}
	offsets = append(offsets, len(s))
	return b.String(), offsets
}

// excerptAround cuts a long paragraph to findExcerptMax around position at.
func excerptAround(b string, at int) string {
	if len(b) <= findExcerptMax {
		return b
	}
	start := max(0, at-findExcerptMax/3)
	for start > 0 && start < len(b) && (b[start]&0xC0) == 0x80 {
		start--
	}
	out := truncateAt(b[start:], findExcerptMax)
	if start+len(out) < len(b) {
		out += "…"
	}
	if start > 0 {
		out = "…" + out
	}
	return out
}
