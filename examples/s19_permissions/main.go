// Component s19 — permission governance.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/permissions"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	plug, err := permissions.NewPlugin("perms", "config/permissions.yaml", permissions.StdinAsker{})
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s19_permissions",
		Description: "Permission-governed agent.",
		Instruction: "Use bash for any shell command. Some patterns will be denied or require approval.",
		Model:       llm,
		Tools:       fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s19", a, plug)
	must(err)
	prompt := "Run `ls /tmp` then try `rm -rf /` and report what happened."
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
