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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/adk/runner"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/claudeformat"
	"github.com/blouargant/yoke/internal/cli"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
	"github.com/blouargant/yoke/internal/sessions"
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
  yoke [flags] reindex-precedents  rebuild the cross-session precedent index
  yoke [flags] reindex-docs        rebuild the documentation semantic index
  yoke [flags] embed-test [text]   probe the configured embedder (semantic recall)
  yoke import-agent <file|-|URL> import a Claude Code sub-agent (.md or .json)
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
		case "reindex-precedents":
			return runReindexPrecedents(ctx, opts, args[1:])
		case "reindex-docs":
			return runReindexDocs(ctx, opts, args[1:])
		case "embed-test":
			return runEmbedTest(ctx, opts, args[1:])
		case "tui":
			return runTUI(ctx, opts)
		case "run":
			return runCLI(ctx, opts, args[1:])
		case "import-agent":
			return runImportAgent(args[1:])
		case "permissions":
			return runPermissions(args[1:])
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

// runTUI launches the tview chat UI in multi-session mode, mirroring
// the web UI's session-sidebar + squad-aware chat behaviour. It builds
// the same Infrastructure+Manager combo the HTTP server uses so the
// TUI and server share session storage and skills.
func runTUI(ctx context.Context, opts options) error {
	curatorEnabled, err := parseOptionalBool(opts.curatorRaw)
	if err != nil {
		return err
	}
	agentOpts := agent.Options{
		SoftSkillsDir:    opts.softSkillsDir,
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
		CuratorEnabled:   curatorEnabled,
		DebugLogging:     opts.debug,
	}
	infra, err := agent.BuildInfrastructure(ctx, agentOpts)
	if err != nil {
		return err
	}
	defer infra.Close()
	if agentOpts.Repo == "" {
		agentOpts.Repo = infra.Repo
	}
	if agentOpts.AppName == "" {
		agentOpts.AppName = infra.AppName
	}
	inst, err := agent.BuildInstance(ctx, infra, agentOpts, 1)
	if err != nil {
		return err
	}
	manager := agent.NewManager(infra, inst)
	defer manager.Close()

	registry := sessions.NewRegistry()

	subAgentNames := make([]string, 0, len(inst.SubAgents))
	for name := range inst.SubAgents {
		subAgentNames = append(subAgentNames, name)
	}
	sort.Strings(subAgentNames)

	if infra.Bus != nil {
		infra.Bus.Emit(events.EventSessionStart, map[string]any{})
		defer infra.Bus.Emit(events.EventSessionEnd, map[string]any{})
	}

	leaderCfg := inst.LeaderCfg
	return tui.Run(ctx, tui.Config{
		Manager:                           manager,
		Sessions:                          registry,
		Bus:                               infra.Bus,
		AskUserRegistry:                   infra.AskUserRegistry,
		AppName:                           inst.RunnerConfig.AppName,
		SubAgentNames:                     subAgentNames,
		RegisterSession:                   infra.RegisterSession,
		UnregisterSession:                 infra.UnregisterSession,
		WatchMailbox:                      infra.WatchMailbox,
		AgentOptions:                      agentOpts,
		InputTokenPricePerMillion:         leaderCfg.InputTokenPricePerMillion,
		OutputTokenPricePerMillion:        leaderCfg.OutputTokenPricePerMillion,
		CachedInputTokenPricePerMillion:   leaderCfg.CachedInputTokenPricePerMillion,
		CacheCreationTokenPricePerMillion: leaderCfg.CacheCreationTokenPricePerMillion,
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
		CuratorEnabled:   curatorEnabled,
		DebugLogging:     opts.debug,
	})
}

// runImportAgent parses a Claude Code sub-agent file (markdown or JSON) and
// installs it into the local agents registry. Pass "-" to read from stdin.
//
// Usage: yoke import-agent [--enable] <file.md|file.json|->
func runImportAgent(args []string) error {
	fs := flag.NewFlagSet("import-agent", flag.ContinueOnError)
	enable := fs.Bool("enable", false, "Add the imported agent(s) to config/agents.json so they load on next run")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: yoke import-agent [--enable] <file.md|file.json|->\n\nReads a Claude Code sub-agent definition and installs it into the local\nagents registry ($YOKE_HOME/registry/agents/<name>/).\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("file argument is required (use - for stdin)")
	}

	var content []byte
	src := fs.Arg(0)
	if src == "-" {
		var err error
		content, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	} else {
		var err error
		content, err = os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}
	}

	defs, err := claudeformat.Parse(content)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	agentsRegistryDir := paths.AgentsRegistryWriteDir()
	for _, def := range defs {
		if !registries.SkillNameRe.MatchString(def.Name) {
			return fmt.Errorf("agent name %q is not valid (must be lowercase kebab-case)", def.Name)
		}
		if err := claudeformat.InstallAgent(def, agentsRegistryDir); err != nil {
			return fmt.Errorf("install %s: %w", def.Name, err)
		}
		fmt.Printf("installed agent %q → %s/%s/\n", def.Name, agentsRegistryDir, def.Name)

		if *enable {
			configRead := paths.FindConfig("agents.json")
			configWrite := filepath.Join(paths.ConfigWriteDir(), "agents.json")
			if err := appendAgentNameToConfig(configRead, configWrite, def.Name); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not enable %q in agents.json: %v\n", def.Name, err)
			} else {
				fmt.Printf("enabled agent %q in %s\n", def.Name, configWrite)
			}
		}
	}
	return nil
}

// appendAgentNameToConfig appends name to the agents list in the JSON config
// at readPath and writes the result to writePath.
func appendAgentNameToConfig(readPath, writePath, name string) error {
	data, err := os.ReadFile(readPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var cfg map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	rawAgents, _ := cfg["agents"].([]any)
	for _, item := range rawAgents {
		if s, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(s), name) {
			return nil // already present
		}
	}
	cfg["agents"] = append(rawAgents, name)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(writePath, out, 0o644)
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
