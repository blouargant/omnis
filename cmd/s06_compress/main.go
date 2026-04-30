// Component s06 — context compression (Phase 2 / s06). Plugs the
// summariser-on-threshold AfterModelCallback. With a tiny threshold the
// effect is observable on a single demo run.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
	fstools "github.com/blouargant/agent-toolkit/core/tools"
	"github.com/blouargant/agent-toolkit/internal/compress"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	plug, err := compress.Plugin("compress", compress.Config{
		Threshold:  500, // tiny so the demo triggers at all
		MemoryPath: ".agent_memory.md",
		LLM:        llm,
	})
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s06_compress",
		Model: llm,
		Tools: fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s06", a, plug)
	must(err)
	prompt := "Write a 200-word essay about agent harnesses, then list 5 key terms."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
	fmt.Println("(see .agent_memory.md for the compressed summary)")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
