// Component s14 — prompt-cache stats.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
	"github.com/blouargant/omnis/internal/cache"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	stats, plug, err := cache.Plugin("cache")
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s14_cache",
		Model: llm,
	})
	must(err)
	r, err := agentkit.Runner("s14", a, plug)
	must(err)
	prompt := "Repeat: hello."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
	fmt.Println(stats.Summary())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
