// Component s22 — subagents as tools. The lead delegates
// to a small "summariser" sub-agent via ADK's agenttool wrapper.
package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	sub, err := llmagent.New(llmagent.Config{
		Name:        "summariser",
		Description: "Summarise any text in <=3 sentences.",
		Instruction: "You summarise text in three sentences max.",
		Model:       llm,
	})
	must(err)
	subTool := agenttool.New(sub, &agenttool.Config{})
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s22_subagents",
		Description: "Lead agent that calls a summariser sub-agent.",
		Instruction: "When asked to summarise, call the summariser tool with the text.",
		Model:       llm,
		Tools:       []tool.Tool{subTool},
	})
	must(err)
	r, err := agentkit.Runner("s22", a)
	must(err)
	prompt := "Summarise this in 3 sentences: Go is a statically typed compiled language. It has goroutines for cheap concurrency. The standard library is famously broad."
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
