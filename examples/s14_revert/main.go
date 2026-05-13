// Component s14 — file-revert tool (Phase 4 / s14). It's already part of
// core/tools so we just demonstrate it.
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
		Name:        "s14_revert",
		Description: "Demonstrates the revert tool.",
		Instruction: "When asked, write to a file, modify it, then call revert and confirm contents.",
		Model:       llm,
		Tools:       fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s14", a)
	must(err)
	prompt := "Write demo.txt with 'first', then overwrite with 'second', then revert. Read the result."
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
