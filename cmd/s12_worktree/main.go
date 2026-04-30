// Component s12 — git worktree isolation (Phase 3 / s12). The agent owns
// the worktree_create / worktree_remove / worktree_merge tools.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
	"github.com/blouargant/agent-toolkit/internal/worktree"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	repo, _ := os.Getwd()
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s12_worktree",
		Description: "Worktree-aware agent.",
		Instruction: "Explain to the user what worktree_create/remove/merge do; do not execute them unless explicitly asked.",
		Model:       llm,
		Tools:       worktree.Tools(repo),
	})
	must(err)
	r, err := agentkit.Runner("s12", a)
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
