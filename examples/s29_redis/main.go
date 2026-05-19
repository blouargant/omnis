// Component s29 — Redis-backed teammates. Same as s26_mailbox but
// requires REDIS_URL.
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
		Name:  "s29_redis",
		Model: llm,
		Tools: me.Tools(),
	})
	must(err)
	r, err := agentkit.Runner("s29", a)
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
