// Component s21 — skills. Loads ./skills/ via ADK's
// skilltoolset.
package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/adk/tool"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	"github.com/blouargant/yoke/internal/skills"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	ts, err := skills.Toolset(ctx, nil)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s21_skills",
		Description: "Skill-aware agent.",
		Model:       llm,
		Toolsets:    []tool.Toolset{ts},
		Instruction: "When the user asks about something a known skill covers, call load_skill first.",
	})
	must(err)
	r, err := agentkit.Runner("s21", a)
	must(err)
	prompt := "List your available skills and pick the most relevant for reviewing Go code."
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
