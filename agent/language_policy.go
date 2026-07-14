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
		"The language you speak to the user in and the language you work in are separate " +
		"choices. Language is sometimes only packaging, and sometimes it carries meaning — " +
		"keep the two apart:\n\n" +
		"- **Reply to the user in the language they wrote in.** That applies to every message " +
		"you address to them, including questions you ask with `ask_user`. This is the only " +
		"place you translate.\n"

	if delegates {
		b += "- **Write your instructions to sub-agents in English.** The framing you author " +
			"(\"find X\", \"load skill Y\", \"report file:line\") means the same thing in any " +
			"language, sub-agents' own prompts are in English, and the models perform best " +
			"there.\n" +
			"- **But never paraphrase the user's request into English.** Their exact wording " +
			"carries information a translation destroys: *\"qui est le président de la " +
			"République\"* scopes the question to France purely by being French — rendered as " +
			"\"who is the president\" it could be any country. So when you delegate, quote the " +
			"user's request **verbatim, in their language**, alongside your English framing:\n\n" +
			"      Find out who currently holds this office and cite the source. The user's exact\n" +
			"      question, which you must not re-interpret (its wording carries the scope):\n" +
			"      \"Qui est le président de la République ?\"\n\n"
	}

	b += "- **Do not translate evidence.** Quotes, code, error messages, log lines and file " +
		"contents stay in their original language. A translated quote is an interpretation, " +
		"and the verbatim text is what lets you and the user check the answer. Translate only " +
		"your own final prose.\n"

	if delegates {
		b += "\nA sub-agent searching the web chooses for itself which language to search in — " +
			"that depends on where the answer lives, not on which language the user used. Do " +
			"not dictate it to them.\n"
	}
	return b
}
