// Package softskills — curator side.
//
// Curator is the agent that distills successful session experience into
// new soft-skills. It is invoked one-shot (not as a SubAgent of the lead)
// after a session ends — either automatically via the EventSessionEnd hook
// or explicitly via the `curate` CLI subcommand / `curate_session` tool.
//
// Inputs the curator consumes (all already produced per-session by the
// existing compress plugin and StateLog):
//   - .agent_memory_<sessionSuffix>.md — distilled audit (compress plugin).
//   - .agent_statelog_<sessionSuffix>.json — structured session insights.
//
// The curator is intentionally NOT given write access to anything outside
// the softskills directory. It uses the three softskill_* tools defined in
// writetool.go plus the read-only fs tools.
package softskills

import (
	"context"
	"fmt"
	"os"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/blouargant/yoke/core/agentkit"
	fstools "github.com/blouargant/yoke/core/tools"
)

// CuratorPrompt is the curator's role-specific instruction. Appended to
// the harness SystemPrompt by agentkit.New.
const CuratorPrompt = `You are the **curator** sub-agent of the soft-skills system.

Your single mission: turn one finished session into at most ONE new soft-skill, or at most ONE substantive update to an existing soft-skill, and keep softskills/INDEX.md in sync. If neither is warranted, write nothing and explain why.

You are invoked AFTER the session ended. The lead agent and the user are gone. Do not address them.

## Inputs

You will receive in the user message:
1. The path to the session's compress audit file (a markdown distillation of the conversation).
2. The path to the session's StateLog JSON (structured: goal, decisions, open_issues, files, tools).
3. The list of currently mounted authored skills (so you can avoid duplication).
4. The list of currently mounted soft-skills (same reason).

Read each file with run_read. Do not invent paths.

## Workflow (follow in order)

1. Read the inputs. Skip any path that does not exist; carry on with what you have.

2. Identify a candidate procedure. Look for a multi-step sequence (3 or more concrete actions) that:
   - reached a verifiable success state, and
   - is generalizable beyond the session's specific IDs/paths/usernames, and
   - is non-trivial (a single tool call is not a soft-skill).

   If no candidate qualifies, output a one-line rationale and stop. DO NOT write anything.

3. Redundancy audit. For each candidate:
   - Compare its essence against the authored-skills list and the soft-skills list provided in the prompt.
   - If an existing entry already covers the procedure, choose between:
     a) Skip — if the existing entry is at least as good (default; prefer skipping over creating).
     b) Update — only if the session revealed a concrete improvement: a new edge case handled, a step removed, a constraint discovered. You MUST justify the improvement in the 'reason' argument of softskill_update (at least 20 chars).
   - Never create a near-duplicate.

4. Generalize. Strip session-specific identifiers (replace concrete pod names, file paths, user IDs with placeholders or remove them entirely). The skill must read as a procedure, not a story.

5. Write the SKILL.md using the standard layout. Frontmatter MUST contain ONLY these two fields (the loader rejects anything else):
   - name: <kebab-case-name> (lowercase letters, digits and dashes only)
   - description: <one sentence describing when to use this>

   Categorisation lives in INDEX.md, not in the frontmatter.

   Body sections (in order):
   - # <Title>
   - ## Context — why this skill exists; what problem it solves.
   - ## Steps — numbered, concrete actions.
   - ## Constraints — things to avoid.
   - ## Validation — how to verify each step succeeded.

   The directory name MUST equal the frontmatter 'name'.

6. Call softskill_create (for a new skill) or softskill_update (for an existing one). The tool will reject trivial updates and path traversal — trust its rejections; do not retry with workarounds.

7. Call softskill_index_append with the chosen category, name and a one-line summary. Idempotent — safe to call after either create or update.

8. Reply with exactly one paragraph: what you did and why. No preamble, no farewell.

## Hard rules

- Write at most ONE soft-skill per invocation. Quality over quantity.
- Never modify files outside the softskills directory.
- Never invoke skills (load_skill / load_softskill) — you are reading sessions, not solving tasks.
- If a tool call returns an error starting with 'Error:', DO NOT retry the same call; pick a different action or stop.
`

// CuratorConfig configures the curator agent.
type CuratorConfig struct {
	// Model is required.
	Model model.LLM
	// SoftSkillsDir defaults to DefaultDir.
	SoftSkillsDir string
	// SkillsDir is where authored skills live; used for the redundancy
	// audit listing the curator embeds in its prompt.
	SkillsDir string
}

// NewCurator builds the curator agent. It mounts:
//   - read-only fs tools (run_read, run_glob, run_grep) for reading the
//     audit and statelog files,
//   - the three softskill_* write tools (constrained to SoftSkillsDir),
//   - the softskills toolset itself (so the curator can `list_softskills`
//     during the redundancy check).
//
// It does NOT mount the authored-skills toolset; the snapshot of authored
// skill names is passed in the user prompt by Curate() instead, to keep
// the curator's tool surface tight.
func NewCurator(ctx context.Context, cfg CuratorConfig) (adkagent.Agent, error) {
	if cfg.SoftSkillsDir == "" {
		cfg.SoftSkillsDir = DefaultDir
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("softskills: curator requires Model")
	}

	tools := fstools.New()
	tools = append(tools, WriteTools(cfg.SoftSkillsDir)...)

	sts, err := Toolset(ctx, cfg.SoftSkillsDir)
	if err != nil {
		return nil, err
	}

	return agentkit.New(agentkit.AgentConfig{
		Name:        "curator",
		Description: "Distils successful session experience into reusable soft-skills.",
		Model:       cfg.Model,
		Tools:       tools,
		Toolsets:    []tool.Toolset{sts},
		Instruction: CuratorPrompt,
	})
}

// CurateInputs are the per-session artefacts the curator reads.
type CurateInputs struct {
	// AuditPath is the compress plugin's per-session memory file.
	AuditPath string
	// StateLogPath is the StateLog JSON file.
	StateLogPath string
	// AuthoredSkills is a list of "<name>: <description>" lines used in
	// the redundancy audit. Pass nil/empty if unknown.
	AuthoredSkills []string
}

// Curate runs the curator once against the provided inputs. It returns
// the final assistant text or an error. Honors ctx cancellation.
func Curate(ctx context.Context, r *runner.Runner, in CurateInputs) (string, error) {
	prompt := buildCuratePrompt(in)
	var last string
	for ev, err := range r.Run(ctx, "curator", "curate-once",
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		adkagent.RunConfig{}) {
		if err != nil {
			return last, err
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p.Text != "" {
				last = p.Text
			}
		}
	}
	return last, nil
}

func buildCuratePrompt(in CurateInputs) string {
	var b strings.Builder
	b.WriteString("You are about to curate a finished session. Inputs:\n\n")
	if in.AuditPath != "" {
		fmt.Fprintf(&b, "1. Audit file: %s\n", in.AuditPath)
		if _, err := os.Stat(in.AuditPath); err != nil {
			b.WriteString("   (NOTE: file is missing — skip step 1.)\n")
		}
	} else {
		b.WriteString("1. Audit file: (none provided)\n")
	}
	if in.StateLogPath != "" {
		fmt.Fprintf(&b, "2. StateLog file: %s\n", in.StateLogPath)
		if _, err := os.Stat(in.StateLogPath); err != nil {
			b.WriteString("   (NOTE: file is missing — skip step 2.)\n")
		}
	} else {
		b.WriteString("2. StateLog file: (none provided)\n")
	}
	b.WriteString("\n3. Authored skills already mounted (do NOT duplicate):\n")
	if len(in.AuthoredSkills) == 0 {
		b.WriteString("   (none listed)\n")
	} else {
		for _, s := range in.AuthoredSkills {
			fmt.Fprintf(&b, "   - %s\n", s)
		}
	}
	b.WriteString("\n4. Existing soft-skills: discover them with `list_softskills` before deciding whether to create or update.\n\n")
	b.WriteString("Begin the workflow now. Reply only at the end with the one-paragraph summary.\n")
	return b.String()
}

// CuratorRunner is a small convenience that pairs NewCurator + agentkit.Runner.
func CuratorRunner(ctx context.Context, cfg CuratorConfig) (*runner.Runner, error) {
	a, err := NewCurator(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return runner.New(runner.Config{
		AppName:           "curator",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
}
