// Component s24 — git worktree isolation. The agent owns
// the worktree_create / worktree_remove / worktree_merge tools.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
	"github.com/blouargant/omnis/internal/worktree"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	repo, _ := os.Getwd()
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s24_worktree",
		Description: "Worktree-aware agent.",
		Instruction: "Explain to the user what worktree_create/remove/merge do; do not execute them unless explicitly asked.",
		Model:       llm,
		Tools:       worktree.Tools(repo),
	})
	must(err)
	r, err := agentkit.Runner("s24", a)
	must(err)
	prompt := "Describe the three worktree tools you have and when to use each."
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
