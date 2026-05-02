package compress

import (
	"regexp"
	"strings"

	"google.golang.org/genai"
)

// taskSwitchKeepUserTurns controls how many recent user prompts the
// sniffer remembers per session.
const taskSwitchKeepUserTurns = 3

// imperativeVerbs are common task-starting verbs that hint a new sub-task
// is being requested.
var imperativeVerbs = map[string]struct{}{
	"add": {}, "build": {}, "fix": {}, "refactor": {}, "implement": {},
	"create": {}, "write": {}, "remove": {}, "delete": {}, "rename": {},
	"migrate": {}, "extract": {}, "split": {}, "merge": {}, "rewrite": {},
	"document": {}, "test": {},
}

// pathLike approximates "looks like a file path or symbol the previous
// task was working on". Anything containing a '/' or ending in a
// recognisable extension counts.
var pathLike = regexp.MustCompile(`(?i)\b[\w./-]+\.[a-z0-9]{1,6}\b|\b[\w-]+/[\w./-]+\b`)

// maybeMarkTaskSwitch inspects the most recent user turn and, if it looks
// like the start of a brand-new task (imperative verb + zero overlap with
// any path/identifier seen in the previous N user turns), flips the
// forceCompact flag. Conservative by design: false negatives (missed
// switches) are far preferable to false positives (mid-task
// compressions).
func maybeMarkTaskSwitch(st *sessionState, contents []*genai.Content) {
	if st == nil || len(contents) == 0 {
		return
	}
	last := contents[len(contents)-1]
	if last == nil || strings.ToLower(last.Role) != "user" {
		return
	}
	text := userText(last)
	if text == "" {
		return
	}
	st.mu.Lock()
	prev := append([]string(nil), st.recentUserTurns...)
	st.recentUserTurns = append(st.recentUserTurns, text)
	if len(st.recentUserTurns) > taskSwitchKeepUserTurns {
		st.recentUserTurns = st.recentUserTurns[len(st.recentUserTurns)-taskSwitchKeepUserTurns:]
	}
	st.mu.Unlock()

	if !startsWithImperative(text) {
		return
	}
	if len(prev) == 0 {
		return // no prior turn to compare against
	}
	newPaths := pathLike.FindAllString(strings.ToLower(text), -1)
	if len(newPaths) == 0 {
		return // no concrete subjects mentioned; not a clear task switch
	}
	prevJoined := strings.ToLower(strings.Join(prev, "\n"))
	for _, p := range newPaths {
		if strings.Contains(prevJoined, p) {
			return // overlaps with an earlier task — same context
		}
	}
	st.forceCompact.Store(true)
}

func userText(c *genai.Content) string {
	var b strings.Builder
	for _, p := range c.Parts {
		if p != nil && p.Text != "" {
			b.WriteString(p.Text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func startsWithImperative(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return false
	}
	// Scan the first few whitespace-delimited tokens; common adverbial
	// fillers ("now", "please", "then") often precede the real verb.
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == '.'
	})
	limit := 4
	if len(fields) < limit {
		limit = len(fields)
	}
	for i := 0; i < limit; i++ {
		if _, ok := imperativeVerbs[fields[i]]; ok {
			return true
		}
	}
	return false
}
