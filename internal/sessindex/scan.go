// scan.go — the no-index search path.
//
// Used in two situations, both of which must still return useful results:
//
//   - No embedder is configured. There is no index and never will be, so every
//     search reads the conversation files directly. Callers warn the user that
//     it may take a while.
//   - An embedder exists but the index is still cold (first ever search, or a
//     rebuild after the embedding model changed). Scanning answers the query now
//     while the index builds in the background, so the search box is never dead.
//
// Matching is literal, not semantic: a session matches when every query term
// appears in a turn's text (case-insensitive). Results are folded by session and
// ranked by match count, then recency — the same shape the semantic path
// returns, so the caller renders them identically.
package sessindex

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ScanOpts tunes a direct scan.
type ScanOpts struct {
	// K caps the number of sessions returned (default 10).
	K int
	// ExcludeArchived restricts the search to ACTIVE sessions. Archived sessions
	// are searched by default: the user archived them, they did not delete them,
	// and being able to find them again is most of the point of this feature.
	ExcludeArchived bool
}

// ScanStats reports what a scan cost, so the UI can be honest about it.
type ScanStats struct {
	Scanned int           `json:"scanned"` // sessions read
	Matched int           `json:"matched"` // sessions with at least one hit
	Took    time.Duration `json:"-"`
	TookMs  int64         `json:"took_ms"`
}

// snippetMax is how much context a result row shows.
const snippetMax = 280

// queryTerms lowercases and splits a query into the terms a scan must all find.
// Punctuation is stripped so "the k8s auditor?" matches "k8s auditor".
func queryTerms(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '_' && r != '-' && r != '.' && r != '/')
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// snippet extracts a short, readable excerpt from text, centred on the first
// query term when one is present (a literal match the user can see) and falling
// back to the head of the passage otherwise (a semantic hit has no matched term).
func snippet(text string, terms []string) string {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" {
		return ""
	}
	start := 0
	if len(terms) > 0 {
		if idx := strings.Index(strings.ToLower(flat), terms[0]); idx > 0 {
			start = idx - snippetMax/3
			if start < 0 {
				start = 0
			}
		}
	}
	runes := []rune(flat)
	if start > len(runes) {
		start = 0
	}
	end := start + snippetMax
	if end > len(runes) {
		end = len(runes)
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

// lexicalWeight is how much a literal term match adds to a semantic score.
//
// Pure vector similarity is weak exactly where a search BOX is strongest: people
// type two keywords ("azure AI"), and a short query embeds poorly against long
// conversational chunks. Observed live: the one session actually titled "Azure AI
// Subscription Tier Upgrade" ranked 5th (0.545) behind four sessions that merely
// talk about AI models in general (0.645, 0.598, …). Nudging a hit that contains
// the user's actual words puts it back on top without abandoning meaning-based
// ranking: a full literal match adds 0.3, which reorders near-ties but cannot
// promote a genuinely irrelevant hit past a strong semantic one.
const lexicalWeight = 0.3

// words splits text into a set of lowercase words, so a term matches whole words
// only. Substring matching would make short, common terms ("ai") match inside
// "said", "available", "again" — which is precisely the case this boost exists to
// get right.
func words(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		out[w] = true
	}
	return out
}

// lexicalOverlap is the fraction of the query's terms that appear verbatim in
// text (0 → no term, 1 → every term).
func lexicalOverlap(text string, terms []string) float32 {
	if len(terms) == 0 {
		return 0
	}
	set := words(text)
	found := 0
	for _, t := range terms {
		if set[t] {
			found++
		}
	}
	return float32(found) / float32(len(terms))
}

// scoreTurn counts how many of the query terms appear in text, and how often.
// Returns (0, false) unless EVERY term is present — an AND match, which is what
// a user typing several words expects.
func scoreTurn(text string, terms []string) (float32, bool) {
	if len(terms) == 0 {
		return 0, false
	}
	low := strings.ToLower(text)
	total := 0
	for _, t := range terms {
		n := strings.Count(low, t)
		if n == 0 {
			return 0, false
		}
		total += n
	}
	return float32(total), true
}

// Scan searches every persisted conversation directly, with no index. It is
// bounded only by the corpus size, so callers must warn the user before running
// it on a large history.
func Scan(ctx context.Context, query string, opts ScanOpts) ([]Hit, ScanStats, error) {
	started := time.Now()
	var stats ScanStats
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil, stats, nil
	}
	k := opts.K
	if k <= 0 {
		k = 10
	}

	var out []Hit
	for _, id := range listSessionIDs() {
		select {
		case <-ctx.Done():
			return nil, stats, ctx.Err()
		default:
		}
		c, _, err := loadConv(id)
		if err != nil || !c.searchable() {
			continue
		}
		if opts.ExcludeArchived && c.Archived {
			continue
		}
		stats.Scanned++

		best := Hit{Score: -1}
		for ti, t := range c.Turns {
			body := turnText(t)
			score, ok := scoreTurn(body, terms)
			if !ok || score <= best.Score {
				continue
			}
			best = Hit{
				SessionID: id,
				TurnIndex: ti,
				At:        t.At,
				Snippet:   snippet(body, terms),
				Score:     score,
			}
		}
		if best.Score > 0 {
			stats.Matched++
			out = append(out, best)
		}
	}

	// Rank by match count, then recency — a scan has no similarity signal, so a
	// recent conversation about the same words is the better guess.
	sort.Slice(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].At.After(out[b].At)
	})
	if len(out) > k {
		out = out[:k]
	}
	stats.Took = time.Since(started)
	stats.TookMs = stats.Took.Milliseconds()
	return out, stats, nil
}
