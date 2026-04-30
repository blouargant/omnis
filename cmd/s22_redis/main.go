// Component s22 — Redis-backed teammates (Phase 6 / s22). Same as s09 but
// requires REDIS_URL.
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
	if os.Getenv("REDIS_URL") == "" {
		fmt.Fprintln(os.Stderr, "REDIS_URL not set; skipping (this demo requires a running Redis).")
		return
	}
	os.Setenv("MAILBOX_BACKEND", "redis")
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)
	be, err := teammates.ChooseBackend()
	must(err)
	defer be.Close()
	me := teammates.NewAgent("lead", be)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:  "s22_redis",
		Model: llm,
		Tools: me.Tools(),
	})
	must(err)
	r, err := agentkit.Runner("s22", a)
	must(err)
	prompt := "Tell teammate 'reviewer' the message 'ping over redis' then check your own mailbox."
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
