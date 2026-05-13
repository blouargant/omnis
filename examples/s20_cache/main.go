// Component s20 — prompt-cache stats (Phase 5 / s20).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	"github.com/blouargant/yoke/internal/cache"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	stats, plug, err := cache.Plugin("cache")
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s20_cache",
		Model: llm,
	})
	must(err)
	r, err := agentkit.Runner("s20", a, plug)
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
