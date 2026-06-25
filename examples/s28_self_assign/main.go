// Component s28 — autonomous task self-assignment. One
// goroutine claims tasks and "completes" them via task_update; the lead
// agent watches via task_list.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
	"github.com/blouargant/omnis/internal/tasks"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	g := tasks.New("")

	// Seed three tasks.
	for _, d := range []string{"build", "test", "release"} {
		_, _ = g.Create(d, nil, tasks.PriorityMedium)
	}

	// Worker drains the queue.
	go func() {
		for {
			t, _ := g.ClaimNext("worker-1")
			if t == nil {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			_, _ = g.Update(t.ID, tasks.StatusDone, "ok")
		}
	}()

	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s28_self_assign",
		Description: "Watches a self-assigning task graph.",
		Instruction: "Call task_list to report progress.",
		Model:       llm,
		Tools:       g.Tools(),
	})
	must(err)
	r, err := agentkit.Runner("s28", a)
	must(err)
	prompt := "Report the current task graph status."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	// Give the worker a beat to drain.
	time.Sleep(500 * time.Millisecond)
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
	fmt.Println()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
