// Component s16 — session resume. ADK already persists
// session events in-memory; here we show how to fetch one across two runs.
package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s16_resume",
		Description: "Session-resume demo (in-memory, two turns).",
		Model:       llm,
	})
	must(err)
	r, err := agentkit.Runner("s16", a)
	must(err)

	turn := func(text string) {
		seq := r.Run(ctx, "u", "sess",
			&genai.Content{Role: "user", Parts: []*genai.Part{{Text: text}}},
			agent.RunConfig{})
		_ = stream.Print(os.Stdout, seq)
	}

	turn("Remember: my favourite colour is teal.")
	fmt.Println("--- new turn (same session) ---")
	turn("What is my favourite colour?")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
