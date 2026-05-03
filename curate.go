// curate.go — `agent-toolkit curate` subcommand. Runs the soft-skills
// curator agent one-shot against an existing session's audit + statelog
// files. Synchronous (unlike the EventSessionEnd hook which fires async).
//
// Usage:
//
//	agent-toolkit curate --user <id> --session <id>
//	agent-toolkit curate --audit <path> --statelog <path>
//
// At least one of (audit, statelog) must resolve to an existing file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/blouargant/agent-toolkit/agent"
	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/internal/softskills"
)

func runCurate(ctx context.Context, opts options, args []string) error {
	fs := flag.NewFlagSet("curate", flag.ContinueOnError)
	var (
		user      string
		session   string
		auditPath string
		statePath string
	)
	fs.StringVar(&user, "user", "", "User ID of the session to curate")
	fs.StringVar(&session, "session", "", "Session ID to curate")
	fs.StringVar(&auditPath, "audit", "", "Explicit path to the per-session audit (.agent_memory_*.md)")
	fs.StringVar(&statePath, "statelog", "", "Explicit path to the per-session State Log (.agent_statelog_*.json)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: agent-toolkit curate (--user <id> --session <id> | --audit <path> --statelog <path>)\n\nFlags:\n")
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
			auditPath = fmt.Sprintf(".agent_memory_%s.md", key)
		}
		if statePath == "" {
			statePath = fmt.Sprintf(".agent_statelog_%s.json", key)
		}
	}

	if !curateExists(auditPath) && !curateExists(statePath) {
		return fmt.Errorf("neither audit nor statelog file exists (%s, %s)", auditPath, statePath)
	}

	llm, err := agentkit.NewModel(ctx)
	if err != nil {
		return fmt.Errorf("model: %w", err)
	}
	r, err := softskills.CuratorRunner(ctx, softskills.CuratorConfig{
		Model:         llm,
		SoftSkillsDir: opts.softSkillsDir,
		SkillsDir:     opts.skillsDir,
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
