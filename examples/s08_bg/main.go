// Component s08 — background tasks with notifications (Phase 3 / s08).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/adk/tool"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
	"github.com/blouargant/agent-toolkit/internal/bg"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	q := bg.NewQueue(16)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s08_bg",
		Description: "Background-task demo.",
		Instruction: "When asked, start a background task with bash_background and report you'll be notified when done.",
		Model:       llm,
		Tools:       []tool.Tool{q.Tool()},
	})
	must(err)
	r, err := agentkit.Runner("s08", a)
	must(err)
	prompt := "Start a background task that runs `sleep 1 && echo done`."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
	// Drain notifications to demonstrate the queue.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			return
		default:
		}
		for _, n := range q.Drain() {
			fmt.Println(bg.FormatNotification(n))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// helper to convert a single tool into a slice
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
