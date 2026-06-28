// parse.go — turns a human schedule spec into resolved scheduling fields.
//
// Accepted forms (case-insensitive, leading/trailing space ignored):
//
//	30m              every 30m                  → recurring interval
//	2h               every 2h                   → recurring interval
//	in 90m                                       → one-shot, now+90m
//	at 2026-06-29T09:00                          → one-shot, absolute (local)
//	at 2026-06-29 09:00                          → one-shot, absolute (local)
//	at 09:00                                     → one-shot, next 09:00 (today/tomorrow)
//	0 9 * * 1-5      */15 * * * *                → recurring 5-field cron
//
// Recurring intervals are clamped to a 30s floor to keep a runaway /loop from
// hammering the model.
package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// MinInterval is the smallest allowed recurring interval. A shorter spec is
// rejected so a /loop cannot fire faster than this.
const MinInterval = 30 * time.Second

// cronParser parses standard 5-field cron expressions (minute hour dom month
// dow). Shared package-level so every parse/next computation agrees.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Spec is the resolved scheduling of a job. Exactly one of Interval / Cron / At
// is set: Interval and Cron are recurring, At is a one-shot.
type Spec struct {
	Interval time.Duration `json:"interval,omitempty"`
	Cron     string        `json:"cron,omitempty"`
	At       time.Time     `json:"at,omitempty"`
}

// atLayouts are tried in order for an "at <time>" one-shot.
var atLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseSpec resolves a raw spec string against now. The returned Spec is what
// the Job stores; `now` only matters for relative ("in …") and time-only
// ("at 09:00") one-shots.
func ParseSpec(raw string, now time.Time) (Spec, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Spec{}, fmt.Errorf("empty schedule spec")
	}
	low := strings.ToLower(s)

	switch {
	case strings.HasPrefix(low, "every "):
		d, err := time.ParseDuration(strings.TrimSpace(s[len("every "):]))
		if err != nil {
			return Spec{}, fmt.Errorf("invalid interval %q: %w", s, err)
		}
		return intervalSpec(d)

	case strings.HasPrefix(low, "in "):
		d, err := time.ParseDuration(strings.TrimSpace(s[len("in "):]))
		if err != nil {
			return Spec{}, fmt.Errorf("invalid relative time %q: %w", s, err)
		}
		if d <= 0 {
			return Spec{}, fmt.Errorf("relative time must be positive: %q", s)
		}
		return Spec{At: now.Add(d)}, nil

	case strings.HasPrefix(low, "at "):
		at, err := parseAt(strings.TrimSpace(s[len("at "):]), now)
		if err != nil {
			return Spec{}, err
		}
		return Spec{At: at}, nil
	}

	// Bare duration ("30m", "2h") → recurring interval.
	if d, err := time.ParseDuration(s); err == nil {
		return intervalSpec(d)
	}

	// Otherwise it must be a cron expression.
	if _, err := cronParser.Parse(s); err != nil {
		return Spec{}, fmt.Errorf("unrecognised schedule %q (expected a duration like \"30m\", \"in 90m\", \"at 09:00\", or a cron expression like \"0 9 * * 1-5\")", raw)
	}
	return Spec{Cron: s}, nil
}

func intervalSpec(d time.Duration) (Spec, error) {
	if d < MinInterval {
		return Spec{}, fmt.Errorf("interval %s is below the %s minimum", d, MinInterval)
	}
	return Spec{Interval: d}, nil
}

// parseAt resolves an absolute or time-only "at" target. A time-only value
// (e.g. "09:00") resolves to the next such moment at/after now.
func parseAt(v string, now time.Time) (time.Time, error) {
	// Time-only forms: next occurrence today, else tomorrow.
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, v, now.Location()); err == nil {
			candidate := time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), 0, now.Location())
			if !candidate.After(now) {
				candidate = candidate.Add(24 * time.Hour)
			}
			return candidate, nil
		}
	}
	for _, layout := range atLayouts {
		if t, err := time.ParseInLocation(layout, v, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q (try RFC3339, \"2006-01-02 15:04\", or \"15:04\")", v)
}
