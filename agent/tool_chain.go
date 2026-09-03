package agent

import "google.golang.org/adk/agent/llmagent"

// beforeToolChain assembles a sub-agent's BeforeToolCallback chain, and is the
// single place that order is written down. The first non-nil return
// short-circuits the tool, so the order IS the policy.
//
// hooks BEFORE permissions. With permissions first, a PreToolUse hook that
// refuses a call was consulted only after the user had already approved it: on
// three failed validation attempts the user clicked "allow" three times for
// calls that were then rejected, which trains reflexive approval and degrades
// the permission layer — the only protection that existed before the validation
// work. It also made hooks' documented permissionDecision:"allow" bypass
// (internal/hooks/run.go:76) unreachable dead code, since the prompt had fired.
//
// budget LAST: a call already refused by a hook or by the user must not be
// charged to the turn's budget.
//
// eventsCB is appended unconditionally (it is the observability bridge and must
// see every call); the other three are skipped when nil, which is what makes a
// build with no hooks engine or no ceiling byte-identical to before.
func beforeToolChain(eventsCB, hooksCB, permCB, budgetCB llmagent.BeforeToolCallback) []llmagent.BeforeToolCallback {
	chain := []llmagent.BeforeToolCallback{eventsCB}
	for _, cb := range []llmagent.BeforeToolCallback{hooksCB, permCB, budgetCB} {
		if cb != nil {
			chain = append(chain, cb)
		}
	}
	return chain
}
