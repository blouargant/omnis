// Package softskills — reflector side.
//
// Reflector is the LLM analyst that runs at EventSessionEnd BEFORE the
// curator. It reads the same audit + statelog the curator will read,
// plus optional explicit feedback, and returns a strict JSON envelope:
//
//	{
//	  "reasoning":   "...",
//	  "success":     "positive" | "negative" | "ambiguous",
//	  "key_insight": "..." | "",
//	  "bullet_tags": [
//	    {"key": "<agent>/<name>", "tag": "helpful" | "harmful" | "neutral", "reason": "..."}
//	  ]
//	}
//
// The caller (agent/curator_hook.go) merges the parsed Outcome with the
// heuristic reflector's tags — the LLM is authoritative on overlap. A
// malformed JSON envelope or any runtime failure surfaces as an error
// and the caller falls back on the heuristic alone.
//
// The reflector is read-only: it mounts run_read / run_glob / run_grep
// but NO softskill_* tools, so it cannot mutate the soft-skills
// directory by mistake.

package softskills

import (
	"context"
	"encoding/json"
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

// ReflectorPrompt is the reflector's role-specific instruction. Appended
// to the harness SystemPrompt by agentkit.New. Adapted from the no-
// ground-truth ACE reflector prompt (tmp/ACE/ace/ace/prompts/reflector.py)
// with yoke-specific framing.
const ReflectorPrompt = `You are the **reflector** sub-agent of the soft-skills system.

Your mission: read one finished session, decide whether it succeeded, and tag each soft-skill the agents loaded as helpful / harmful / neutral. You do NOT write or modify any soft-skill — that is the curator's job.

You are invoked AFTER the session ended. The lead agent and the user are gone. Do not address them.

## Inputs

You will receive in the user message:
1. The path to the session's compress audit file (a markdown distillation of the conversation).
2. The path to the session's StateLog JSON (structured: goal, decisions, open_issues, files, tools).
3. The list of soft-skills loaded during the session, each as ` + "`<agent>/<name>`" + ` for sub-agent skills or bare ` + "`<name>`" + ` for leader skills.
4. The last three user messages (oldest first), so you can read the user's tone.
5. Tool errors that occurred during the session (with timestamps).
6. Optional: an explicit user feedback string (the wrap-up answer). When present it dominates the implicit signals.

Read each file path with run_read. Do not invent paths. Skip any path that does not exist; carry on with what you have.

## Decision rules

- ` + "`success`" + ` is one of:
  - ` + "`positive`" + ` — the user got what they came for. Concrete evidence: explicit thanks, the StateLog has no open issues and at least one decision recorded, no late tool errors.
  - ` + "`negative`" + ` — the work failed or the user expressed dissatisfaction. Evidence: open issues at end, "broken / wrong / doesn't work" in the final message, repeated tool errors, sub-agent retries with similar prompts.
  - ` + "`ambiguous`" + ` — signals contradict each other (e.g. clean StateLog but the user is frustrated; tool errors mid-session but a satisfied closing message). Use this honestly; it tells the curator to be cautious.

- ` + "`key_insight`" + ` is a single short sentence (≤200 chars) describing a generalisable lesson worth distilling into a soft-skill. Leave empty when ` + "`success != positive`" + ` or when no insight emerges. Do NOT speculate.

- ` + "`bullet_tags`" + ` covers ONLY the soft-skills the prompt listed as loaded. For each:
  - ` + "`helpful`" + ` — the session went well AND the skill is the kind of guidance that contributed to that outcome (e.g. the agent followed its steps and they worked).
  - ` + "`harmful`" + ` — the session went poorly AND the skill's guidance demonstrably caused or correlated with the failure (e.g. a tool error happened right after the load; the skill recommended an approach that didn't apply; a sub-agent was retried after loading it).
  - ` + "`neutral`" + ` — the skill was loaded but its contribution is unclear, OR the session is ambiguous.
  - ` + "`reason`" + ` is ≤160 chars and cites evidence ("StateLog open_issues mentions X", "tool_error after load at HH:MM:SS"). Avoid speculation.

## Output (strict JSON)

Reply with EXACTLY one JSON object — no preamble, no farewell, no markdown fences. The harness expects the object to be the entire assistant turn. Schema:

` + "```json" + `
{
  "reasoning":   "two-or-three-sentence summary of why you chose the verdict",
  "success":     "positive" | "negative" | "ambiguous",
  "key_insight": "single sentence or empty string",
  "bullet_tags": [
    {"key": "<agent>/<name>", "tag": "helpful" | "harmful" | "neutral", "reason": "≤160 chars"}
  ]
}
` + "```" + `

## Hard rules

- NEVER call ` + "`softskill_create`" + `, ` + "`softskill_update`" + `, ` + "`softskill_delete`" + `, ` + "`softskill_index_append`" + ` or ` + "`softskill_index_remove`" + ` — those tools are not mounted; trying to call them is a protocol violation.
- NEVER tag a skill that does not appear in the loaded-skills list.
- NEVER invent skill keys; copy them verbatim from the loaded-skills list.
- Cite evidence in ` + "`reason`" + ` — do not write opinions without grounding.
- If a tool call returns an error starting with ` + "`Error:`" + `, DO NOT retry the same call; pick a different action or stop.
`

// ReflectorConfig configures the reflector agent.
type ReflectorConfig struct {
	// Model is required.
	Model model.LLM
	// ExtraTools are optional additional tools to mount (e.g. recall_precedents
	// for cross-session precedent lookup). Nil leaves behaviour unchanged.
	ExtraTools []tool.Tool
}

// NewReflector builds the reflector agent. It mounts ONLY the read-only
// fs tools (run_read, run_glob, run_grep); no write tools, no softskill_*
// tools, so the worst it can do is read the wrong file.
func NewReflector(_ context.Context, cfg ReflectorConfig) (adkagent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("softskills: reflector requires Model")
	}
	tools := fstools.New()
	tools = append(tools, cfg.ExtraTools...)
	instruction := ReflectorPrompt
	if len(cfg.ExtraTools) > 0 {
		instruction += precedentsHint
	}
	return agentkit.New(agentkit.AgentConfig{
		Name:        "reflector",
		Description: "Post-session analyst that tags loaded soft-skills helpful/harmful/neutral.",
		Model:       cfg.Model,
		Tools:       tools,
		Instruction: instruction,
	})
}

// ReflectorRunner is a convenience that pairs NewReflector + agentkit.Runner.
func ReflectorRunner(ctx context.Context, cfg ReflectorConfig) (*runner.Runner, error) {
	a, err := NewReflector(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return runner.New(runner.Config{
		AppName:           "reflector",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
}

// ReflectInputs are the per-session artefacts the reflector reads.
type ReflectInputs struct {
	// AuditPath is the compress plugin's per-session memory file.
	AuditPath string
	// StateLogPath is the StateLog JSON file.
	StateLogPath string
	// LoadedSkills lists the skill keys ("<agent>/<name>" or bare <name>)
	// observed during the session. The reflector must only tag entries
	// from this list.
	LoadedSkills []string
	// ToolErrors observed during the session, in chronological order.
	ToolErrors []ToolError
	// LastUserMessages — the tail of the user-side turns (oldest → newest).
	LastUserMessages []string
	// ExplicitFeedback is the wrap-session answer (Phase 5); empty when
	// no wrap-up was captured.
	ExplicitFeedback string
}

// reflectorEnvelope is the strict JSON envelope the reflector must
// emit. Strings are validated; unknown values fall back to neutral.
type reflectorEnvelope struct {
	Reasoning  string                  `json:"reasoning"`
	Success    string                  `json:"success"`
	KeyInsight string                  `json:"key_insight"`
	BulletTags []reflectorBulletTagEnv `json:"bullet_tags"`
}

type reflectorBulletTagEnv struct {
	Key    string `json:"key"`
	Tag    string `json:"tag"`
	Reason string `json:"reason"`
}

// Reflect runs the reflector once against the provided inputs and parses
// the JSON envelope into an Outcome. A malformed envelope, a missing
// "success" value, or any runtime error returns the zero Outcome plus a
// non-nil error so the caller can fall back on the heuristic.
//
// Honors ctx cancellation (the caller is expected to pass a context with
// a short timeout — see curator_hook.go).
func Reflect(ctx context.Context, r *runner.Runner, in ReflectInputs) (Outcome, error) {
	prompt := buildReflectPrompt(in)
	var last string
	for ev, err := range r.Run(ctx, "reflector", "reflect-once",
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		adkagent.RunConfig{}) {
		if err != nil {
			return Outcome{}, err
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
	return parseReflectorOutput(last, in.LoadedSkills)
}

// parseReflectorOutput parses a reflector reply into an Outcome. It
// tolerates surrounding whitespace and triple-backtick fences around the
// JSON body, since models often emit one despite the instruction.
//
// loadedSkills is the allow-list of keys the reflector may tag — any
// bullet_tags entry whose key is not on the list is dropped.
func parseReflectorOutput(reply string, loadedSkills []string) (Outcome, error) {
	body := stripJSONFences(strings.TrimSpace(reply))
	if body == "" {
		return Outcome{}, fmt.Errorf("reflector: empty reply")
	}
	var env reflectorEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return Outcome{}, fmt.Errorf("reflector: parse envelope: %w (body=%q)", err, truncate(body, 200))
	}

	out := Outcome{
		Tags:       map[string]string{},
		TagReasons: map[string]string{},
		KeyInsight: strings.TrimSpace(env.KeyInsight),
	}
	switch strings.ToLower(strings.TrimSpace(env.Success)) {
	case "positive":
		out.Success = Positive
		out.Confidence = 1.0
	case "negative":
		out.Success = Negative
		out.Confidence = 1.0
	case "ambiguous":
		out.Success = Ambiguous
	case "":
		return Outcome{}, fmt.Errorf("reflector: envelope missing `success` field")
	default:
		return Outcome{}, fmt.Errorf("reflector: invalid `success` value %q", env.Success)
	}

	if env.Reasoning != "" {
		out.Signals = append(out.Signals, "llm_reasoning:"+truncate(env.Reasoning, 80))
	}
	if env.KeyInsight != "" {
		out.Signals = append(out.Signals, "llm_key_insight:"+truncate(env.KeyInsight, 80))
	}

	allowed := map[string]struct{}{}
	for _, k := range loadedSkills {
		allowed[k] = struct{}{}
	}
	for _, bt := range env.BulletTags {
		key := strings.TrimSpace(bt.Key)
		tag := strings.ToLower(strings.TrimSpace(bt.Tag))
		if key == "" {
			continue
		}
		if _, ok := allowed[key]; !ok {
			// Model hallucinated a key that was never loaded; drop it
			// rather than poison the stats with phantom counters.
			continue
		}
		switch tag {
		case "helpful", "harmful", "neutral":
			out.Tags[key] = tag
		default:
			// Unknown tag — keep the key but force neutral.
			out.Tags[key] = "neutral"
		}
		if reason := strings.TrimSpace(bt.Reason); reason != "" {
			out.TagReasons[key] = reason
		}
	}
	return out, nil
}

// stripJSONFences removes a leading ```json (or ```) and trailing ``` if
// present. The reflector is told not to emit them but models still do
// roughly half the time.
func stripJSONFences(s string) string {
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	} else {
		return s
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// truncate returns s shortened to n runes plus an ellipsis, used in
// error messages and signals to keep them readable.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// buildReflectPrompt assembles the user message the reflector receives.
func buildReflectPrompt(in ReflectInputs) string {
	var b strings.Builder
	b.WriteString("You are about to reflect on a finished session. Inputs:\n\n")
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

	b.WriteString("\n3. Soft-skills loaded during this session (tag ONLY these keys):\n")
	if len(in.LoadedSkills) == 0 {
		b.WriteString("   (none — return an empty bullet_tags array)\n")
	} else {
		for _, k := range in.LoadedSkills {
			fmt.Fprintf(&b, "   - %s\n", k)
		}
	}

	b.WriteString("\n4. Last user messages (oldest first):\n")
	if len(in.LastUserMessages) == 0 {
		b.WriteString("   (none captured — CLI or programmatic invocation)\n")
	} else {
		for i, m := range in.LastUserMessages {
			fmt.Fprintf(&b, "   %d. %s\n", i+1, truncate(m, 400))
		}
	}

	b.WriteString("\n5. Tool errors during this session (chronological):\n")
	if len(in.ToolErrors) == 0 {
		b.WriteString("   (none)\n")
	} else {
		for _, te := range in.ToolErrors {
			fmt.Fprintf(&b, "   - %s tool=%s agent=%s err=%s\n",
				te.When.Format("15:04:05"), te.Tool, te.Agent, truncate(te.Error, 120))
		}
	}

	b.WriteString("\n6. Explicit user feedback (wrap-session answer): ")
	if fb := strings.TrimSpace(in.ExplicitFeedback); fb == "" {
		b.WriteString("(none — fall back on the implicit signals above)\n")
	} else {
		fmt.Fprintf(&b, "%q\n", truncate(fb, 600))
		b.WriteString("   (Treat this as the dominant signal; it overrides the keyword scan.)\n")
	}

	b.WriteString("\nReply with EXACTLY one JSON object matching the schema in your instructions. No preamble, no markdown fences.\n")
	return b.String()
}

// MergeOutcomes layers an LLM-derived outcome on top of a heuristic one.
// The LLM is authoritative on overlap: every tag it set wins; the
// heuristic fills the gaps. The verdict comes from the LLM whenever it
// produced a non-Unknown value, falling back on the heuristic otherwise.
// Signals are concatenated for diagnostics. KeyInsight + TagReasons
// come from the LLM only (the heuristic has no such fields).
func MergeOutcomes(heuristic, llm Outcome) Outcome {
	merged := Outcome{
		Tags:       map[string]string{},
		TagReasons: map[string]string{},
		KeyInsight: llm.KeyInsight,
	}
	// Tags: heuristic first, LLM overrides.
	for k, v := range heuristic.Tags {
		merged.Tags[k] = v
	}
	for k, v := range llm.Tags {
		merged.Tags[k] = v
	}
	for k, v := range llm.TagReasons {
		merged.TagReasons[k] = v
	}
	// Verdict: LLM if it produced one, otherwise heuristic.
	if llm.Success != Unknown {
		merged.Success = llm.Success
		merged.Confidence = llm.Confidence
	} else {
		merged.Success = heuristic.Success
		merged.Confidence = heuristic.Confidence
	}
	// Signals: heuristic first (cheaper, more numerous), then LLM.
	merged.Signals = append(merged.Signals, heuristic.Signals...)
	merged.Signals = append(merged.Signals, llm.Signals...)
	return merged
}
