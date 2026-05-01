// Package stream is a minimal helper to print streaming agent events to a
// terminal (Phase 4 / s13). The launcher in the root binary handles its own UI;
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
//
// When SSE streaming is enabled, ADK emits a sequence of Partial=true text
// deltas followed by a Partial=false aggregated event with the same text.
// We render the deltas as they arrive and suppress the aggregated final
// text event so it doesn't get printed twice.
func Print(w io.Writer, seq iter.Seq2[*session.Event, error]) error {
	sawPartialText := false
	for ev, err := range seq {
		if err != nil {
			return err
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		isPartial := ev.LLMResponse.Partial
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" {
				// Skip the aggregated final text that follows a partial run.
				if !isPartial && sawPartialText && p.FunctionCall == nil {
					continue
				}
				fmt.Fprint(w, p.Text)
				if isPartial {
					sawPartialText = true
				}
			}
			if p.FunctionCall != nil {
				fmt.Fprintf(w, "\n[tool_call %s %v]\n", p.FunctionCall.Name, p.FunctionCall.Args)
				sawPartialText = false
			}
			if p.FunctionResponse != nil {
				fmt.Fprintf(w, "\n[tool_result %s %v]\n", p.FunctionResponse.Name, p.FunctionResponse.Response)
				sawPartialText = false
			}
		}
		if !isPartial {
			sawPartialText = false
		}
	}
	fmt.Fprintln(w)
	return nil
}
