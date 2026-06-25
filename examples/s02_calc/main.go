// Component s02 — `calculate` tool. The model is unreliable at multi-step
// arithmetic; the calc tool offloads each expression to govaluate and
// returns the exact result.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
	fstools "github.com/blouargant/omnis/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)

	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s02_calc",
		Description: "Calculator tool demo.",
		Model:       llm,
		Instruction: "Use the calculate tool for every arithmetic step. " +
			"Never do the math in your head. Show each call's result inline.",
		Tools: fstools.NewCalcTools(),
	})
	must(err)
	r, err := agentkit.Runner("s02", a)
	must(err)

	prompt := "Compute (sqrt(2) + 3*4) ** 2 and then divide that by 7. Show your work."
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
