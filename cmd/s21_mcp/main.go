// Component s21 — MCP toolsets (Phase 5 / s21).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
	mcpcfg "github.com/blouargant/agent-toolkit/internal/mcp"
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
		Name:        "s21_mcp",
		Description: "MCP-tool-aware agent.",
		Instruction: "List the MCP tools you have, grouped by server.",
		Model:       llm,
		Toolsets:    tsets,
	})
	must(err)
	r, err := agentkit.Runner("s21", a)
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
