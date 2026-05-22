// Component s09 — bash output filters. Yoke ships YAML rules under
// config/filters/ that condense noisy command output (git status, kubectl
// logs, npm install, etc.) before it reaches the model. This example runs
// the same noisy command twice — once with the filter pipeline disabled,
// once enabled — so the byte-count delta is visible.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
)

func main() {
	ctx := context.Background()

	// 1. Locate the filters directory. Filters are repo-local YAML.
	filtersDir := locateFiltersDir()

	// 2. Direct demo: same command, both modes — show the byte-count delta.
	cmd := "git status"
	must(fstools.ConfigureBashOutputFilter(fstools.BashOutputFilterConfig{Enabled: false}))
	raw, _ := fstools.RunBash(ctx, fstools.BashIn{Command: cmd})

	must(fstools.ConfigureBashOutputFilter(fstools.BashOutputFilterConfig{
		Enabled:    true,
		FiltersDir: filtersDir,
	}))
	filtered, _ := fstools.RunBash(ctx, fstools.BashIn{Command: cmd})

	fmt.Printf("=== %q raw output: %d bytes ===\n%s\n\n", cmd, len(raw), raw)
	fmt.Printf("=== %q filtered output: %d bytes ===\n%s\n\n", cmd, len(filtered), filtered)
	fmt.Printf("Filter saved %d bytes (%.0f%%)\n\n", len(raw)-len(filtered), 100*float64(len(raw)-len(filtered))/float64(max(len(raw), 1)))

	// 3. Same setup but driven by the agent: it sees the filtered output,
	// not the raw one, so its summary is grounded in the condensed view.
	llm, err := agentkit.NewModel(ctx)
	must(err)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s09_output_filters",
		Description: "Bash output filter demo.",
		Model:       llm,
		Instruction: "Run the requested bash command and report what it produced.",
		Tools:       fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s09", a)
	must(err)

	prompt := "Run `git status` and tell me in one sentence what state the repo is in."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
}

// locateFiltersDir walks up from this file's directory until it finds
// config/filters/, so the example works regardless of where it is invoked.
func locateFiltersDir() string {
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Dir(here)
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "config", "filters")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	return ".agents/filters"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
