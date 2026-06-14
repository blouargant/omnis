package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	fstools "github.com/blouargant/yoke/core/tools"
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

// Run executes every hook command configured for (event, subject), piping in as
// stdin JSON and interpreting each command's exit code + stdout per Claude
// Code's protocol. cwd is the working directory for the commands (and the hook
// input's "cwd"); defaultTimeout applies when a command sets none.
//
// Aggregation: any deny/block (exit 2, decision="block", permissionDecision=
// "deny") yields DecisionBlock with the first reason. An explicit
// permissionDecision="allow" (and no deny) yields DecisionAllow. additionalContext
// fragments are joined with blank lines. Non-blocking command errors (exit codes
// other than 0/2, timeouts, safety-floor blocks) are logged to stderr and do not
// stop the turn — matching Claude Code, only exit code 2 is treated as blocking.
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
			continue
		}
		if res.TimedOut {
			fmt.Fprintf(os.Stderr, "[hooks] %s: command timed out: %s\n", event, cmd.Command)
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
		switch strings.ToLower(hs.PermissionDecision) {
		case "deny":
			out.Decision = DecisionBlock
			if out.Reason == "" {
				out.Reason = firstNonEmpty(hs.PermissionDecisionReason, jo.Reason)
			}
		case "allow":
			if out.Decision != DecisionBlock {
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
		if out.Decision != DecisionBlock && event == PreToolUse {
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
