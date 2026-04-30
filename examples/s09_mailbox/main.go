// Component s09 — persistent teammates / mailboxes (Phase 3 / s09).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/stream"
	"github.com/blouargant/agent-toolkit/internal/teammates"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	be, err := teammates.ChooseBackend()
	must(err)
	defer be.Close()
	me := teammates.NewAgent("lead", be)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s09_mailbox",
		Description: "Lead agent with teammate mailbox.",
		Instruction: "Use teammate_tell to drop a message in 'reviewer' mailbox, then teammate_check to read your own.",
		Model:       llm,
		Tools:       me.Tools(),
	})
	must(err)
	r, err := agentkit.Runner("s09", a)
	must(err)
	prompt := "Send the reviewer agent the message 'please review PR #42' then check your own mailbox."
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
