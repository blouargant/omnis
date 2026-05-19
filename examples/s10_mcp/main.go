// Component s10 — MCP toolsets.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	mcpcfg "github.com/blouargant/yoke/internal/mcp"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	cfg, err := mcpcfg.Load("config/mcp_config.yaml")
	must(err)
	tsets, err := cfg.Toolsets()
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s10_mcp",
		Description: "MCP-tool-aware agent.",
		Instruction: "List the MCP tools you have, grouped by server.",
		Model:       llm,
		Toolsets:    tsets,
	})
	must(err)
	r, err := agentkit.Runner("s10", a)
	must(err)
	prompt := "What MCP tools do you have?"
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
