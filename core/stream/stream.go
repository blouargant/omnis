// Package stream is a minimal helper to print streaming agent events to a
// terminal (Phase 4 / s13). The launcher in cmd/full handles its own UI;
// stream is for the per-component cmd/sXX demos that just want to print.
package stream

import (
	"fmt"
	"io"
	"iter"

	"google.golang.org/adk/session"
)

// Print drains an event iterator returned by runner.Run and writes each
// text part to w. Returns the first error from the iterator (if any).
func Print(w io.Writer, seq iter.Seq2[*session.Event, error]) error {
	for ev, err := range seq {
		if err != nil {
			return err
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" {
				fmt.Fprint(w, p.Text)
			}
			if p.FunctionCall != nil {
				fmt.Fprintf(w, "\n[tool_call %s %v]\n", p.FunctionCall.Name, p.FunctionCall.Args)
			}
			if p.FunctionResponse != nil {
				fmt.Fprintf(w, "\n[tool_result %s]\n", p.FunctionResponse.Name)
			}
		}
	}
	fmt.Fprintln(w)
	return nil
}
