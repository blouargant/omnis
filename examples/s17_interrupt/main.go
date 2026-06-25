// Component s17 — interrupts. The user can hit Ctrl-C and
// the run is cancelled cleanly. We wire context.Cancel on SIGINT.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s17_interrupt",
		Description: "Cancellable agent.",
		Model:       llm,
	})
	must(err)
	r, err := agentkit.Runner("s17", a)
	must(err)
	prompt := "Write a 1000-word essay about agentic systems."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	if err := stream.Print(os.Stdout, agentkit.RunOnceStream(ctx, r, prompt)); err != nil {
		fmt.Fprintln(os.Stderr, "run ended:", err)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
