// Component s16 — event bus (Phase 4 / s16).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/events"
	"github.com/blouargant/agent-toolkit/core/stream"
	fstools "github.com/blouargant/agent-toolkit/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	bus := events.NewBus()
	logger, closeLog, err := events.FileLogger(".agent_events.log")
	must(err)
	defer closeLog()
	counter, counterH := events.NewCounter()
	for _, ev := range []string{
		events.EventBeforeTool, events.EventAfterTool,
		events.EventBeforeModel, events.EventAfterModel,
		events.EventToolError, events.EventSessionStart, events.EventSessionEnd,
	} {
		bus.On(ev, logger).On(ev, counterH)
	}
	plug, err := bus.Plugin("events")
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s16_events",
		Model: llm,
		Tools: fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s16", a, plug)
	must(err)
	prompt := "List the files in the current directory using bash."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
	fmt.Println("\nEvent counts:")
	fmt.Print(counter.Summary())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
