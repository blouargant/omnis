package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// parsePageSpec reads a page selection such as "3", "3-5", "1,4,7-9" or
// "10-" (to the end) against a PDF of n pages. Pages come back sorted and
// without repeats.
func parsePageSpec(spec string, n int) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	add := func(p int) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, item := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		item = strings.NewReplacer("–", "-", "—", "-", "..", "-").Replace(item)
		lo, hi, isRange := strings.Cut(item, "-")
		first, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("%q is not a page number or range", item)
		}
		last := first
		if isRange {
			if strings.TrimSpace(hi) == "" {
				last = n
			} else if last, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("%q is not a page number or range", item)
			}
		}
		if first < 1 || last < first {
			return nil, fmt.Errorf("%q is not a valid page range", item)
		}
		if first > n {
			return nil, fmt.Errorf("page %d out of range: the PDF has %d pages", first, n)
		}
		for p := first; p <= min(last, n); p++ {
			add(p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no page given")
	}
	for i := 1; i < len(out); i++ { // insertion sort: lists are short
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// formatPageList writes pages back as ranges: [1 2 3 7] -> "1-3, 7".
func formatPageList(pages []int) string {
	var parts []string
	for i := 0; i < len(pages); {
		j := i
		for j+1 < len(pages) && pages[j+1] == pages[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(pages[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", pages[i], pages[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// renderPages returns the given pages of a PDF, each headed by its marker,
// within limit. Pages that do not fit are named in the header so the next
// call can ask for them; a single page longer than the limit is cut.
func renderPages(d *webDoc, pages []int, limit int) string {
	total := len(d.Pages)
	room := limit - partReserve
	var b strings.Builder
	shown := 0
	cut := false
	for _, n := range pages {
		piece := pdfPageMarker(n) + "\n\n" + strings.TrimSpace(d.Pages[n-1])
		sep := ""
		if b.Len() > 0 {
			sep = "\n\n"
		}
		if b.Len()+len(sep)+len(piece) > room {
			if shown == 0 {
				piece = truncateAt(piece, room)
				b.WriteString(piece)
				shown, cut = 1, true
			}
			break
		}
		b.WriteString(sep)
		b.WriteString(piece)
		shown++
	}
	head := fmt.Sprintf("[Pages %s of %d]", formatPageList(pages[:shown]), total)
	switch {
	case cut:
		head = fmt.Sprintf("[Page %d of %d, cut at the limit (%d characters): search it with find=\"<text>\" and pages=\"%d\" for the rest.", pages[0], total, limit, pages[0])
		if len(pages) > 1 {
			head += fmt.Sprintf(" Then fetch pages=%q.", formatPageList(pages[1:]))
		}
		head += "]"
	case shown < len(pages):
		head = fmt.Sprintf("[Pages %s of %d shown; the rest did not fit: fetch pages=%q next.]",
			formatPageList(pages[:shown]), total, formatPageList(pages[shown:]))
	}
	return head + "\n\n" + b.String()
}
