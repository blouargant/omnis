// Component s01 — "the loop" (article Phase 1 / s01).
// We ask the question; ADK runs the model→tool→model loop until the
// agent stops requesting tool calls. No tools are added so the loop
// simply terminates after one model turn.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s01_loop",
		Description: "Bare agent loop demo.",
		Model:       llm,
	})
	must(err)
	r, err := agentkit.Runner("s01", a)
	must(err)
	prompt := "Say hello and tell me what 'the agent loop' means in one sentence."
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
