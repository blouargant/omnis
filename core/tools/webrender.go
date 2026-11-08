package tools

import (
	"fmt"
	"strings"
)

// WebFetchLimit is the most WebFetch returns in one result, notices
// included. The output shaper's WebFetch budget uses the same constant, so a
// result is never cut a second time.
const WebFetchLimit = 32000

// outlineBudget is how much of an outline the levels below the first may take
// before they are folded into a sub-section count.
const outlineBudget = 4000

// leadMax caps the text shown under the title of an outline.
const leadMax = 1000

// partReserve is the room left in a part for its header.
const partReserve = 300

// renderSection returns the selection whole when it fits, else its outline
// (when it has sub-sections) or its first part. part > 0 asks for that part
// whatever the selection holds.
func renderSection(d *webDoc, s *webSection, part int, limit int) string {
	if part <= 0 {
		if s.Size() <= limit {
			return s.Markdown()
		}
		if len(s.Children) > 0 {
			return renderOutline(d, s, limit)
		}
		part = 1
	}
	title := sectionTitle(d, s)
	if s == d.Root && len(d.Pages) > 0 {
		parts := pdfParts(d.Pages, limit-partReserve)
		if part > len(parts) {
			return fmt.Sprintf("error part %d out of range: %q has %d parts", part, title, len(parts))
		}
		p := parts[part-1]
		return fmt.Sprintf("[Part %d of %d (pages %d–%d of %d) — %s]\n\n%s",
			part, len(parts), p.First, p.Last, len(d.Pages), nextPart(part, len(parts)), p.Text)
	}
	parts := splitParts(s.Markdown(), limit-partReserve)
	if part > len(parts) {
		return fmt.Sprintf("error part %d out of range: %q has %d parts", part, title, len(parts))
	}
	return fmt.Sprintf("[Part %d of %d of %q — %s]\n\n%s", part, len(parts), title, nextPart(part, len(parts)), parts[part-1])
}

func nextPart(i, n int) string {
	if i >= n {
		return "last part."
	}
	return fmt.Sprintf("fetch part=%d for the next.", i+1)
}

func sectionTitle(d *webDoc, s *webSection) string {
	if s.Title != "" {
		return s.Title
	}
	if d.Title != "" {
		return d.Title
	}
	return "this page"
}

// renderOutline lists the sub-sections of s with their ids and sizes, as many
// levels deep as fit outlineBudget; the first level is always listed.
func renderOutline(d *webDoc, s *webSection, limit int) string {
	var b strings.Builder
	if s == d.Root {
		fmt.Fprintf(&b, "[Page too long to return whole: %d characters, limit %d.", s.Size(), limit)
	} else {
		fmt.Fprintf(&b, "[Section %q too long to return whole: %d characters, limit %d.", s.Title, s.Size(), limit)
	}
	b.WriteString(` You have NOT read its content yet. Fetch the part you need with section="<id>" (a section returns everything under it when it fits), or search it with find="<text>". Do not answer from this outline alone.]`)
	if s != d.Root {
		var path []string
		for p := s; p != nil && p != d.Root; p = p.Parent {
			path = append([]string{p.Title}, path...)
		}
		b.WriteString("\nPath: " + strings.Join(path, " > "))
	}
	b.WriteString("\n\n# " + sectionTitle(d, s))
	if lead := cutLead(s.Own); lead != "" {
		b.WriteString("\n" + lead)
	}
	b.WriteString("\n\n")

	height := subtreeHeight(s)
	var entries []string
	for depth := height; depth >= 1; depth-- {
		entries = outlineEntries(s, depth)
		if depth == 1 || joinedLen(entries) <= outlineBudget {
			break
		}
	}
	room := limit - b.Len()
	if joinedLen(entries) <= room {
		b.WriteString(strings.Join(entries, "\n"))
		return b.String()
	}
	// Too many top-level sections even for the limit: list what fits.
	room -= 120
	shown, used := 0, 0
	for _, e := range entries {
		if used+len(e)+1 > room {
			break
		}
		b.WriteString(e + "\n")
		used += len(e) + 1
		shown++
	}
	fmt.Fprintf(&b, "… and %d more top-level sections not shown: use find to locate one.", len(entries)-shown)
	return b.String()
}

// cutLead cuts the lead to leadMax on a line boundary when it can.
func cutLead(own string) string {
	own = strings.TrimSpace(own)
	if len(own) <= leadMax {
		return own
	}
	cut := truncateAt(own, leadMax)
	if i := strings.LastIndex(cut, "\n"); i > leadMax/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}

func subtreeHeight(s *webSection) int {
	h := 0
	for _, c := range s.Children {
		h = max(h, 1+subtreeHeight(c))
	}
	return h
}

// outlineEntries lists the sub-sections of s down to depth levels.
func outlineEntries(s *webSection, depth int) []string {
	var out []string
	var walk func(p *webSection, level int)
	walk = func(p *webSection, level int) {
		for _, c := range p.Children {
			e := fmt.Sprintf("%s- %s (%s) — %s", strings.Repeat("  ", level-1), c.Title, c.ID, fmtSize(c.Size()))
			if level == depth && len(c.Children) > 0 {
				e += fmt.Sprintf(", %d sub-sections", c.Descendants())
			}
			out = append(out, e)
			if level < depth {
				walk(c, level+1)
			}
		}
	}
	walk(s, 1)
	return out
}

func joinedLen(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	return n
}

// fmtSize rounds a size for an outline: 850, 2.1k, 96k.
func fmtSize(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		s := fmt.Sprintf("%.1f", float64(n)/1000)
		return strings.TrimSuffix(s, ".0") + "k"
	default:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
}
