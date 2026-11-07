// Package agentkit centralises agent construction so the per-component
// `cmd/sXX/main.go` runners stay tiny. It picks the model from env, applies
// sensible defaults, and exposes one-shot Run helpers.
package agentkit

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/llm"
)

// DefaultModel is kept for documentation; the actual default is owned by
// core/llm and depends on OMNIS_PROVIDER.
const DefaultModel = "gpt-4o-mini"

// SystemPrompt is the harness's universal operating contract — domain-
// agnostic by design. It describes a *method*, not a domain. Specialise an
// agent by mounting the right tools, skills and MCP servers, not by
// rewriting this prompt. Per-agent context goes into AgentConfig.Instruction
// and is appended below.
const systemPromptTemplate = `You are an agent in the omnis harness. What you can do is exactly the tools,
skills and MCP servers mounted for you: never assume a capability no tool
exposes, and never refuse a task because your role seems not to fit.

Method:
%STEP1%  2. PLAN with todo_write when the task has more than one step; keep the
     steps small and verifiable.
  3. INVESTIGATE with read-only tools before acting; never assume what a
     tool can confirm.
  4. ACT in small, reversible steps. Prefer dedicated tools over raw shell,
     and dry-runs over mutations.
  5. REPORT after each action: what you did, what you saw, what comes next.
  6. RESPECT permissions: if a tool call is denied, do NOT retry — surface
     it to the user and ask how to proceed.
  7. PERSIST: when a tool fails or an assumption proves wrong, try another
     approach (tool, query, angle) before asking the user. Escalate only
     when the request is ambiguous in a way that changes the outcome, an
     irreversible change lacks a clear mandate, or the alternatives are
     exhausted. If you proceed on an assumption, say so.

Tools: use a skill when one matches the task. Run commands that take more
than a few seconds with bash_background. Delegate to a sub-agent by calling
its tool; teammate_ask / teammate_tell are only for messages to other
sessions; never use transfer_to_agent. Skip any step whose tool you lack.`

// rootStep1 opens a user-facing root agent's reply with a restatement the
// user can check.
const rootStep1 = `  1. RESTATE the user's request in your own words as the very first thing
     in your reply, before any tool call, as "<acknowledgement>: <your
     restatement>." — <acknowledgement> is one word meaning "Understood" in
     the user's language ("Compris", "Entendido", "Verstanden"), and the
     whole line is in that language. Then confirm scope before any
     irreversible action. Skip this only for short replies to your previous
     turn ("yes", "ok", "continue", "stop"…).
`

// subAgentStep1 replaces it for a sub-agent. A sub-agent is called as a tool
// by another agent, runs once, and returns: nobody can answer a question it
// asks. Given the root's step 1 ("restate, then confirm scope") a
// research_critic replied with a scope restatement and a request for
// confirmation instead of its review — a wasted 9 s call the leader had to
// repeat — and printed the "<acknowledgement>" placeholder literally.
const subAgentStep1 = `  1. START WORKING IMMEDIATELY. You are a sub-agent: another agent called
     you as a tool, you run once, and your final reply is its result.
     Nobody can answer you, so do not restate the task, ask for
     confirmation or announce your plan — do the work. If the task is
     ambiguous, take the most reasonable reading and state that assumption
     in one line at the top of your result.
`

// SystemPrompt is the operating contract of a user-facing (root) agent.
var SystemPrompt = strings.Replace(systemPromptTemplate, "%STEP1%", rootStep1, 1)

// SubAgentSystemPrompt is the same contract for an agent invoked as a tool by
// another agent: step 1 says to start working instead of restating the task
// and confirming scope, and escalation goes to the calling agent.
var SubAgentSystemPrompt = strings.NewReplacer(
	"%STEP1%", subAgentStep1,
	"surface\n     it to the user and ask how to proceed.", "report\n     it in your result instead.",
	"before asking the user.", "before giving up.",
).Replace(systemPromptTemplate)

// NewModel selects an LLM via core/llm based on OMNIS_PROVIDER. See the
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

	// SubAgent selects SubAgentSystemPrompt: the agent is invoked as a tool
	// by another agent (one run, no user to confirm with).
	SubAgent bool

	// NoContract leaves out the operating contract: the instruction is used
	// alone. For the Omnis router, which only routes — its text is never shown,
	// so a "restate the request, plan, report" method only costs tokens (and
	// output) on every message.
	NoContract bool

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
	// OnModelErrorCallbacks may replace a failed model call's error with a
	// response (sub-agent context-overflow recovery, internal/compress).
	OnModelErrorCallbacks []llmagent.OnModelErrorCallback
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
	if cfg.SubAgent {
		instr = SubAgentSystemPrompt
	}
	if cfg.NoContract {
		instr = cfg.Instruction
	} else if cfg.Instruction != "" {
		instr += "\n\n" + cfg.Instruction
	}
	return llmagent.New(llmagent.Config{
		Name:                  cfg.Name,
		Description:           cfg.Description,
		Instruction:           instr,
		Model:                 cfg.Model,
		Tools:                 cfg.Tools,
		Toolsets:              cfg.Toolsets,
		SubAgents:             cfg.SubAgents,
		BeforeToolCallbacks:   cfg.BeforeToolCallbacks,
		AfterToolCallbacks:    cfg.AfterToolCallbacks,
		OnToolErrorCallbacks:  cfg.OnToolErrorCallbacks,
		BeforeModelCallbacks:  cfg.BeforeModelCallbacks,
		AfterModelCallbacks:   cfg.AfterModelCallbacks,
		OnModelErrorCallbacks: cfg.OnModelErrorCallbacks,
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
