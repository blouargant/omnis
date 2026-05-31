// Package fileref resolves "@path" file references typed in a chat prompt.
//
// A reference is an "@" at the start of the prompt or preceded by whitespace
// (so email addresses like "a@b.com" are NOT treated as references) followed
// by a non-whitespace path token. Referenced regular files have their content
// inlined into the agent's context; directories and missing paths are left as
// plain prompt text. The same grammar is mirrored client-side in web/app.js so
// rendered references line up with what the server inlines.
package fileref

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind classifies a resolved reference.
type Kind string

const (
	// KindMissing means the path does not exist; callers treat it as plain text.
	KindMissing Kind = "missing"
	KindFile    Kind = "file"
	KindDir     Kind = "dir"
)

// TrailingTrim is the set of trailing punctuation stripped from a token so a
// reference at the end of a sentence ("see @main.go.") resolves cleanly. It is
// duplicated verbatim in the web UI regex.
const TrailingTrim = `.,;:!?)]}>"'`

// maxFileBytes caps how much of each referenced file is inlined into context.
const maxFileBytes = 64 * 1024

// maxRefs caps how many file references are inlined per turn.
const maxRefs = 20

// Span is a located "@" reference within a prompt. Start/End are byte offsets
// of the "@token" text, excluding any trailing punctuation that was trimmed.
type Span struct {
	Start, End int
	Token      string // path token, after the leading "@" and trailing-punct trim
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

// Spans returns the located "@" references in prompt.
func Spans(prompt string) []Span {
	var out []Span
	for i := 0; i < len(prompt); i++ {
		if prompt[i] != '@' {
			continue
		}
		// Must be at the start or preceded by whitespace (excludes emails).
		if i > 0 && !isSpace(prompt[i-1]) {
			continue
		}
		j := i + 1
		for j < len(prompt) && !isSpace(prompt[j]) {
			j++
		}
		tok := strings.TrimRight(prompt[i+1:j], TrailingTrim)
		if tok == "" {
			i = j
			continue
		}
		out = append(out, Span{Start: i, End: i + 1 + len(tok), Token: tok})
		i = j
	}
	return out
}

// Tokens returns the path tokens of every "@" reference in prompt.
func Tokens(prompt string) []string {
	spans := Spans(prompt)
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Token
	}
	return out
}

// resolveAbs resolves token against cwd, expanding a leading "~".
func resolveAbs(token, cwd string) string {
	p := token
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + strings.TrimPrefix(p, "~")
		}
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			if wd, err := os.Getwd(); err == nil {
				cwd = wd
			}
		}
		p = filepath.Join(cwd, p)
	}
	return p
}

// Ref is one resolved "@" reference.
type Ref struct {
	Token string
	Abs   string // absolute resolved path (empty when KindMissing is unresolvable)
	Kind  Kind
}

// Classify resolves a single token against cwd and classifies it.
func Classify(token, cwd string) Ref {
	abs := resolveAbs(token, cwd)
	info, err := os.Stat(abs)
	switch {
	case err != nil:
		return Ref{Token: token, Kind: KindMissing}
	case info.IsDir():
		return Ref{Token: token, Abs: abs, Kind: KindDir}
	default:
		return Ref{Token: token, Abs: abs, Kind: KindFile}
	}
}

// Resolve classifies every "@" reference in prompt against cwd.
func Resolve(prompt, cwd string) []Ref {
	toks := Tokens(prompt)
	out := make([]Ref, 0, len(toks))
	for _, t := range toks {
		out = append(out, Classify(t, cwd))
	}
	return out
}

// Context builds a context block that inlines the contents of every referenced
// regular file (deduplicated, capped per file and in count). It returns "" when
// there is nothing to inline. The block is meant to be sent as an additional
// part of the user turn — separate from the persisted prompt text, so the raw
// "@reference" survives in the conversation history.
func Context(prompt, cwd string) string {
	seen := map[string]bool{}
	var b strings.Builder
	n := 0
	for _, ref := range Resolve(prompt, cwd) {
		if ref.Kind != KindFile || seen[ref.Abs] {
			continue
		}
		seen[ref.Abs] = true
		if n >= maxRefs {
			break
		}
		data, err := os.ReadFile(ref.Abs)
		if err != nil {
			continue
		}
		truncated := false
		if len(data) > maxFileBytes {
			data = data[:maxFileBytes]
			truncated = true
		}
		if n == 0 {
			b.WriteString(`The user referenced files with "@"; their contents are included below for context.` + "\n")
		}
		fmt.Fprintf(&b, "\n===== FILE: %s =====\n", ref.Token)
		b.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
		if truncated {
			fmt.Fprintf(&b, "===== (truncated at %d bytes) =====\n", maxFileBytes)
		}
		fmt.Fprintf(&b, "===== END FILE: %s =====\n", ref.Token)
		n++
	}
	return b.String()
}
