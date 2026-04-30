// Component s19 — interrupts (Phase 5 / s19). The user can hit Ctrl-C and
// the run is cancelled cleanly. We wire context.Cancel on SIGINT.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s19_interrupt",
		Description: "Cancellable agent.",
		Model:       llm,
	})
	must(err)
	r, err := agentkit.Runner("s19", a)
	must(err)
	prompt := "Write a 1000-word essay about distributed systems."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	if err := stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)); err != nil {
		fmt.Fprintln(os.Stderr, "run ended:", err)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
