---
name: ask-user
description: |
  Pattern for asking the user structured questions and gating execution on their
  response. Use whenever the agent needs a choice, confirmation, or free-text
  answer before it can proceed. Renders an interactive widget in the web UI and
  TUI, and falls back to stdin on the console.
metadata:
  tags: "user input, decision gate, interaction pattern, questions, interactive"
  triggers: "ask the user, ask user, ask questions, question the user, gather user input, need user input, interactive planning, present options, ask before proceeding, choice gate, user decision, clarify with user, confirm with user, collect user preferences"
---

# Ask User — Choice Gate Pattern

## How to Ask

Call the `ask_user` tool. It **blocks** until the user responds and returns the
answer as a structured tool result. Do not attempt to simulate a question by
writing prose — use the tool.

### Tool parameters

| Field | Type | Required | Notes |
|---|---|---|---|
| `kind` | string | ✓ | `"single"`, `"multi"`, `"text"`, or `"confirm"` |
| `prompt` | string | ✓ | The question to show the user |
| `choices` | []string | see below | Required for `single`/`confirm`/`multi`; optional for `text` |
| `allow_text` | bool | – | Add a free-text field to a `multi` question |
| `default` | string | – | Pre-selected value hint (display only) |
| `timeout_seconds` | int | – | Override the 5-minute default |

### Kind semantics

| Kind | Widget | Choices | Result fields used |
|---|---|---|---|
| `single` | Radio buttons | 2-4 required | `selected[0]` |
| `confirm` | Radio buttons | Exactly 2 required (Yes/No) | `selected[0]` |
| `multi` | Checkboxes (+ optional text) | ≥1 required | `selected[]`, `text` |
| `text` | Text area | Optional hint choices ignored | `text` |

### Cancelled / timed-out result

If the user clicks Skip/Cancel or the timeout fires, `cancelled: true` is set
in the result. Fall back to the safest default and note what was assumed.

---

## Editorial policy

### Infer before asking

Before calling `ask_user`, run these checks:
1. **Scan conversation history** — did the user already answer this implicitly?
2. **Check stated preferences** — does a loaded skill or softskill supply a default?
3. **Estimate cost of a wrong default** — if low, pick the safest default and note it.

Only gate when the three checks leave the answer genuinely ambiguous.

### Constraints

| Check | Requirement |
|---|---|
| Option count | **2–4 choices max.** More than 4 creates decision paralysis. |
| Labels | **Self-explanatory labels.** Action verb + brief qualifier, not "Option 1". |
| Escape hatch | **Always include an escape hatch** (e.g. "Skip" or "Cancel") as the last choice. |
| Question scope | **One `ask_user` call per turn.** Never stack multiple calls sequentially before reading results. |
| Safety ordering | **Safest/reversible option first** for destructive ops. |
| Blocking order | **Ask the most blocking question first** when multiple unknowns exist. |

### How to gate (CRITICAL)

After calling `ask_user`, the tool blocks — the LLM turn does not end until the
user answers. Do not speculatively continue planning inside the same turn while
waiting. Read the tool result, acknowledge briefly, then branch.

If the result has `cancelled: true`, fall back to the safest default and
continue with a note about the assumption.

### Non-blocking exception

If the gate blocks only *one branch* of a parallelizable plan and unrelated
independent work exists, you MAY call other tools for that unrelated work
**before** calling `ask_user` for the blocked branch, and state what you are
doing in parallel.

---

## Example calls

### Single choice
```json
ask_user({
  "kind": "single",
  "prompt": "Which branch should I target?",
  "choices": ["main", "develop", "feature/my-branch", "Skip — I'll tell you later"]
})
```

### Confirmation
```json
ask_user({
  "kind": "confirm",
  "prompt": "This will delete 47 files. Proceed?",
  "choices": ["No, cancel", "Yes, delete them"]
})
```

### Multi-select
```json
ask_user({
  "kind": "multi",
  "prompt": "Which test suites should I run?",
  "choices": ["unit", "integration", "e2e", "lint"],
  "allow_text": false
})
```

### Free text
```json
ask_user({
  "kind": "text",
  "prompt": "What should I name the new branch?"
})
```

