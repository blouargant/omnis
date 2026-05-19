// Component s05 — extended tool arsenal: bash,
// read, write, grep, glob, revert.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s05_tools",
		Description: "Bash + read + write + grep + glob + revert demo.",
		Model:       llm,
		Tools:       fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s05", a)
	must(err)
	prompt := "Use the tools to (1) write hello.txt with 'hi', (2) read it back, (3) revert it. Report each step."
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
