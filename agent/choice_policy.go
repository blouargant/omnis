package agent

// choicePolicyBlock tells an answering squad root how to put a decision to the
// user. Appended to every non-router root (see buildSquadInstance); handsBack is
// true when the root has `handoff_to_router` mounted, i.e. when "this isn't mine"
// is an option it can actually exercise.
//
// The rule exists because prose and `AskUserQuestion` are not two styles of the
// same thing — they differ in whether the turn survives:
//
//   - A question written in prose is a tool-call-free response, so ADK's flow loop
//     treats it as final and ENDS the run. The user must retype an answer, and the
//     agent restarts from a fresh turn having lost whatever it had in flight.
//   - `AskUserQuestion` blocks inside the tool call: the answer comes back as the
//     tool result and the agent continues in the SAME turn, with its context and
//     partial work intact.
//
// Observed live: the Helper answered a "does a way to do X exist?" question with a
// prose either/or ("shall I search the omnis registry, or is this a general
// architecture question?"). The turn ended there, and to the user it read as the
// agent simply stopping — the request was never answered.
//
// The second half of the block is the guard that keeps this from becoming the
// opposite failure. That same prose question was ALSO asking the user to resolve a
// *routing* decision the agent is supposed to resolve itself (its own instruction
// tells it to hand such questions back to the router). Turning that into a tidy
// button menu would have institutionalised it: a menu is for a choice only the
// user can make, never a way to offload one the agent owns.
func choicePolicyBlock(handsBack bool) string {
	b := "\n\n## Putting a choice to the user\n\n" +
		"When the user must decide before you can continue and the options are enumerable, " +
		"call `AskUserQuestion` (`kind: \"single\"` or `\"confirm\"`, 2-4 `choices`, " +
		"`allow_text: true` when free text also fits). A question written in prose ends your " +
		"turn and loses the work in flight; the tool keeps the turn alive. " +
		"Only ask about what only the user can decide — a preference, a trade-off, an ambiguity " +
		"in their intent, permission for something consequential — never which squad or " +
		"specialist should handle the request, which tool to use or whether a lookup is worth " +
		"doing: decide those yourself.\n"
	if handsBack {
		b += "If the request is outside your scope, call `handoff_to_router` instead of asking " +
			"the user what kind of request it is.\n"
	}
	return b
}
