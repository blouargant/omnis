package scheduler

import "strings"

// SplitSpecPrompt separates a schedule spec from the prompt in a "/loop" or
// "/schedule" command argument. The spec is either a quoted string (for
// multi-word specs like `"in 90m"` or a cron expression) or the first
// whitespace-delimited token; everything after is the prompt. Shared by the CLI
// and TUI command handlers (the web UI mirrors this grammar in JS).
func SplitSpecPrompt(s string) (spec, prompt string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if s[0] == '"' || s[0] == '\'' {
		q := s[0]
		if end := strings.IndexByte(s[1:], q); end >= 0 {
			return s[1 : 1+end], strings.TrimSpace(s[1+end+1:])
		}
		return strings.Trim(s, "\"'"), ""
	}
	if sp := strings.IndexAny(s, " \t"); sp >= 0 {
		return s[:sp], strings.TrimSpace(s[sp:])
	}
	return s, ""
}

// FirstLine returns the first non-empty line of s, truncated for one-line
// display of a job's prompt.
func FirstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if len(ln) > 60 {
			return ln[:60] + "…"
		}
		return ln
	}
	return ""
}
