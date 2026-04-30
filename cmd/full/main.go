// cmd/full — the all-in-one harness binary. Wires every component together
// and hands control to ADK's full launcher (interactive console + web).
//
// Run modes:
//
//	go run ./cmd/full console   # interactive REPL
//	go run ./cmd/full web       # local web UI
package main

import (
	"context"
	"fmt"
	"os"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"

	"github.com/blouargant/agent-toolkit/core/agentkit"
	"github.com/blouargant/agent-toolkit/core/events"
	"github.com/blouargant/agent-toolkit/core/permissions"
	fstools "github.com/blouargant/agent-toolkit/core/tools"
	"github.com/blouargant/agent-toolkit/internal/bg"
	"github.com/blouargant/agent-toolkit/internal/cache"
	"github.com/blouargant/agent-toolkit/internal/compress"
	mcpcfg "github.com/blouargant/agent-toolkit/internal/mcp"
	"github.com/blouargant/agent-toolkit/internal/skills"
	"github.com/blouargant/agent-toolkit/internal/tasks"
	"github.com/blouargant/agent-toolkit/internal/teammates"
	"github.com/blouargant/agent-toolkit/internal/todo"
	"github.com/blouargant/agent-toolkit/internal/worktree"
)

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	llm, err := agentkit.NewModel(ctx)
	if err != nil {
		return err
	}

	// ── Toolsets ─────────────────────────────────────────────────────────
	repo, _ := os.Getwd()
	g := tasks.New("")
	q := bg.NewQueue(32)
	store := todo.NewStore("")

	leadTools := []tool.Tool{}
	leadTools = append(leadTools, fstools.New()...)
	leadTools = append(leadTools, store.Tools()...)
	leadTools = append(leadTools, g.Tools()...)
	leadTools = append(leadTools, worktree.Tools(repo)...)
	leadTools = append(leadTools, q.Tool())

	var toolsets []tool.Toolset
	if ts, err := skills.Toolset(ctx, "skills"); err == nil {
		toolsets = append(toolsets, ts)
	}
	if mc, err := mcpcfg.Load("config/mcp_config.yaml"); err == nil {
		if mts, err := mc.Toolsets(); err == nil {
			toolsets = append(toolsets, mts...)
		}
	}

	be, err := teammates.ChooseBackend()
	if err != nil {
		return fmt.Errorf("mailbox backend: %w", err)
	}
	defer be.Close()
	leadMailbox := teammates.NewAgent("lead", be)
	leadTools = append(leadTools, leadMailbox.Tools()...)

	// Generic specialist sub-agents — domain-agnostic by design. Specialise
	// them by adding tools/skills/MCP servers via config, not by hard-coding
	// a domain in their prompt. Examples of specialisation: drop a
	// `skills/k8s-triage/SKILL.md`, point an MCP server at `kubectl`, add a
	// permissions rule for `kubectl get`. The same binary then becomes a
	// Kubernetes diagnostician with no code change.
	investigator, err := agentkit.New(agentkit.AgentConfig{
		Name:        "investigator",
		Description: "Gathers evidence with read-only tools (file reads, log inspection, MCP queries) and reports findings.",
		Model:       llm,
		Tools:       fstools.New(),
		Toolsets:    toolsets,
		Instruction: "You are an investigator. Use the available tools to collect concrete evidence before drawing any conclusion. Cite each finding with its source (file:line, command output, MCP resource id). Do not modify state.",
	})
	if err != nil {
		return err
	}
	summariser, err := agentkit.New(agentkit.AgentConfig{
		Name:        "summariser",
		Description: "Condenses long content into a structured brief.",
		Model:       llm,
		Instruction: "Reply with: (1) a one-sentence headline, (2) ≤ 7 bullets of the most important facts, (3) a short list of suggested next actions. No fluff.",
	})
	if err != nil {
		return err
	}
	leadTools = append(leadTools,
		agenttool.New(investigator, &agenttool.Config{}),
		agenttool.New(summariser, &agenttool.Config{}),
	)

	lead, err := agentkit.New(agentkit.AgentConfig{
		Name:        "lead",
		Description: "Generic coordinator agent. Specialise it by mounting domain-specific tools, skills, and MCP servers.",
		Model:       llm,
		Tools:       leadTools,
		Toolsets:    toolsets,
		SubAgents:   []adkagent.Agent{investigator, summariser},
		Instruction: `You are a generic Claude-Code-style coordinator. You are not bound to any single domain — what you can do is determined by the tools, skills and MCP servers currently mounted.

Operating method (always, regardless of the task):
  1. RESTATE the user's goal in one sentence and confirm scope before acting on anything irreversible.
  2. PLAN with task_create whenever the work has more than one step. Keep tasks small and verifiable.
  3. INVESTIGATE before you act: call the 'investigator' sub-agent (or read tools yourself) to gather evidence. Never rely on assumptions when a tool can confirm.
  4. ACT in small reversible steps. Prefer tools over shell, prefer dry-runs over mutations.
  5. SUMMARISE long outputs through the 'summariser' sub-agent before reasoning over them.
  6. RESPECT permissions: if a tool call is denied, do NOT retry — report and ask the user.
  7. ESCALATE to the user when ambiguity remains after one round of evidence gathering.

You have no built-in domain expertise. Lean on the mounted skills and tools to discover what is appropriate for the current environment.`,
	})
	if err != nil {
		return err
	}

	loader, err := adkagent.NewMultiLoader(lead, investigator, summariser)
	if err != nil {
		return err
	}

	// ── Plugins ──────────────────────────────────────────────────────────
	var plugins []*plugin.Plugin
	bus := events.NewBus()
	logger, closeLog, err := events.FileLogger(".agent_events.log")
	if err != nil {
		return err
	}
	defer closeLog()
	for _, ev := range []string{
		events.EventBeforeTool, events.EventAfterTool,
		events.EventBeforeModel, events.EventAfterModel,
		events.EventToolError, events.EventSessionStart, events.EventSessionEnd,
	} {
		bus.On(ev, logger)
	}
	if eb, err := bus.Plugin("events"); err == nil {
		plugins = append(plugins, eb)
	}
	if perms, err := permissions.NewPlugin("perms", "config/permissions.yaml", permissions.StdinAsker{}); err == nil {
		plugins = append(plugins, perms)
	}
	if _, cp, err := cache.Plugin("cache"); err == nil {
		plugins = append(plugins, cp)
	}
	if cmp, err := compress.Plugin("compress", compress.Config{
		MemoryPath: ".agent_memory.md",
		LLM:        llm,
	}); err == nil {
		plugins = append(plugins, cmp)
	}

	cfg := &launcher.Config{
		SessionService: session.InMemoryService(),
		AgentLoader:    loader,
		PluginConfig:   runner.PluginConfig{Plugins: plugins},
	}

	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"console"}
	}
	return full.NewLauncher().Execute(ctx, cfg, args)
}
