// curate.go — `omnis curate` subcommand. Runs the soft-skills
// curator agent one-shot against an existing session's audit + statelog
// files. Synchronous (unlike the EventSessionEnd hook which fires async).
//
// Usage:
//
//	omnis curate --user <id> --session <id>
//	omnis curate --audit <path> --statelog <path>
//
// At least one of (audit, statelog) must resolve to an existing file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/core/llm"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/softskills"
)

func runCurate(ctx context.Context, opts options, args []string) error {
	curatorEnabled, err := parseOptionalBool(opts.curatorRaw)
	if err != nil {
		return err
	}
	runtime, err := agent.ResolveRuntimeSettings(agent.Options{
		SoftSkillsDir:    opts.softSkillsDir,
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
		CuratorEnabled:   curatorEnabled,
	})
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("curate", flag.ContinueOnError)
	var (
		user      string
		session   string
		auditPath string
		statePath string
	)
	fs.StringVar(&user, "user", "", "User ID of the session to curate")
	fs.StringVar(&session, "session", "", "Session ID to curate")
	fs.StringVar(&auditPath, "audit", "", "Explicit path to the per-session audit ($OMNIS_HOME/logs/agent_memory_*.md)")
	fs.StringVar(&statePath, "statelog", "", "Explicit path to the per-session State Log ($OMNIS_HOME/logs/agent_statelog_*.json)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omnis curate (--user <id> --session <id> | --audit <path> --statelog <path>)\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if auditPath == "" || statePath == "" {
		if user == "" || session == "" {
			fs.Usage()
			return fmt.Errorf("either --audit and --statelog, or --user and --session, must be provided")
		}
		key := agent.SessionSuffix(user, session)
		if auditPath == "" {
			auditPath = filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_memory_%s.md", key))
		}
		if statePath == "" {
			statePath = filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_statelog_%s.json", key))
		}
	}

	if !curateExists(auditPath) && !curateExists(statePath) {
		return fmt.Errorf("neither audit nor statelog file exists (%s, %s)", auditPath, statePath)
	}

	curatorCfg, ok := runtime.AgentConfig("curator")
	if !ok {
		return fmt.Errorf("runtime config: missing curator agent config")
	}
	model, err := llm.NewWithSelection(ctx, llm.Selection{
		Provider: curatorCfg.Provider,
		Model:    curatorCfg.Model,
		BaseURL:  curatorCfg.BaseURL,
		APIKey:   curatorCfg.APIKey,
	})
	if err != nil {
		return fmt.Errorf("model: %w", err)
	}
	r, err := softskills.CuratorRunner(ctx, softskills.CuratorConfig{
		Model:         model,
		SoftSkillsDir: runtime.SoftSkillsDir,
	})
	if err != nil {
		return fmt.Errorf("curator runner: %w", err)
	}
	out, err := softskills.Curate(ctx, r, softskills.CurateInputs{
		AuditPath:    auditPath,
		StateLogPath: statePath,
	})
	if err != nil {
		return fmt.Errorf("curate: %w", err)
	}
	fmt.Println(strings.TrimSpace(out))
	return nil
}

func curateExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
