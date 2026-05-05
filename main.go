// agent-toolkit — the all-in-one harness binary. Wires every component
// together and hands control to ADK's full launcher (interactive console
// + web).
//
// Run modes:
//
//	go run . [flags] console      # interactive REPL
//	go run . [flags] web webui    # local web UI
//	go run . --tui                # custom tview chat UI
//
// Flags:
//
//	-s, --skills <dir>   Directory to load skills from (default "skills")
//	    --tui            Launch the tview chat interface instead of the
//	                     ADK launcher.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/runner"

	"github.com/blouargant/agent-toolkit/agent"
	"github.com/blouargant/agent-toolkit/core/events"
	"github.com/blouargant/agent-toolkit/internal/tui"
)

// options holds the CLI flags consumed by this binary before the launcher
// subcommand (console / web ...) is dispatched.
type options struct {
	skillsDir     string
	softSkillsDir string
	tui           bool
	appName       string
	configPath    string
	modelProvider string
	modelName     string
	modelBaseURL  string
	modelAPIKey   string
	curatorRaw    string
}

// parseFlags extracts our own flags from args, returning the parsed
// options and the remaining args to forward to the ADK launcher.
func parseFlags(args []string) (options, []string, error) {
	opts := options{}

	fs := flag.NewFlagSet("agent-toolkit", flag.ContinueOnError)
	fs.StringVar(&opts.skillsDir, "skills", opts.skillsDir, "Directory to load skills from")
	fs.StringVar(&opts.skillsDir, "s", opts.skillsDir, "Directory to load skills from (shorthand)")
	fs.StringVar(&opts.softSkillsDir, "softskills", opts.softSkillsDir, "Directory to load curator-generated soft-skills from")
	fs.BoolVar(&opts.tui, "tui", false, "Launch the tview chat interface (ignores launcher subcommand)")
	fs.StringVar(&opts.appName, "name", opts.appName, "Application name")
	fs.StringVar(&opts.configPath, "config", "", "Path to runtime YAML config file (default: config/agent.yaml)")
	fs.StringVar(&opts.modelProvider, "provider", "", "Global model provider override")
	fs.StringVar(&opts.modelName, "model", "", "Global model override")
	fs.StringVar(&opts.modelBaseURL, "base-url", "", "Global model base URL override")
	fs.StringVar(&opts.modelAPIKey, "api-key", "", "Global model API key override")
	fs.StringVar(&opts.curatorRaw, "curator-enabled", "", "Enable/disable auto-curator hook (true/false)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: agent-toolkit [flags] <launcher-command> [launcher-args]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nLauncher commands: console, web webui, ...\n")
	}

	if err := fs.Parse(args); err != nil {
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func main() {
	ctx := context.Background()
	opts, rest, err := parseFlags(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	if err := run(ctx, opts, rest); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, launcherArgs []string) error {
	curatorEnabled, err := parseOptionalBool(opts.curatorRaw)
	if err != nil {
		return err
	}

	// `curate` is a special pre-launcher subcommand that runs the
	// soft-skills curator one-shot against an existing session's audit
	// and statelog files. Useful for replaying curation manually.
	if len(launcherArgs) > 0 && launcherArgs[0] == "curate" {
		return runCurate(ctx, opts, launcherArgs[1:])
	}

	// Create the fully configured agent using the agent package
	result, err := agent.NewAgent(ctx, agent.Options{
		SkillsDir:        opts.skillsDir,
		SoftSkillsDir:    opts.softSkillsDir,
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
		ModelProvider:    opts.modelProvider,
		ModelName:        opts.modelName,
		ModelBaseURL:     opts.modelBaseURL,
		ModelAPIKey:      opts.modelAPIKey,
		CuratorEnabled:   curatorEnabled,
	})
	if err != nil {
		return err
	}

	if opts.tui {
		r, err := runner.New(result.RunnerConfig)
		if err != nil {
			return fmt.Errorf("tui runner: %w", err)
		}
		subAgentNames := make([]string, 0, len(result.SubAgents))
		for name := range result.SubAgents {
			subAgentNames = append(subAgentNames, name)
		}
		sort.Strings(subAgentNames)
		return tui.Run(ctx, tui.Config{
			Runner:                        r,
			Bus:                           result.EventBus,
			AppName:                       result.RunnerConfig.AppName,
			SubAgentNames:                 subAgentNames,
			InputTokenPricePerMillion:     result.LeaderInputTokenPricePerMillion,
			OutputTokenPricePerMillion:    result.LeaderOutputTokenPricePerMillion,
		})
	}

	args := launcherArgs
	if len(args) == 0 {
		args = []string{"console"}
	}

	cfg := &launcher.Config{
		SessionService: result.RunnerConfig.SessionService,
		AgentLoader:    result.AgentLoader,
		PluginConfig:   result.RunnerConfig.PluginConfig,
	}
	// Emit the real session lifecycle events around the launcher so
	// subscribers (notably the soft-skills curator) fire once on shutdown
	// rather than on every per-turn run_end. The launcher creates the
	// session at runtime so we don't know user_id / session_id upfront;
	// the curator hook tolerates empty IDs by skipping.
	if result.EventBus != nil {
		result.EventBus.Emit(events.EventSessionStart, map[string]any{})
		defer result.EventBus.Emit(events.EventSessionEnd, map[string]any{})
	}
	return full.NewLauncher().Execute(ctx, cfg, args)
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
