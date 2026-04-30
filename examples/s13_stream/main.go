// Component s13 — streaming responses (Phase 4 / s13). Just demonstrates
// the stream printer; ADK already streams text chunks.
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
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s13_stream",
		Description: "Streaming-text demo.",
		Model:       llm,
	})
	must(err)
	r, err := agentkit.Runner("s13", a)
	must(err)
	prompt := "Stream a 5-line haiku about goroutines."
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
