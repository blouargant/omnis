// Component s04 — streaming responses. Just demonstrates
// the stream printer; ADK already streams text chunks.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s04_stream",
		Description: "Streaming-text demo.",
		Model:       llm,
	})
	must(err)
	r, err := agentkit.Runner("s04", a)
	must(err)
	prompt := "Stream a 50-line haiku about goroutines."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnceStream(ctx, r, prompt)))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
