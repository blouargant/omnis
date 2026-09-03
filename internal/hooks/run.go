package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	fstools "github.com/blouargant/omnis/core/tools"
)

// Input is the JSON object piped to a hook command's stdin. Field names match
// Claude Code's hook input schema so existing hook scripts work unchanged. The
// common fields are always present; the rest are populated per event.
type Input struct {
	SessionID      string `json:"session_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	HookEventName  string `json:"hook_event_name"`

	// Tool events (PreToolUse / PostToolUse).
	ToolName     string         `json:"tool_name,omitempty"`
	ToolInput    map[string]any `json:"tool_input,omitempty"`
	ToolResponse map[string]any `json:"tool_response,omitempty"`
	// AgentName is the agent making the call. Omnis extension (additive, so a
	// Claude Code script ignores it): without it a hook cannot apply a rule to
	// one agent — e.g. requiring an ephemeral-resource label for a cleanup
	// agent's deletes but not for a change agent's, which may legitimately
	// delete a real resource.
	AgentName string `json:"agent_name,omitempty"`
	// Attempt is how many times this tool has been called with these exact
	// arguments in this session, 1 on the first. Consecutive is how many calls
	// of this tool were blocked back-to-back before this one. Omnis extensions:
	// the engine reports them, the script decides what they mean, so a
	// retry-then-escalate policy stays in configuration.
	Attempt     int `json:"attempt,omitempty"`
	Consecutive int `json:"consecutive,omitempty"`

	// UserPromptSubmit.
	Prompt string `json:"prompt,omitempty"`

	// Notification.
	Message string `json:"message,omitempty"`

	// PreCompact.
	Trigger            string `json:"trigger,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`

	// SessionStart / SessionEnd.
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`

	// Stop / SubagentStop.
	StopHookActive bool `json:"stop_hook_active,omitempty"`
}

// hookSpecificOutput mirrors Claude Code's hookSpecificOutput object.
type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"` // allow|deny|ask (PreToolUse)
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"` // UserPromptSubmit / SessionStart
}

// jsonOutput is the structured stdout protocol a hook command may emit.
type jsonOutput struct {
	Continue       *bool               `json:"continue,omitempty"`
	StopReason     string              `json:"stopReason,omitempty"`
	Decision       string              `json:"decision,omitempty"` // approve|block (legacy)
	Reason         string              `json:"reason,omitempty"`
	SystemMessage  string              `json:"systemMessage,omitempty"`
	SuppressOutput bool                `json:"suppressOutput,omitempty"`
	HookSpecific   *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// Decision is the aggregated effect of running the matched hook commands for an
// event.
type Decision int

const (
	// DecisionProceed means no hook objected (allow / no opinion).
	DecisionProceed Decision = iota
	// DecisionBlock means a hook denied/blocked the action (PreToolUse deny,
	// UserPromptSubmit block, exit code 2, decision="block").
	DecisionBlock
	// DecisionAllow means a hook explicitly allowed a tool (PreToolUse
	// permissionDecision="allow"), bypassing the permission prompt.
	DecisionAllow
	// DecisionAsk means a hook wants the user to decide (PreToolUse
	// permissionDecision="ask"). Only PreToolUse has a user to ask, so every
	// other consumer ignores it — Blocked() stays false for it deliberately.
	DecisionAsk
)

// Outcome is the combined result of running all matched hook commands for one
// event invocation.
type Outcome struct {
	Decision          Decision
	Reason            string // block/deny reason, surfaced to the model
	AdditionalContext string // joined additionalContext (UserPromptSubmit/SessionStart)
	SystemMessage     string // surfaced to the user
	Continue          *bool  // explicit continue=false from a hook (advisory)
	Ran               int    // number of commands actually executed
}

// Blocked reports whether the outcome denies the action.
func (o Outcome) Blocked() bool { return o.Decision == DecisionBlock }

// Asks reports whether the outcome defers the decision to the user.
func (o Outcome) Asks() bool { return o.Decision == DecisionAsk }

// Run executes every hook command configured for (event, subject), piping in as
// stdin JSON and interpreting each command's exit code + stdout per Claude
// Code's protocol. cwd is the working directory for the commands (and the hook
// input's "cwd"); defaultTimeout applies when a command sets none.
//
// Aggregation across matched hooks: Block > Ask > Allow > Proceed. Any deny/block
// (exit 2, decision="block", permissionDecision="deny") yields DecisionBlock with
// the first reason. A permissionDecision="ask" or legacy decision="approve" (with
// no deny) yields DecisionAsk or DecisionAllow respectively — but ask wins over
// allow, so a permissive hook never cancels another's escalation to user input.
// additionalContext fragments are joined with blank lines.
//
// Non-blocking command errors (exit codes other than 0/2, timeouts, safety-floor
// blocks) are logged to stderr and do not stop the turn, EXCEPT when a command
// opts in via fail_closed — then any non-zero/timeout/blocked result yields
// DecisionBlock instead. The fail_closed flag lets a command enforce its own
// contract and refuse to be ignored.
func (c *Config) Run(ctx context.Context, event, subject string, in Input, cwd string, defaultTimeout time.Duration) Outcome {
	cmds := c.Match(event, subject)
	out := Outcome{Decision: DecisionProceed}
	if len(cmds) == 0 {
		return out
	}

	in.HookEventName = event
	if in.Cwd == "" {
		in.Cwd = cwd
	}
	stdin, err := json.Marshal(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[hooks] %s: marshal input: %v\n", event, err)
		return out
	}

	var contexts []string
	for _, cmd := range cmds {
		if strings.TrimSpace(cmd.Command) == "" {
			continue
		}
		timeout := defaultTimeout
		if cmd.Timeout > 0 {
			timeout = time.Duration(cmd.Timeout) * time.Second
		}
		res := fstools.RunShellCaptured(ctx, cmd.Command, cwd, stdin, timeout)
		out.Ran++

		// Non-zero, non-blocking outcomes: log and move on.
		if res.Blocked {
			fmt.Fprintf(os.Stderr, "[hooks] %s: command refused by safety floor: %s\n", event, res.Stderr)
			if cmd.FailClosed {
				failClosedBlock(&out, event, cmd.Command, "refused by the safety floor")
			}
			continue
		}
		if res.TimedOut {
			fmt.Fprintf(os.Stderr, "[hooks] %s: command timed out: %s\n", event, cmd.Command)
			if cmd.FailClosed {
				failClosedBlock(&out, event, cmd.Command, "timed out")
			}
			continue
		}
		if res.ExitCode == 2 {
			// Blocking error: stderr is the reason fed back to the model.
			out.Decision = DecisionBlock
			if out.Reason == "" {
				out.Reason = strings.TrimSpace(res.Stderr)
			}
			continue
		}
		if res.ExitCode != 0 {
			// Other non-zero: non-blocking error (Claude Code shows stderr to
			// the user but proceeds).
			if s := strings.TrimSpace(res.Stderr); s != "" {
				fmt.Fprintf(os.Stderr, "[hooks] %s: command exited %d: %s\n", event, res.ExitCode, s)
			}
			if cmd.FailClosed {
				failClosedBlock(&out, event, cmd.Command, fmt.Sprintf("exited %d", res.ExitCode))
			}
			continue
		}

		// Exit 0: interpret stdout. A JSON object drives the structured
		// protocol; anything else is additionalContext for the context-adding
		// events and otherwise informational.
		stdout := strings.TrimSpace(res.Stdout)
		if jo, ok := parseJSONOutput(stdout); ok {
			applyJSONOutput(jo, event, &out, &contexts)
			continue
		}
		if stdout != "" && addsContext(event) {
			contexts = append(contexts, stdout)
		}
	}

	if len(contexts) > 0 {
		out.AdditionalContext = strings.Join(contexts, "\n\n")
	}
	return out
}

// failClosedBlock turns a command that produced no usable verdict into a block,
// for commands that opted in via fail_closed. why names the cause so the model
// (and the user) can tell "the guard refused this" from "the guard is broken".
func failClosedBlock(out *Outcome, event, command, why string) {
	out.Decision = DecisionBlock
	if out.Reason == "" {
		out.Reason = fmt.Sprintf("hook did not complete (%s) and is declared fail_closed; refusing the action", why)
	}
	fmt.Fprintf(os.Stderr, "[hooks] %s: fail_closed block (%s): %s\n", event, why, command)
}

// addsContext reports whether plain exit-0 stdout is injected as context for
// this event (Claude Code: UserPromptSubmit and SessionStart).
func addsContext(event string) bool {
	return event == UserPromptSubmit || event == SessionStart
}

// parseJSONOutput tries to decode a hook command's stdout as the structured
// JSON protocol. It only succeeds for a JSON object (leading '{'), so plain text
// is never mistaken for control output.
func parseJSONOutput(stdout string) (jsonOutput, bool) {
	if !strings.HasPrefix(stdout, "{") {
		return jsonOutput{}, false
	}
	var jo jsonOutput
	if err := json.Unmarshal([]byte(stdout), &jo); err != nil {
		return jsonOutput{}, false
	}
	return jo, true
}

// applyJSONOutput folds one command's structured output into the running
// Outcome.
func applyJSONOutput(jo jsonOutput, event string, out *Outcome, contexts *[]string) {
	if jo.SystemMessage != "" && out.SystemMessage == "" {
		out.SystemMessage = jo.SystemMessage
	}
	if jo.Continue != nil && !*jo.Continue {
		v := false
		out.Continue = &v
		if out.Reason == "" {
			out.Reason = firstNonEmpty(jo.StopReason, jo.Reason)
		}
	}

	// Per-event permission / block semantics.
	if hs := jo.HookSpecific; hs != nil {
		// Aggregation across several matched hooks: Block > Ask > Allow > Proceed.
		// A permissive hook must never cancel another hook's deny or escalation.
		switch strings.ToLower(hs.PermissionDecision) {
		case "deny":
			out.Decision = DecisionBlock
			if out.Reason == "" {
				out.Reason = firstNonEmpty(hs.PermissionDecisionReason, jo.Reason)
			}
		case "ask":
			if out.Decision != DecisionBlock {
				out.Decision = DecisionAsk
				if out.Reason == "" {
					out.Reason = firstNonEmpty(hs.PermissionDecisionReason, jo.Reason)
				}
			}
		case "allow":
			if out.Decision != DecisionBlock && out.Decision != DecisionAsk {
				out.Decision = DecisionAllow
			}
		}
		if hs.AdditionalContext != "" {
			*contexts = append(*contexts, hs.AdditionalContext)
		}
	}

	switch strings.ToLower(jo.Decision) {
	case "block":
		out.Decision = DecisionBlock
		if out.Reason == "" {
			out.Reason = jo.Reason
		}
	case "approve":
		if out.Decision != DecisionBlock && out.Decision != DecisionAsk && event == PreToolUse {
			out.Decision = DecisionAllow
		}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
