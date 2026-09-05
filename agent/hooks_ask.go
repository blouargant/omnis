package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/blouargant/omnis/internal/askuser"
)

// The two choices offered when a hook escalates. Deliberately NOT the five
// scopes of the permission asker: an "allow always" there is persisted as an
// allow rule, which would permanently disable the guard that asked. A hook
// question is per-call by nature.
const (
	choiceHookAllowOnce = "Allow this once"
	choiceHookDeny      = "Deny"
)

// nonInteractive reports whether this process has nobody to answer a prompt, so
// an escalation must be refused instead of left waiting forever.
//
// Opt-IN, and deliberately an explicit declaration rather than something
// inferred. The tempting inference is "does this session have a listener?" —
// but nothing in omnis can answer that question honestly:
//
//   - askuser.Registry.SetNotify installs ONE process-wide callback
//     (func(Question)), set once in BuildInfrastructure to emit a bus event. It
//     carries no per-session subscriber knowledge and is never replaced by a
//     surface, so it cannot report who is listening to what.
//   - The thing that DOES count listeners, sessionPushBroadcaster.subs, is the
//     wrong unit: ask_user reaches browsers over the GLOBAL /api/events stream
//     ("Live ask_user / ask_user_cancel for any session"), so a browser
//     displaying no session at all can still answer this one.
//   - Worst, /api/events REPLAYS every pending question on connect, precisely
//     so a question outlives having no listener — and the permission asker
//     waits on the tool's run context for the same reason, so a Stop ends the
//     wait while "a mere client disconnect does not". Treating "no subscriber
//     right now" as "nobody will ever answer" would invert that deliberate
//     design and turn a page reload, a network blip, or a user who closed the
//     tab meaning to come back into a hard block on their work.
//
// A CI run or a bench, by contrast, KNOWS it is unattended. Letting it say so
// costs one env var and has no false positives, where the inference has a whole
// class of them.
func nonInteractive() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OMNIS_NON_INTERACTIVE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// canEscalate reports whether a hook's escalation can reach anyone at all.
// Read by the caller BEFORE asking, so a refusal it causes can say what
// actually happened rather than claiming the user declined.
func canEscalate(reg *askuser.Registry) bool {
	return reg != nil && !nonInteractive()
}

// askHookPermission puts a hook's escalation to the user and reports whether to
// allow this one call.
//
// ctx is the tool's run context, which gives the right lifetime for free: an
// unanswered card is ended by a Stop / session end / shutdown but survives a
// mere client disconnect, so a backgrounded tab keeps the question pending.
//
// With nobody able to answer it denies: nobody is going to authorise the call,
// and proceeding unvalidated is the one outcome the guard exists to prevent.
// That is either a nil registry — a caller-passes-nil case only, since every
// shipped surface builds one (Infrastructure sets AskUserRegistry
// unconditionally), so in practice it is tests and embedders — or an explicit
// OMNIS_NON_INTERACTIVE, which is what an unattended run (a bench, CI) sets to
// say so. Without either, a real run has a registry and therefore WAITS on the
// question rather than auto-denying, which is the correct default: the deny is
// for when waiting cannot end, not merely when nobody has answered yet.
func askHookPermission(ctx context.Context, reg *askuser.Registry, sid, toolName, reason string) bool {
	if !canEscalate(reg) {
		return false
	}
	prompt := fmt.Sprintf("**A validation hook is refusing `%s`.**\n\n%s\n\nAllow this call anyway?", toolName, reason)
	ans, err := reg.Ask(ctx, sid, askuser.Question{
		Kind:    askuser.KindSingle,
		Prompt:  prompt,
		Choices: []string{choiceHookAllowOnce, choiceHookDeny},
		Default: choiceHookDeny,
	})
	if err != nil || ans.Cancelled || len(ans.Selected) == 0 {
		return false
	}
	return ans.Selected[0] == choiceHookAllowOnce
}
