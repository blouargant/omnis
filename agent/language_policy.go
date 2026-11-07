package agent

// languagePolicyBlock tells an answering squad root how to handle a conversation
// held in a language other than English. Appended to every non-router root (see
// buildSquadInstance); delegates is false for a leaderless root, which has no
// sub-agents to instruct.
//
// The rule it encodes is that language is sometimes packaging and sometimes
// payload. A delegation framing ("find X, report file:line") carries no meaning
// that depends on the language it is written in, and English is where the models
// are strongest — but the user's own wording does carry meaning, and a paraphrase
// destroys it before any sub-agent can see it ("qui est le président de la
// République" scopes to France purely by being French). Hence: English framing,
// verbatim user request, translate only at the final boundary.
//
// This is the same guarantee the host already gives one level up — RunWithRouting
// hands every answering squad the user's original parts so the router cannot
// paraphrase the request (see routing.go). It cannot be enforced for delegation
// (the sub-agent request is a free-text string the model authors), so here it is
// an instruction.
func languagePolicyBlock(delegates bool) string {
	b := "\n\n## Language\n\n" +
		"- Reply to the user in the language they wrote in, including in `AskUserQuestion`; " +
		"that is the only place you translate.\n"
	if delegates {
		b += "- Write your instructions to sub-agents in English, but never paraphrase the user's " +
			"request: quote it verbatim, in their language, beside your framing — its wording " +
			"carries scope a translation loses (\"le président de la République\" means France).\n"
	}
	b += "- Do not translate evidence: quotes, code, errors, logs and file contents stay in " +
		"their original language.\n"
	return b
}
