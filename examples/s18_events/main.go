// Component s18 — event bus.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	bus := events.NewBus()
	must(os.MkdirAll("logs", 0o755))
	logName := "agent_events_" + time.Now().Format("20060102_150405") + ".log"
	logger, closeLog, err := events.FileLogger(filepath.Join("logs", logName))
	must(err)
	defer closeLog()
	counter, counterH := events.NewCounter()
	for _, ev := range []string{
		events.EventBeforeTool, events.EventAfterTool,
		events.EventBeforeModel, events.EventAfterModel,
		events.EventToolError,
		events.EventSessionStart, events.EventSessionEnd,
		events.EventRunStart, events.EventRunEnd,
		events.EventCurateNow,
	} {
		bus.On(ev, logger).On(ev, counterH)
	}
	plug, err := bus.Plugin("events")
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s18_events",
		Model: llm,
		Tools: fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s18", a, plug)
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
