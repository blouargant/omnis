package tools

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var atxRE = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)

// isFence reports whether line opens or closes a fenced code block.
func isFence(line string) bool {
	t := strings.TrimLeft(line, " ")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// buildMarkdownDoc splits a Markdown file at its ATX headings (# …); a #
// line inside fenced code is not a heading. Ids are GitHub's anchors.
func buildMarkdownDoc(md, title string) *webDoc {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var parts []rawSection
	var cur strings.Builder
	lead := ""
	inFence := false
	flush := func() {
		if len(parts) == 0 {
			lead = cur.String()
		} else {
			parts[len(parts)-1].Body = cur.String()
		}
		cur.Reset()
	}
	slugs := map[string]int{}
	for _, line := range lines {
		if isFence(line) {
			inFence = !inFence
		}
		if !inFence {
			if m := atxRE.FindStringSubmatch(line); m != nil {
				flush()
				t := strings.ReplaceAll(m[2], "`", "")
				parts = append(parts, rawSection{ID: githubSlug(t, slugs), Title: t, HNum: len(m[1]), HIndex: -1})
				continue
			}
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	flush()
	return assembleDoc(title, lead, parts)
}

// githubSlug is the anchor GitHub gives a heading: lower case, letters,
// digits, - and _ kept, spaces as -, repeats suffixed -1, -2.
func githubSlug(title string, seen map[string]int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	s := b.String()
	n := seen[s]
	seen[s] = n + 1
	if n > 0 {
		return s + "-" + strconv.Itoa(n)
	}
	return s
}

// buildTextDoc wraps a text with no headings.
func buildTextDoc(text string) *webDoc {
	d := &webDoc{Root: &webSection{Own: cleanBody(text)}}
	d.finalize()
	return d
}

// buildPDFDoc wraps the text of a PDF; parts are cut on page boundaries.
func buildPDFDoc(pages []string) *webDoc {
	d := &webDoc{Root: &webSection{Own: cleanBody(strings.Join(pages, "\n\n"))}, Pages: pages}
	d.finalize()
	return d
}
