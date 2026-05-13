// Component s09 — persistent teammates / mailboxes (Phase 3 / s09).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	"github.com/blouargant/yoke/internal/teammates"
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
		Instruction: "Use teammate_tell with fields `to` and `body` to send a one-way message to 'reviewer', then teammate_check to read your own mailbox.",
		Model:       llm,
		Tools:       me.Tools(),
	})
	must(err)
	r, err := agentkit.Runner("s09", a)
	must(err)
	prompt := "Call teammate_tell with to='reviewer' and body='please review PR #42', then call teammate_check on your own mailbox."
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
