// Package features renders the "What's new" feed for the web UI from an
// embedded FEATURES.md. The file is embedded (not read from disk) so the notes
// always match the running binary's version and survive every packaging channel
// with no extra bundling.
//
// The comparison is on the MINOR version only (A.B): the patch digit C in A.B.C
// is reserved for bug-fixes and is never feature-listed, so two builds that
// differ only in C show nothing new. Sections whose version is above the current
// build (work still in development) are filtered out, so unreleased notes never
// leak into a shipped build.
package features

import (
	_ "embed"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed FEATURES.md
var featuresMD string

// Section is one minor-version block parsed from FEATURES.md.
type Section struct {
	Version string // "1.7"
	Major   int
	Minor   int
	Summary string // the one-line headline after the em dash
	Items   []string
}

// ShownSection is a Section after range-filtering and compaction, ready for the
// client. Display is one of "full", "condensed", or "headline".
type ShownSection struct {
	Version string   `json:"version"`
	Summary string   `json:"summary"`
	Display string   `json:"display"`
	Items   []string `json:"items"`
	More    int      `json:"more"`
}

// Payload is the response for GET /api/whatsnew.
type Payload struct {
	Show     bool           `json:"show"`
	Current  string         `json:"current"`
	From     string         `json:"from"`
	Sections []ShownSection `json:"sections"`
}

// Compaction tiers, indexed from the newest shown section (0 = newest). Oldest
// versions are compacted the most so a long span (e.g. a fresh install catching
// up from 1.0) stays readable.
const (
	fullCount        = 2 // newest N sections: every bullet
	condensedThrough = 5 // sections [fullCount, condensedThrough): headline + a few bullets
	condensedItems   = 3 // how many bullets a condensed section keeps
)

var (
	headerRe = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	abRe     = regexp.MustCompile(`(\d+)\.(\d+)`)
	verRe    = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)
)

// Parse extracts the version sections from the embedded FEATURES.md, newest
// first.
func Parse() []Section { return parse(featuresMD) }

func parse(md string) []Section {
	var out []Section
	curIdx := -1
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := headerRe.FindStringSubmatch(line); m != nil {
			label, summary := splitHeader(m[1])
			ab := abRe.FindStringSubmatch(label)
			if ab == nil {
				curIdx = -1 // a non-version "## " heading ends the current section
				continue
			}
			out = append(out, Section{
				Version: ab[1] + "." + ab[2],
				Major:   atoi(ab[1]),
				Minor:   atoi(ab[2]),
				Summary: summary,
			})
			curIdx = len(out) - 1
			continue
		}
		if curIdx < 0 {
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "- ") {
			out[curIdx].Items = append(out[curIdx].Items, strings.TrimSpace(t[2:]))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Major != out[j].Major {
			return out[i].Major > out[j].Major
		}
		return out[i].Minor > out[j].Minor
	})
	return out
}

// splitHeader separates a "## " heading into its version label and the headline
// summary that follows an em dash (or a plain " - " hyphen as a fallback).
func splitHeader(h string) (label, summary string) {
	for _, sep := range []string{"—", " - "} {
		if i := strings.Index(h, sep); i >= 0 {
			return strings.TrimSpace(h[:i]), strings.TrimSpace(h[i+len(sep):])
		}
	}
	return strings.TrimSpace(h), ""
}

// WhatsNew builds the feed to show for a client currently on `current` whose
// last-seen version was `lastSeen`. It returns Show=false (empty feed) when the
// current version is unparseable (a "dev" build), when the client is already
// caught up, or when nothing falls in range.
func WhatsNew(current, lastSeen string) Payload {
	p := Payload{Current: cleanVersion(current), From: cleanVersion(lastSeen), Sections: []ShownSection{}}

	curMaj, curMin, ok := parseAB(current)
	if !ok {
		return p // no comparable version (e.g. "dev") → never prompt
	}
	fromMaj, fromMin, ok := parseAB(lastSeen)
	if !ok {
		fromMaj, fromMin = 1, 0 // "no version recorded ⇒ assume 1.0.0"
	}
	if !abGreater(curMaj, curMin, fromMaj, fromMin) {
		return p // A and B both unchanged (or older) — nothing to announce
	}

	shown := make([]ShownSection, 0)
	for _, s := range Parse() {
		if !abGreater(s.Major, s.Minor, fromMaj, fromMin) {
			continue // at or below what the user already saw
		}
		if abGreater(s.Major, s.Minor, curMaj, curMin) {
			continue // above the running build — still in development
		}
		shown = append(shown, compact(s, len(shown)))
	}
	if len(shown) == 0 {
		return p
	}
	p.Show = true
	p.Sections = shown
	return p
}

func compact(s Section, idx int) ShownSection {
	out := ShownSection{Version: s.Version, Summary: s.Summary, Items: []string{}}
	switch {
	case idx < fullCount:
		out.Display = "full"
		out.Items = append(out.Items, s.Items...)
	case idx < condensedThrough:
		out.Display = "condensed"
		if len(s.Items) > condensedItems {
			out.Items = append(out.Items, s.Items[:condensedItems]...)
			out.More = len(s.Items) - condensedItems
		} else {
			out.Items = append(out.Items, s.Items...)
		}
	default:
		out.Display = "headline"
		out.More = len(s.Items)
	}
	return out
}

// abGreater reports whether (aMaj,aMin) > (bMaj,bMin).
func abGreater(aMaj, aMin, bMaj, bMin int) bool {
	if aMaj != bMaj {
		return aMaj > bMaj
	}
	return aMin > bMin
}

// parseAB extracts major.minor from a version string, tolerating a leading "v"
// and any git-describe / pre-release suffix ("v1.7.0-14-g7e0989f", "1.0.0-rc5").
func parseAB(s string) (maj, min int, ok bool) {
	m := abRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	return atoi(m[1]), atoi(m[2]), true
}

// cleanVersion tidies a version for display: "v1.7.0-14-g7e0989f" → "1.7.0".
// Unparseable input (e.g. "dev") is returned unchanged.
func cleanVersion(s string) string {
	if m := verRe.FindStringSubmatch(s); m != nil {
		if m[3] != "" {
			return m[1] + "." + m[2] + "." + m[3]
		}
		return m[1] + "." + m[2]
	}
	return strings.TrimSpace(s)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
