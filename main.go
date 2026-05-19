// yoke — the all-in-one harness binary.
//
// Usage:
//
//	yoke [flags] [prompt...]    # CLI mode (REPL when stdin is a TTY,
//	                            # one-shot when a prompt arg or piped input
//	                            # is provided)
//	yoke [flags] run [prompt]   # explicit CLI form
//	yoke [flags] tui            # tview chat UI
//	yoke [flags] curate ...     # one-shot soft-skill curator
//	yoke version                # print version information
//
// The HTTP API server lives in the separate `yoke-server` binary (see
// ./server). The 3-mode contract is intentional: command line, TUI, server.
//
// See `yoke --help` for the full flag reference.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/adk/runner"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/cli"
	"github.com/blouargant/yoke/internal/tui"
)

// Build metadata, populated via -ldflags in the Makefile:
//
//	-X main.version=... -X main.commit=... -X main.date=...
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// options holds CLI flags consumed before the subcommand keyword.
type options struct {
	softSkillsDir string
	appName       string
	configPath    string
	modelProvider string
	modelName     string
	modelBaseURL  string
	modelAPIKey   string
	curatorRaw    string
	debug         bool
}

// newFlagSet wires the global yoke flags onto opts and returns the
// flag.FlagSet so callers can either Parse() or print usage.
func newFlagSet(opts *options) *flag.FlagSet {
	fs := flag.NewFlagSet("yoke", flag.ContinueOnError)
	fs.StringVar(&opts.softSkillsDir, "softskills", opts.softSkillsDir, "Directory to load curator-generated soft-skills from")
	fs.StringVar(&opts.appName, "name", opts.appName, "Application name")
	fs.StringVar(&opts.configPath, "config", "", "Path to runtime JSON config file (default: config/agents.json)")
	fs.StringVar(&opts.modelProvider, "provider", "", "Global model provider override")
	fs.StringVar(&opts.modelName, "model", "", "Global model override")
	fs.StringVar(&opts.modelBaseURL, "base-url", "", "Global model base URL override")
	fs.StringVar(&opts.modelAPIKey, "api-key", "", "Global model API key override")
	fs.StringVar(&opts.curatorRaw, "curator-enabled", "", "Enable/disable auto-curator hook (true/false)")
	fs.BoolVar(&opts.debug, "debug", false, "Log full conversation/event payloads")
	fs.BoolVar(&opts.debug, "d", false, "Log full conversation/event payloads (shorthand)")
	fs.Usage = func() { printUsage(fs) }
	return fs
}

// parseFlags extracts our own flags from args and returns the remainder
// (intended for subcommand dispatch).
func parseFlags(args []string) (options, []string, error) {
	opts := options{}
	fs := newFlagSet(&opts)
	if err := fs.Parse(args); err != nil {
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprintf(os.Stderr, `yoke — single-binary harness for the agent toolkit.

Usage:
  yoke [flags] [prompt...]      run a one-shot turn (or REPL if stdin is a TTY)
  yoke [flags] run [prompt]     same as above; the explicit form
  yoke [flags] tui              launch the interactive TUI
  yoke [flags] curate ...       run the soft-skill curator one-shot
  yoke version                  print version information
  yoke help                     show this help

Flags (must appear before any subcommand or prompt):
`)
	fs.PrintDefaults()
	fmt.Fprintf(os.Stderr, "\nThe HTTP API server is a separate binary: yoke-server (see ./server).\n")
}

func main() {
	ctx := context.Background()
	opts, rest, err := parseFlags(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	if opts.configPath == "" {
		opts.configPath = strings.TrimSpace(os.Getenv("YOKE_CONFIG_PATH"))
	}
	if err := run(ctx, opts, rest); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// run dispatches to the correct subcommand based on the first remaining
// argument. When no subcommand keyword is present, the remaining args are
// joined as a one-shot prompt for the CLI; an empty arg list yields a REPL
// (if stdin is a TTY) or a one-shot reading piped stdin.
func run(ctx context.Context, opts options, args []string) error {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "version", "--version", "-v":
			fmt.Printf("yoke %s (commit %s, built %s)\n", version, commit, date)
			return nil
		case "help", "--help", "-h":
			var dummy options
			printUsage(newFlagSet(&dummy))
			return nil
		case "curate":
			return runCurate(ctx, opts, args[1:])
		case "tui":
			return runTUI(ctx, opts)
		case "run":
			return runCLI(ctx, opts, args[1:])
		}
	}
	return runCLI(ctx, opts, args)
}

// runCLI builds the agent and hands off to internal/cli. When promptArgs is
// non-empty they are joined as the one-shot prompt; otherwise the CLI
// auto-detects between REPL (TTY) and stdin-piped one-shot.
func runCLI(ctx context.Context, opts options, promptArgs []string) error {
	result, err := buildAgent(ctx, opts)
	if err != nil {
		return err
	}
	r, err := runner.New(result.RunnerConfig)
	if err != nil {
		return fmt.Errorf("cli runner: %w", err)
	}

	prompt := strings.TrimSpace(strings.Join(promptArgs, " "))

	if result.EventBus != nil {
		result.EventBus.Emit(events.EventSessionStart, map[string]any{})
		defer result.EventBus.Emit(events.EventSessionEnd, map[string]any{})
	}

	return cli.Run(ctx, cli.Config{
		Runner:          r,
		Bus:             result.EventBus,
		AskUserRegistry: result.AskUserRegistry,
		AppName:         result.RunnerConfig.AppName,
		Prompt:          prompt,
	})
}

// runTUI mirrors the legacy `--tui` flag: launches the tview chat UI.
func runTUI(ctx context.Context, opts options) error {
	result, err := buildAgent(ctx, opts)
	if err != nil {
		return err
	}
	r, err := runner.New(result.RunnerConfig)
	if err != nil {
		return fmt.Errorf("tui runner: %w", err)
	}
	subAgentNames := make([]string, 0, len(result.SubAgents))
	for name := range result.SubAgents {
		subAgentNames = append(subAgentNames, name)
	}
	sort.Strings(subAgentNames)

	if result.EventBus != nil {
		result.EventBus.Emit(events.EventSessionStart, map[string]any{})
		defer result.EventBus.Emit(events.EventSessionEnd, map[string]any{})
	}

	return tui.Run(ctx, tui.Config{
		Runner:                            r,
		Bus:                               result.EventBus,
		AskUserRegistry:                   result.AskUserRegistry,
		AppName:                           result.RunnerConfig.AppName,
		SubAgentNames:                     subAgentNames,
		InputTokenPricePerMillion:         result.LeaderInputTokenPricePerMillion,
		OutputTokenPricePerMillion:        result.LeaderOutputTokenPricePerMillion,
		CachedInputTokenPricePerMillion:   result.LeaderCachedInputTokenPricePerMillion,
		CacheCreationTokenPricePerMillion: result.LeaderCacheCreationTokenPricePerMillion,
	})
}

// buildAgent assembles the lead agent from the user-supplied options.
// Shared between CLI, TUI, and curate subcommands.
func buildAgent(ctx context.Context, opts options) (*agent.AgentResult, error) {
	curatorEnabled, err := parseOptionalBool(opts.curatorRaw)
	if err != nil {
		return nil, err
	}
	return agent.NewAgent(ctx, agent.Options{
		SoftSkillsDir:    opts.softSkillsDir,
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
		ModelProvider:    opts.modelProvider,
		ModelName:        opts.modelName,
		ModelBaseURL:     opts.modelBaseURL,
		ModelAPIKey:      opts.modelAPIKey,
		CuratorEnabled:   curatorEnabled,
		DebugLogging:     opts.debug,
	})
}

func parseOptionalBool(raw string) (*bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid boolean %q (expected true/false)", raw)
	}
	return &v, nil
}
