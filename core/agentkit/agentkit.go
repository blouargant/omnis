// Package agentkit centralises agent construction so the per-component
// `cmd/sXX/main.go` runners stay tiny. It picks the model from env, applies
// sensible defaults, and exposes one-shot Run helpers.
package agentkit

import (
	"context"
	"fmt"
	"iter"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"github.com/blouargant/agent-toolkit/core/llm"
)

// DefaultModel is kept for documentation; the actual default is owned by
// core/llm and depends on YOKE_PROVIDER.
const DefaultModel = "gpt-4o-mini"

// SystemPrompt is the harness's universal operating contract — domain-
// agnostic by design. It describes a *method*, not a domain. Specialise an
// agent by mounting the right tools, skills and MCP servers, not by
// rewriting this prompt. Per-agent context goes into AgentConfig.Instruction
// and is appended below.
const SystemPrompt = `You are a harness agent built on the methodology
proven by Anthropic's Claude Code. The harness is generic and stays
generic: it has no built-in domain knowledge. Your effective capability
on any given run equals exactly the union of the tools, skills and MCP
servers that are currently mounted — nothing more, nothing less.

The same binary becomes a code reviewer, a Kubernetes triage assistant,
a DBA helper, or a release engineer purely by changing what is mounted.
Never assume a capability that isn't exposed as a tool. Never refuse a
task because your "role" doesn't fit — discover the mounted tools and
skills, and let them define the role.

Operating method (apply to every task, in order):
  1. RESTATE the user's request in your own words as the very first
     thing in your reply, BEFORE any tool call, planning, skill
     lookup, or delegation. This lets the user verify you understood
     correctly. Format it as: "Understood: <your restatement>." Then
     confirm scope before any irreversible action.
     EXCEPTION — skip the restatement (and act directly) only for
     trivial acknowledgements such as "yes", "no", "do it",
     "continue", "ok", "go", "stop", and similar short confirmations
     or denials that refer to your previous turn.
  2. PLAN with todo_write or task_create whenever the task has more than
     one step. Keep steps small and individually verifiable.
  3. INVESTIGATE before you act. Read state with read-only tools first;
     never assume what a tool can confirm.
  4. ACT in small, reversible steps. Prefer dedicated tools over raw
     shell. Prefer dry-runs over mutations.
  5. REPORT after every action: what you did, what you observed, what
     you'll do next.
  6. RESPECT permissions: if a tool call is denied, do NOT retry — surface
     it to the user and ask how to proceed.
  7. PERSIST through genuine blockers before escalating. If a tool
     fails, an assumption proves wrong, or evidence is incomplete, try
     at least one alternative approach (different tool, different
     query, different angle) before asking the user. Only escalate
     when (a) the request itself is ambiguous and the choice
     materially changes the outcome, (b) you would need to make an
     irreversible or destructive change without a clear mandate, or
     (c) you have exhausted the reasonable alternatives available
     with the mounted tools. Never guess silently — if you proceed on
     a working assumption, state it explicitly in your reply.

Tool selection rules:
  - Use a 'load_skill' / skill tool when one matches the task: skills
    encode proven, domain-specific procedures.
  - Use bash_background for any command expected to take more than a
    couple of seconds; check the queue between turns.
  - Use teammate_ask / teammate_tell ONLY for follow-up messages to a
    sub-agent that is already running in the background (mailbox-based
    coordination). To DELEGATE a task to a sub-agent, call the
    sub-agent's tool directly by its name (each enabled sub-agent is
    mounted as a tool) — never use teammate_ask for the initial
    delegation, and never use transfer_to_agent.
  - Call compact_now after completing a major sub-task to free context
    for what's next; the harness will summarise the older middle of the
    conversation before the next model call.

If a step in this protocol references a tool you do not have, skip it
silently rather than refusing the task.`

// NewModel selects an LLM via core/llm based on YOKE_PROVIDER. See the
// llm package docs for the supported providers and required env vars.
func NewModel(ctx context.Context) (model.LLM, error) {
	m, err := llm.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("agentkit.NewModel: %w", err)
	}
	return m, nil
}

// AgentConfig is the inputs every demo cares about.
type AgentConfig struct {
	Name        string
	Description string
	Instruction string
	Tools       []tool.Tool
	Toolsets    []tool.Toolset
	SubAgents   []agent.Agent
	Model       model.LLM

	// Optional per-agent callbacks. Use these to attach observers (e.g. an
	// events bus) directly on the agent so they fire even when the agent is
	// invoked outside the runner that owns the toolkit's plugin — most
	// notably when a sub-agent is wrapped by agenttool, which spawns its own
	// internal runner without inherited plugins.
	BeforeToolCallbacks  []llmagent.BeforeToolCallback
	AfterToolCallbacks   []llmagent.AfterToolCallback
	OnToolErrorCallbacks []llmagent.OnToolErrorCallback
	BeforeModelCallbacks []llmagent.BeforeModelCallback
	AfterModelCallbacks  []llmagent.AfterModelCallback
}

// New constructs an llmagent with the article's default system prompt
// prepended to cfg.Instruction.
func New(cfg AgentConfig) (agent.Agent, error) {
	if cfg.Name == "" {
		cfg.Name = "lead"
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("agentkit.New: Model required")
	}
	instr := SystemPrompt
	if cfg.Instruction != "" {
		instr = SystemPrompt + "\n\n" + cfg.Instruction
	}
	return llmagent.New(llmagent.Config{
		Name:                 cfg.Name,
		Description:          cfg.Description,
		Instruction:          instr,
		Model:                cfg.Model,
		Tools:                cfg.Tools,
		Toolsets:             cfg.Toolsets,
		SubAgents:            cfg.SubAgents,
		BeforeToolCallbacks:  cfg.BeforeToolCallbacks,
		AfterToolCallbacks:   cfg.AfterToolCallbacks,
		OnToolErrorCallbacks: cfg.OnToolErrorCallbacks,
		BeforeModelCallbacks: cfg.BeforeModelCallbacks,
		AfterModelCallbacks:  cfg.AfterModelCallbacks,
	})
}

// Runner builds an ADK runner with an in-memory session service and the
// supplied plugins.
func Runner(name string, a agent.Agent, plugins ...*plugin.Plugin) (*runner.Runner, error) {
	return runner.New(runner.Config{
		AppName:           name,
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
		PluginConfig:      runner.PluginConfig{Plugins: plugins},
	})
}

// RunOnce sends `prompt` and returns the resulting event iterator.
func RunOnce(ctx context.Context, r *runner.Runner, prompt string) iter.Seq2[*session.Event, error] {
	return r.Run(ctx, "demo-user", "demo-session",
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		agent.RunConfig{})
}

// RunOnceStream is like RunOnce but asks ADK to stream LLM tokens as they
// are produced (StreamingModeSSE). Use together with stream.Print to render
// text incrementally.
func RunOnceStream(ctx context.Context, r *runner.Runner, prompt string) iter.Seq2[*session.Event, error] {
	return r.Run(ctx, "demo-user", "demo-session",
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		agent.RunConfig{StreamingMode: agent.StreamingModeSSE})
}
