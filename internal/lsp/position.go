// position.go — translation between symbol names and LSP positions, including
// the UTF-16 column math LSP mandates. LSP Position.Character is a UTF-16
// code-unit offset (unless the server negotiates positionEncoding=utf-8, LSP
// 3.17), while Go strings are UTF-8 bytes — so a column can't be a byte index
// when a line contains non-BMP runes. The name-based tools resolve a symbol to
// a position here so the LLM never deals with coordinates.
package lsp

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// utf16Len returns the number of UTF-16 code units in s (runes above U+FFFF
// count as two — a surrogate pair).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isIdentByte reports whether b can be part of a typical source identifier.
func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// locateWord finds the first whole-identifier occurrence of word in content and
// returns its zero-based (line, UTF-16 character) position. "Whole-identifier"
// means it is not flanked by identifier characters, so "Client" does not match
// inside "NewClientPool". Used as the fallback when a symbol is referenced (not
// declared) in a file and so isn't in its documentSymbol outline.
func locateWord(content, word string) (Position, bool) {
	if word == "" {
		return Position{}, false
	}
	line, start := 0, 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			if col, ok := wordColumnUTF16(content[start:i], word); ok {
				return Position{Line: line, Character: col}, true
			}
			line++
			start = i + 1
		}
	}
	return Position{}, false
}

// byteOffset converts an LSP Position (line, UTF-16 character) to a byte offset
// in content. It walks pos.Line newlines, then advances pos.Character UTF-16
// code units within that line. A position past a line's end clamps to the line
// end. Returns false only when the line index is out of range.
func byteOffset(content string, pos Position) (int, bool) {
	i, line := 0, 0
	for line < pos.Line {
		nl := strings.IndexByte(content[i:], '\n')
		if nl < 0 {
			return 0, false
		}
		i += nl + 1
		line++
	}
	col := 0
	for col < pos.Character {
		if i >= len(content) || content[i] == '\n' {
			return i, true // clamp to end of line
		}
		r, size := utf8.DecodeRuneInString(content[i:])
		if r > 0xFFFF {
			col += 2
		} else {
			col++
		}
		i += size
	}
	return i, true
}

// applyTextEdits applies edits to content and returns the new content. Edits are
// applied in descending start order so an earlier splice never shifts the byte
// offsets of edits before it. Overlapping or out-of-range edits are skipped.
func applyTextEdits(content string, edits []TextEdit) string {
	type span struct {
		start, end int
		text       string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		s, ok1 := byteOffset(content, e.Range.Start)
		en, ok2 := byteOffset(content, e.Range.End)
		if !ok1 || !ok2 || s > en {
			continue
		}
		spans = append(spans, span{start: s, end: en, text: e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	b := []byte(content)
	prevStart := len(b) + 1
	for _, sp := range spans {
		if sp.start < 0 || sp.end > len(b) || sp.end > prevStart {
			continue // out of range or overlaps an already-applied (later) edit
		}
		nb := make([]byte, 0, len(b)-(sp.end-sp.start)+len(sp.text))
		nb = append(nb, b[:sp.start]...)
		nb = append(nb, sp.text...)
		nb = append(nb, b[sp.end:]...)
		b = nb
		prevStart = sp.start
	}
	return string(b)
}

// wordColumnUTF16 returns the UTF-16 start column of word as a whole identifier
// within a single line.
func wordColumnUTF16(line, word string) (int, bool) {
	from := 0
	for from <= len(line)-len(word) {
		idx := strings.Index(line[from:], word)
		if idx < 0 {
			return 0, false
		}
		s := from + idx
		e := s + len(word)
		beforeOK := s == 0 || !isIdentByte(line[s-1])
		afterOK := e == len(line) || !isIdentByte(line[e])
		if beforeOK && afterOK {
			return utf16Len(line[:s]), true
		}
		from = s + 1
	}
	return 0, false
}
