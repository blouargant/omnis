// Package softskills — curator side.
//
// Curator is the agent that distills successful session experience into
// new soft-skills. It is invoked one-shot (not as a SubAgent of the lead)
// after a session ends — either automatically via the EventSessionEnd hook
// or explicitly via the `curate` CLI subcommand / `curate_session` tool.
//
// Inputs the curator consumes (all already produced per-session by the
// existing compress plugin and StateLog):
//   - logs/agent_memory_<sessionSuffix>.md — distilled audit (compress plugin).
//   - logs/agent_statelog_<sessionSuffix>.json — structured session insights.
//
// The curator is intentionally NOT given write access to anything outside
// the softskills directory. It uses the three softskill_* tools defined in
// writetool.go plus the read-only fs tools.
package softskills

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/blouargant/yoke/core/agentkit"
	fstools "github.com/blouargant/yoke/core/tools"
)

// CuratorPrompt is the curator's role-specific instruction. Appended to
// the harness SystemPrompt by agentkit.New.
const CuratorPrompt = `You are the **curator** sub-agent of the soft-skills system.

Your mission: turn one finished session into reusable soft-skills. You may write one soft-skill per target agent when warranted — so you can write several skills in a single invocation if the session produced distinct procedures that belong to different agents. Prefer quality over quantity: a procedure that is too thin does not qualify even if it technically belongs to an agent.

You are invoked AFTER the session ended. The lead agent and the user are gone. Do not address them.

## Per-agent directory layout

Soft-skills are organized by the agent that will use them at runtime:

| Agent | Directory | When to write here |
|---|---|---|
| leader (coordinator) | ` + "`softskills/<skill>/SKILL.md`" + ` | Orchestration knowledge: task decomposition, when to delegate, cross-agent coordination |
| investigator | ` + "`softskills/investigator/<skill>/SKILL.md`" + ` | Evidence gathering, log/file inspection, structured findings |
| summariser | ` + "`softskills/summariser/<skill>/SKILL.md`" + ` | Condensing output, report structuring |
| (other sub-agents) | ` + "`softskills/<agent>/<skill>/SKILL.md`" + ` | Domain-specific procedures for that agent |

**Prefer sub-agents.** Sub-agents run on cheaper, faster models and gain the most from distilled procedures. If a procedure is about *how to do* something (gather evidence, structure output, run a check), it belongs to the sub-agent that performs it. Reserve leader skills for *when to delegate* and *how to coordinate*.

**Write to multiple agents** when the session contains both a coordination pattern (leader) and an execution pattern (sub-agent). Each is a separate ` + "`softskill_create`" + ` + ` + "`softskill_index_append`" + ` pair with the appropriate ` + "`agent`" + ` value.

The ` + "`agent`" + ` parameter:
- Omit (empty string / absent) → writes to leader root ` + "`softskills/<skill>/SKILL.md`" + `.
- Set to e.g. ` + `"investigator"` + ` → writes to ` + "`softskills/investigator/<skill>/SKILL.md`" + `.

## Inputs

You will receive in the user message:
1. The path to the session's compress audit file (a markdown distillation of the conversation).
2. The path to the session's StateLog JSON (structured: goal, decisions, open_issues, files, tools).
3. The list of currently mounted authored skills (so you can avoid duplication).
4. The known agents in this deployment (so you know which ` + "`agent`" + ` values are valid).

Read each file with run_read. Do not invent paths.

## Workflow (follow in order)

1. Read the inputs. Skip any path that does not exist; carry on with what you have.

2. Identify candidate procedures. Look for multi-step sequences (3 or more concrete actions) that:
   - reached a verifiable success state, and
   - are generalizable beyond the session's specific IDs/paths/usernames, and
   - are non-trivial (a single tool call is not a soft-skill).

   For each candidate, decide which agent it belongs to.

   If no candidate qualifies, output a one-line rationale and stop. DO NOT write anything.

3. Redundancy audit. For each candidate:
   - Use run_glob to discover existing soft-skills: ` + "`softskills/*/SKILL.md`" + ` (leader) and ` + "`softskills/<agent>/*/SKILL.md`" + ` (per-agent). Read relevant SKILL.md files with run_read.
   - Compare against the authored-skills list provided in the prompt.
   - Apply the gating rules below in order. **Skip is the default; only act when the rule explicitly permits it.**

   **Create** an entirely new soft-skill ONLY when ALL of the following hold:
   - Reflector outcome ` + "`success == positive`" + ` (section 6 of the prompt).
   - ` + "`key_insight`" + ` is non-empty (section 6).
   - No near-duplicate exists in the authored-skills list or in the soft-skills you discovered with run_glob.
   - The procedure has ≥3 concrete steps and is generalisable.
   If any of those fail, skip the create — even if you think the procedure is interesting.

   **Update** an existing soft-skill ONLY when:
   - The reflector's ` + "`key_insight`" + ` cleanly extends or refines the existing skill (a new edge case, a removed step, a discovered constraint), AND
   - The improvement is concrete enough to write in the ` + "`reason`" + ` argument (≥20 chars).
   Pure rewrites for style or tone are rejected by the tool — do not attempt them.

   **Delete** an existing soft-skill ONLY when at least ONE of these holds:
   - Per-skill stats (section 7) show ` + "`harmful >= 3`" + ` AND ` + "`harmful > helpful`" + ` — the corpus is telling you the skill is doing more harm than good.
   - Section 8 lists the skill as harmful this session AND the reason mentions "wrong assumptions", "superseded", or an explicit factual error.
   Otherwise: skip. A neutral-but-rare skill is not a deletion candidate.
   Deletion always requires a substantive ` + "`reason`" + ` (≥20 chars). Always prefer skip over delete when in doubt.

   Never create a near-duplicate.

4. Generalize. Strip session-specific identifiers (replace concrete pod names, file paths, user IDs with placeholders or remove them entirely). The skill must read as a procedure, not a story.

5. Write each SKILL.md using the standard layout. Frontmatter MUST contain ONLY these two fields (the loader rejects anything else):
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

6. For each skill to write or delete:
   - New skill: call softskill_create, then softskill_index_append.
   - Updated skill: call softskill_update, then softskill_index_append (idempotent).
   - Deleted skill: call softskill_delete (removes the directory), then softskill_index_remove (removes the index entry).
   The tools reject trivial changes and path traversal — trust their rejections; do not retry with workarounds.

7. Reply with exactly one paragraph: what you created, updated, or deleted; which agents you targeted; and why. No preamble, no farewell.

## Hard rules

- At most ONE soft-skill action (create/update/delete) per target agent per invocation. Acting on three agents means at most three skills total.
- Deletions require both softskill_delete AND softskill_index_remove — never leave a dangling index entry.
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
	// AgentNames lists all known sub-agent names (excluding leader and
	// curator). Passed to the curator prompt so it knows which `agent`
	// values are valid write targets.
	AgentNames []string
}

// NewCurator builds the curator agent. It mounts:
//   - read-only fs tools (run_read, run_glob, run_grep) for reading the
//     audit and statelog files and for discovering existing soft-skills,
//   - the three softskill_* write tools (constrained to SoftSkillsDir).
//
// The curator uses run_glob to discover existing soft-skills across all agent
// directories, so a separate list_softskills toolset is not needed.
// Authored skill names are passed in the user prompt by Curate() instead.
func NewCurator(ctx context.Context, cfg CuratorConfig) (adkagent.Agent, error) {
	if cfg.SoftSkillsDir == "" {
		cfg.SoftSkillsDir = DefaultDir()
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("softskills: curator requires Model")
	}

	tools := fstools.New()
	tools = append(tools, WriteTools(cfg.SoftSkillsDir)...)

	return agentkit.New(agentkit.AgentConfig{
		Name:        "curator",
		Description: "Distils successful session experience into reusable soft-skills.",
		Model:       cfg.Model,
		Tools:       tools,
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
	// AgentNames lists the known sub-agent names (excluding leader and
	// curator) so the curator knows which `agent` values are valid.
	AgentNames []string
	// Outcome is the merged (heuristic + LLM reflector) verdict for
	// this session. Optional — when nil the curator falls back to the
	// pre-Phase-4 prompt that judges success on its own. When set it
	// drives the create/update/delete gating rules in CuratorPrompt.
	Outcome *Outcome
	// Stats is the current persistent softskill stats sidecar. Optional.
	// When set the curator sees per-skill load/helpful/harmful/neutral
	// counts so it can apply the harmful-threshold deletion rule.
	Stats *Stats
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
	b.WriteString("\n4. Known agents you may target with the `agent` parameter (sub-agents only — omit for leader):\n")
	if len(in.AgentNames) == 0 {
		b.WriteString("   (none — only leader-level skills are available this deployment)\n")
	} else {
		for _, a := range in.AgentNames {
			fmt.Fprintf(&b, "   - %s\n", a)
		}
	}
	b.WriteString("\n5. Existing soft-skills: use run_glob on `softskills/*/SKILL.md` (leader) and `softskills/<agent>/*/SKILL.md` (per-agent) to discover them before deciding whether to create or update.\n")

	// 6. Reflector outcome
	b.WriteString("\n6. Reflector outcome for this session: ")
	if in.Outcome == nil {
		b.WriteString("(none — fall back on your own judgement from the audit + statelog)\n")
	} else {
		fmt.Fprintf(&b, "success=%s", in.Outcome.Success.String())
		if ki := strings.TrimSpace(in.Outcome.KeyInsight); ki != "" {
			fmt.Fprintf(&b, "; key_insight=%q", ki)
		} else {
			b.WriteString("; key_insight=(empty)")
		}
		b.WriteString("\n")
	}

	// 7. Per-skill stats (top 20 by LoadedCount)
	b.WriteString("\n7. Per-skill usage stats (top 20 by loaded_count; sidecar at softskills/_stats.json):\n")
	if in.Stats == nil || len(in.Stats.Entries) == 0 {
		b.WriteString("   (none recorded yet)\n")
	} else {
		for _, line := range topStatsLines(in.Stats, 20) {
			fmt.Fprintf(&b, "   - %s\n", line)
		}
	}

	// 8. Reflector's harmful tags this session (with reasons)
	b.WriteString("\n8. Skills the reflector tagged 'harmful' this session, with reasons:\n")
	wroteAny := false
	if in.Outcome != nil {
		// Stable order so the prompt is deterministic across runs.
		keys := make([]string, 0, len(in.Outcome.Tags))
		for k, t := range in.Outcome.Tags {
			if t == "harmful" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			reason := strings.TrimSpace(in.Outcome.TagReasons[k])
			if reason == "" {
				reason = "(no reason recorded)"
			}
			fmt.Fprintf(&b, "   - %s: %s\n", k, reason)
			wroteAny = true
		}
	}
	if !wroteAny {
		b.WriteString("   (none)\n")
	}

	b.WriteString("\nBegin the workflow now. Reply only at the end with the one-paragraph summary.\n")
	return b.String()
}

// topStatsLines returns up to `n` "<key>: loaded=L helpful=H harmful=Ha neutral=Ne"
// lines ordered by descending LoadedCount, with a stable secondary sort
// on the key so the prompt body is deterministic across invocations.
func topStatsLines(s *Stats, n int) []string {
	if s == nil || len(s.Entries) == 0 {
		return nil
	}
	type row struct {
		key string
		e   *StatsEntry
	}
	rows := make([]row, 0, len(s.Entries))
	for k, e := range s.Entries {
		rows = append(rows, row{key: k, e: e})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].e.LoadedCount != rows[j].e.LoadedCount {
			return rows[i].e.LoadedCount > rows[j].e.LoadedCount
		}
		return rows[i].key < rows[j].key
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%s: loaded=%d helpful=%d harmful=%d neutral=%d",
			r.key, r.e.LoadedCount, r.e.Helpful, r.e.Harmful, r.e.Neutral))
	}
	return out
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
