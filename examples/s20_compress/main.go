// Component s20 — intelligent context management. Wires
// the v2 compress plugin and the compact_now tool, with tiny window
// settings so the soft/hard triggers fire on a single demo run.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/compress"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	plug, compactTools, wait, err := compress.PluginWithTools("compress", compress.Config{
		WindowTokens: 800, // tiny so the demo triggers at all
		SoftRatio:    0.5,
		HardRatio:    0.8,
		AuditPath:    ".agent_memory.md",
		LLM:          llm,
	})
	must(err)
	tools := append(fstools.New(), compactTools...)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s20_compress",
		Model: llm,
		Tools: tools,
	})
	must(err)
	r, err := agentkit.Runner("s20", a, plug)
	must(err)
	prompt := "Write a 500-word essay about agent harnesses, then list 5 key terms."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
	wait()
	fmt.Println("(see .agent_memory.md for the compression audit log)")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
