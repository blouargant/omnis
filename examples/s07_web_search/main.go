// Component s07 — web tools: `web_search` (DuckDuckGo) and `web_fetch`.
// The agent searches the web, picks a result, fetches the page as
// markdown, then summarises. Requires outbound network access.
//
// Set SERPAPI_KEY to swap DuckDuckGo for the SerpAPI Google engine.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)

	tools := fstools.NewWebTools()
	if key := os.Getenv("SERPAPI_KEY"); key != "" {
		fmt.Fprintln(os.Stderr, "(web_search backed by SerpAPI / Google)")
		tools = append(tools, fstools.NewSerpAPITools(key)...)
	} else {
		fmt.Fprintln(os.Stderr, "(web_search backed by DuckDuckGo — set SERPAPI_KEY for Google)")
		tools = append(tools, fstools.NewDDGTools()...)
	}

	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s07_web_search",
		Description: "web_search + web_fetch demo.",
		Model:       llm,
		Instruction: "Use web_search to find relevant pages, then use web_fetch on " +
			"the most promising URL. Summarise the answer in 3 sentences citing the URL.",
		Tools: tools,
	})
	must(err)
	r, err := agentkit.Runner("s07", a)
	must(err)

	prompt := "Who maintains the Go ADK library and what is its primary purpose?"
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
