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
		"When you genuinely need the user to decide before you can continue, and the options " +
		"are enumerable, **call `AskUserQuestion`** — do not write the question as prose. Use " +
		"`kind: \"single\"` (or `\"confirm\"`) with 2-4 concrete `choices`, and set " +
		"`allow_text: true` when a free-form answer also makes sense.\n\n" +
		"This is not a style preference. A question written in prose **ends your turn**: the " +
		"user has to retype an answer and you start again from a new turn, losing the work you " +
		"had in flight. `AskUserQuestion` keeps the turn alive — the answer arrives as the " +
		"tool result and you carry straight on. Asked in prose, a stopped turn reads to the " +
		"user as the agent having simply given up.\n\n" +
		"**Only ask about what only the user can decide** — a preference, a trade-off, an " +
		"ambiguity in *their* intent, or permission for something consequential. Never use it " +
		"to hand back a decision that is yours: which squad or specialist should handle the " +
		"request, which tool or source to consult, or whether some lookup is worth doing. " +
		"Decide those yourself.\n"

	if handsBack {
		b += "\nIn particular, if the honest answer is that the request is outside your scope, " +
			"**call `handoff_to_router`** — do not ask the user to choose which kind of request " +
			"it was. Working out where a request belongs is the router's job, not theirs.\n"
	}
	return b
}
