// Component s07 — durable task graph (Phase 2 / s07).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	"github.com/blouargant/yoke/internal/tasks"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	g := tasks.New("")
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s07_tasks",
		Description: "Durable task-graph demo.",
		Instruction: "When asked to plan, use task_create to record each step (with depends_on if needed) before executing.",
		Model:       llm,
		Tools:       g.Tools(),
	})
	must(err)
	r, err := agentkit.Runner("s07", a)
	must(err)
	prompt := "Plan three tasks for shipping a Go CLI: 1) write README, 2) add tests (depends on README), 3) tag v1.0 (depends on tests). Then list them."
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
