// Component s23 — soft-skills curator (manual invocation).
//
// Demonstrates the full soft-skill lifecycle without a running lead agent:
//
//  1. Writes a synthetic session audit + statelog to temp files.
//  2. Runs the curator one-shot against them.
//  3. Prints the curator's summary paragraph.
//  4. Lists whatever was written under ./tmp-softskills/.
//
// Run:
//
//	go run ./examples/s23_softskills
//
// The curator may decide nothing is worth curating — that is the expected
// behaviour when the session is trivial. Pass --force to supply a richer
// audit that reliably produces a new soft-skill.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/internal/softskills"
)

func main() {
	force := flag.Bool("force", false, "Use a richer audit that more reliably produces a soft-skill")
	dir := flag.String("dir", "tmp-softskills", "Directory the curator writes soft-skills into")
	flag.Parse()

	ctx := context.Background()
	must(run(ctx, *force, *dir))
}

func run(ctx context.Context, force bool, softDir string) error {
	// ── 1. Synthetic session artefacts ──────────────────────────────────
	audit, statelog, cleanup, err := writeSyntheticSession(force)
	defer cleanup()
	if err != nil {
		return fmt.Errorf("write synthetic session: %w", err)
	}
	fmt.Printf("audit:    %s\nstatelog: %s\n\n", audit, statelog)

	// ── 2. Build curator ─────────────────────────────────────────────────
	llm, err := agentkit.NewModel(ctx)
	if err != nil {
		return fmt.Errorf("model: %w", err)
	}
	r, err := softskills.CuratorRunner(ctx, softskills.CuratorConfig{
		Model:         llm,
		SoftSkillsDir: softDir,
	})
	if err != nil {
		return fmt.Errorf("curator runner: %w", err)
	}

	// ── 3. Run ────────────────────────────────────────────────────────────
	out, err := softskills.Curate(ctx, r, softskills.CurateInputs{
		AuditPath:    audit,
		StateLogPath: statelog,
	})
	if err != nil {
		return fmt.Errorf("curate: %w", err)
	}
	fmt.Println("── Curator summary ──────────────────────────────────────────")
	fmt.Println(out)
	fmt.Println("─────────────────────────────────────────────────────────────")

	// ── 4. Show what was written ──────────────────────────────────────────
	fmt.Printf("\n── %s/ ──\n", softDir)
	if err := filepath.Walk(softDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(softDir, p)
		fmt.Printf("  %s\n", rel)
		return nil
	}); err != nil {
		fmt.Printf("  (empty or not created)\n")
	}
	return nil
}

// writeSyntheticSession writes minimal realistic audit + statelog files to
// temp paths. It returns paths, a cleanup func, and any error.
func writeSyntheticSession(rich bool) (auditPath, statelogPath string, cleanup func(), err error) {
	cleanup = func() {}

	tmp, err := os.MkdirTemp("", "s23_session_*")
	if err != nil {
		return
	}
	cleanup = func() { os.RemoveAll(tmp) }

	audit := trivialAudit
	statelog := trivialStatelog
	if rich {
		audit = richAudit
		statelog = richStatelog
	}

	auditPath = filepath.Join(tmp, "audit.md")
	statelogPath = filepath.Join(tmp, "statelog.json")
	if err = os.WriteFile(auditPath, []byte(audit), 0o644); err != nil {
		return
	}
	err = os.WriteFile(statelogPath, []byte(statelog), 0o644)
	return
}

// trivialAudit — a single-step session the curator should skip.
const trivialAudit = `# Session audit

## Goal
Echo "hello world" in bash.

## Actions
1. Ran bash: echo hello world

## Outcome
Success.
`

const trivialStatelog = `{"version":1,"goal":"echo hello","outcome":"success","tool_calls":[{"tool":"Bash","args":"echo hello world"}]}`

// richAudit — a multi-step session the curator should distil.
const richAudit = `# Session audit

## Goal
Debug a Docker container that kept restarting with exit code 137 (OOM).

## Actions
1. Ran: docker inspect <container> | jq '.[0].State'
   — confirmed RestartCount=12, OOMKilled=true.
2. Ran: docker stats --no-stream <container>
   — showed memory usage spiking to 100% of the 256 MB limit.
3. Read application logs with: docker logs --tail 200 <container>
   — spotted a log flood: the app was writing structured JSON logs at
     DEBUG level, allocating large buffers on each log line.
4. Changed the log level in the container's env to INFO by updating
   the Compose file: LOG_LEVEL=info, then docker compose up -d.
5. Monitored memory with docker stats for 3 minutes — usage stabilised
   at ~90 MB.

## Outcome
Container running stably. Root cause: debug logging in a memory-
constrained container triggered OOM kills.

## Lesson
When a Docker container is repeatedly OOM-killed, check logs for
volume and verbosity before raising the memory limit. Log floods at
DEBUG level are a common cause in Go / Java services.
`

const richStatelog = `{
  "version": 1,
  "goal": "debug OOM-killed Docker container",
  "outcome": "fixed",
  "tool_calls": [
    {"tool": "Bash", "args": "docker inspect <container>"},
    {"tool": "Bash", "args": "docker stats --no-stream <container>"},
    {"tool": "Bash", "args": "docker logs --tail 200 <container>"},
    {"tool": "Write", "args": {"path": "docker-compose.yml"}},
    {"tool": "Bash", "args": "docker compose up -d"},
    {"tool": "Bash", "args": "docker stats"}
  ],
  "decisions": ["log level was DEBUG → changed to INFO"],
  "open_issues": []
}`

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
