package tools

import "strings"

// mdBlocks splits Markdown at blank lines; a fenced code block is one block
// even when it holds blank lines.
func mdBlocks(md string) []string {
	var blocks []string
	var cur []string
	inFence := false
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(md, "\n") {
		if isFence(line) {
			inFence = !inFence
		}
		if !inFence && strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}

// splitParts cuts md into parts of at most max characters, packing whole
// blocks. A block too long on its own is cut by table rows (header repeated),
// by lines of a code block (each part a complete fence), by lines, and only
// as a last resort inside a line, never inside a UTF-8 sequence.
func splitParts(md string, max int) []string {
	var parts []string
	cur := ""
	add := func(piece string) {
		switch {
		case cur == "":
			cur = piece
		case len(cur)+2+len(piece) <= max:
			cur += "\n\n" + piece
		default:
			parts = append(parts, cur)
			cur = piece
		}
	}
	for _, b := range mdBlocks(md) {
		if len(b) <= max {
			add(b)
			continue
		}
		// Start the cut block in the part being filled, so a heading or a
		// short paragraph before it is not left alone in a part.
		first := max
		if cur != "" {
			first = max - len(cur) - 2
		}
		for _, piece := range splitBlock(b, max, first) {
			add(piece)
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

// splitBlock cuts one oversize block into pieces of at most max, the first
// one at most first.
func splitBlock(b string, max, first int) []string {
	lines := strings.Split(b, "\n")
	prefix, suffix := "", ""
	switch {
	case len(lines) > 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "|") && isTableSeparator(lines[1]):
		prefix = lines[0] + "\n" + lines[1] + "\n"
		lines = lines[2:]
	case len(lines) > 1 && isFence(lines[0]):
		prefix = lines[0] + "\n"
		last := len(lines) - 1
		if isFence(lines[last]) {
			suffix = "\n" + strings.TrimSpace(lines[last])
			lines = lines[1:last]
		} else {
			suffix = "\n" + strings.TrimSpace(lines[0])[:3]
			lines = lines[1:]
		}
	}
	full := max - len(prefix) - len(suffix)
	if full < 100 { // a header longer than a part: give up keeping it
		prefix, suffix, full = "", "", max
	}
	room := first - len(prefix) - len(suffix)
	if room < 100 {
		room = full
	}
	var out []string
	var cur []string
	size := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, prefix+strings.Join(cur, "\n")+suffix)
			cur, size = nil, 0
			room = full
		}
	}
	for _, line := range lines {
		for len(line) > room {
			flush()
			head := truncateAt(line, room)
			out = append(out, prefix+head+suffix)
			line = line[len(head):]
			room = full
		}
		if size > 0 && size+1+len(line) > room {
			flush()
		}
		if size > 0 {
			size++
		}
		cur = append(cur, line)
		size += len(line)
	}
	flush()
	return out
}

func isTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.Trim(t, "|-: ") == ""
}

// pdfPart is a slice of a PDF's text and the pages it covers (1-based).
type pdfPart struct {
	First, Last int
	Text        string
}

// pdfParts packs whole pages into parts of at most max; a page too long on
// its own is cut by lines.
func pdfParts(pages []string, max int) []pdfPart {
	var parts []pdfPart
	var cur *pdfPart
	for i, p := range pages {
		n := i + 1
		p = strings.TrimSpace(p)
		if len(p) > max {
			if cur != nil {
				parts = append(parts, *cur)
				cur = nil
			}
			for _, piece := range splitBlock(p, max, max) {
				parts = append(parts, pdfPart{First: n, Last: n, Text: piece})
			}
			continue
		}
		if cur != nil && len(cur.Text)+2+len(p) <= max {
			cur.Text += "\n\n" + p
			cur.Last = n
			continue
		}
		if cur != nil {
			parts = append(parts, *cur)
		}
		cur = &pdfPart{First: n, Last: n, Text: p}
	}
	if cur != nil {
		parts = append(parts, *cur)
	}
	return parts
}
