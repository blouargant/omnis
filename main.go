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

	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/runner"

	"github.com/blouargant/agent-toolkit/agent"
	"github.com/blouargant/agent-toolkit/internal/tui"
)

// options holds the CLI flags consumed by this binary before the launcher
// subcommand (console / web ...) is dispatched.
type options struct {
	skillsDir     string
	softSkillsDir string
	tui           bool
	appName       string
}

// parseFlags extracts our own flags from args, returning the parsed
// options and the remaining args to forward to the ADK launcher.
func parseFlags(args []string) (options, []string, error) {
	opts := options{skillsDir: "skills", softSkillsDir: "softskills", appName: "agent-toolkit"}

	fs := flag.NewFlagSet("agent-toolkit", flag.ContinueOnError)
	fs.StringVar(&opts.skillsDir, "skills", opts.skillsDir, "Directory to load skills from")
	fs.StringVar(&opts.skillsDir, "s", opts.skillsDir, "Directory to load skills from (shorthand)")
	fs.StringVar(&opts.softSkillsDir, "softskills", opts.softSkillsDir, "Directory to load curator-generated soft-skills from")
	fs.BoolVar(&opts.tui, "tui", false, "Launch the tview chat interface (ignores launcher subcommand)")
	fs.StringVar(&opts.appName, "name", opts.appName, "Application name")
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
	// `curate` is a special pre-launcher subcommand that runs the
	// soft-skills curator one-shot against an existing session's audit
	// and statelog files. Useful for replaying curation manually.
	if len(launcherArgs) > 0 && launcherArgs[0] == "curate" {
		return runCurate(ctx, opts, launcherArgs[1:])
	}

	// Create the fully configured agent using the agent package
	result, err := agent.NewAgent(ctx, agent.Options{
		SkillsDir:     opts.skillsDir,
		SoftSkillsDir: opts.softSkillsDir,
		AppName:       opts.appName,
	})
	if err != nil {
		return err
	}

	if opts.tui {
		r, err := runner.New(result.RunnerConfig)
		if err != nil {
			return fmt.Errorf("tui runner: %w", err)
		}
		return tui.Run(ctx, tui.Config{
			Runner:  r,
			Bus:     result.EventBus,
			AppName: opts.appName,
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
	return full.NewLauncher().Execute(ctx, cfg, args)
}
