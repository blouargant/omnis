// Component s18 — async runtime / parallel tool calls (Phase 5 / s18).
// ADK already dispatches function calls in the order the model emits
// them; this demo just shows several tool calls in one turn.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
	fstools "github.com/blouargant/agent-toolkit/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s18_parallel",
		Description: "Multiple tool calls in one turn.",
		Instruction: "When asked, call several read/glob/grep tools in the same turn.",
		Model:       llm,
		Tools:       fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s18", a)
	must(err)
	prompt := "Use glob to list *.go files, then read the first one's first 5 lines, then grep for 'package' in the same file. Do them all in one turn if possible."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
