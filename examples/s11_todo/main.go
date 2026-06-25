// Component s11 — TodoWrite planning tools.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/todo"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	store := todo.NewStore("")
	tools := append([]any{}, fstools.New(), store.Tools())
	_ = tools
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s11_todo",
		Description: "TodoWrite-first planning demo.",
		Model:       llm,
		Tools:       append(fstools.New(), store.Tools()...),
		Instruction: "ALWAYS call todo_write with a 3-5 step plan before doing anything else.",
	})
	must(err)
	r, err := agentkit.Runner("s11", a)
	must(err)
	prompt := "Plan and then execute: create a file plan.md with three bullet points about adding TLS to a web server."
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
