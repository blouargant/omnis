package askuser

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// InstallStdinAsker attaches a stdin-based surface to the registry. When a
// question is registered, it immediately prompts on stderr and reads an answer
// from stdin. This is the fallback for console mode (no TUI, no web UI).
//
// The function is synchronous: it is called from the registry's notify callback
// in the same goroutine that emitted the question. Since Ask blocks on the
// registry channel, the prompt is presented before Ask returns.
func InstallStdinAsker(reg *Registry) {
	reg.SetNotify(func(q Question) {
		ans := promptStdin(q)
		// Errors are silently swallowed; the registry will handle a closed
		// channel as cancelled if the write never happens.
		_ = reg.Resolve(q.SessionID, q.ID, ans)
	})
}

func promptStdin(q Question) Answer {
	sc := bufio.NewScanner(os.Stdin)

	switch q.Kind {
	case KindConfirm, KindSingle:
		return promptChoice(sc, q)
	case KindMulti:
		return promptMulti(sc, q)
	case KindText:
		return promptText(sc, q)
	default:
		return promptText(sc, q)
	}
}

func promptChoice(sc *bufio.Scanner, q Question) Answer {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "[ask_user] %s\n", q.Prompt)
	for i, c := range q.Choices {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, c)
	}
	if q.AllowText {
		fmt.Fprintf(os.Stderr, "  (or type a custom answer)\n")
	}
	fmt.Fprintf(os.Stderr, "Your choice: ")

	if !sc.Scan() {
		return Answer{Cancelled: true}
	}
	input := strings.TrimSpace(sc.Text())
	if input == "" {
		if q.Default != "" {
			return Answer{Selected: []string{q.Default}}
		}
		return Answer{Cancelled: true}
	}
	// Accept numeric index.
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(q.Choices) {
		return Answer{Selected: []string{q.Choices[n-1]}}
	}
	// Accept exact match (case-insensitive).
	lower := strings.ToLower(input)
	for _, c := range q.Choices {
		if strings.ToLower(c) == lower {
			return Answer{Selected: []string{c}}
		}
	}
	// Free text if allowed.
	if q.AllowText {
		return Answer{Text: input}
	}
	// Fallback: treat as free text even when not explicitly allowed.
	return Answer{Selected: []string{input}}
}

func promptMulti(sc *bufio.Scanner, q Question) Answer {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "[ask_user] %s\n", q.Prompt)
	for i, c := range q.Choices {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, c)
	}
	fmt.Fprintf(os.Stderr, "  (enter numbers separated by commas, e.g. 1,3)\n")
	if q.AllowText {
		fmt.Fprintf(os.Stderr, "  (or type a custom answer)\n")
	}
	fmt.Fprintf(os.Stderr, "Your choices: ")

	if !sc.Scan() {
		return Answer{Cancelled: true}
	}
	input := strings.TrimSpace(sc.Text())
	if input == "" {
		if q.Default != "" {
			return Answer{Selected: []string{q.Default}}
		}
		return Answer{Cancelled: true}
	}

	var selected []string
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(part)
		if n, err := strconv.Atoi(part); err == nil && n >= 1 && n <= len(q.Choices) {
			selected = append(selected, q.Choices[n-1])
		} else if part != "" {
			// Try exact match.
			lower := strings.ToLower(part)
			matched := false
			for _, c := range q.Choices {
				if strings.ToLower(c) == lower {
					selected = append(selected, c)
					matched = true
					break
				}
			}
			if !matched && q.AllowText {
				return Answer{Text: part}
			}
		}
	}
	if len(selected) == 0 {
		return Answer{Cancelled: true}
	}
	return Answer{Selected: selected}
}

func promptText(sc *bufio.Scanner, q Question) Answer {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "[ask_user] %s\n", q.Prompt)
	if q.Default != "" {
		fmt.Fprintf(os.Stderr, "  (default: %s)\n", q.Default)
	}
	fmt.Fprintf(os.Stderr, "Your answer: ")

	if !sc.Scan() {
		return Answer{Cancelled: true}
	}
	input := strings.TrimSpace(sc.Text())
	if input == "" {
		if q.Default != "" {
			return Answer{Text: q.Default}
		}
		return Answer{Cancelled: true}
	}
	return Answer{Text: input}
}
