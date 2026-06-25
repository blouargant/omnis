// Component s08 — interactive `ask_user` tool. Demonstrates how the agent
// can pause the loop and ask the human a structured question (single,
// multi, text, or confirm) before continuing. The askuser registry is
// wired to stdin so the question is rendered on the console.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/core/stream"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/askuser"
	"google.golang.org/adk/tool"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)

	reg := askuser.NewRegistry()
	askuser.InstallStdinAsker(reg)

	tools := append(fstools.New(), fstools.NewAskUserTool(reg))
	tools = withAskUserOnly(tools) // narrow set so the demo always picks ask_user

	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s08_ask_user",
		Description: "Interactive ask_user tool demo.",
		Model:       llm,
		Instruction: "Use the ask_user tool exactly once to learn the user's name " +
			"(kind=\"text\"), then greet them by that name in a single sentence. " +
			"Do not call any other tool.",
		Tools: tools,
	})
	must(err)
	r, err := agentkit.Runner("s08", a)
	must(err)

	prompt := "Please greet me — but find out my name first."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
}

// withAskUserOnly keeps only the ask_user tool from the toolset so the demo
// agent doesn't reach for bash/read/write when it could simply ask.
func withAskUserOnly(in []tool.Tool) []tool.Tool {
	out := make([]tool.Tool, 0, 1)
	for _, t := range in {
		if t.Name() == "AskUserQuestion" {
			out = append(out, t)
		}
	}
	return out
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
