# Agent-instruction drafting assistant (web UI Settings)

**Date:** 2026-07-19
**Surface:** web UI only (Settings → Fleet → Agent detail)
**Status:** design approved, pre-implementation

## Problem

The web UI already has a **collection-context drafting assistant**: a small
Helper-backed chat embedded in the collection-context modal that helps the user
*write* a collection's instructions and *adapt* its memory. The assistant
proposes text inside fenced blocks, which the client turns into **Apply** buttons
that fill the editable fields (propose-then-commit — nothing is written to disk
until the user reviews it and saves).

Agents have an analogous authoring surface — the **Instruction Set** section of
the agent detail panel, with a public **Description** and a **System
instruction** — but no drafting assistant. Writing a good agent instruction from
scratch is exactly the kind of task the collections assistant helps with. This
feature brings the same affordance to agent instructions.

## Prior art (what we mirror)

- `wireCollectionAssistant(overlay, name)` — [web/app.js](../../../web/app.js)
  (~L6173): restructures the collection-context modal into `[fields | chat]`,
  wires a hidden Helper session, extracts fenced ` ```instructions ` /
  ` ```memory ` blocks into **Apply** buttons, and prepends a per-turn preamble
  (collection name + current field values).
- `extractCollectionDrafts(md)` — [web/app.js](../../../web/app.js) (~L6159):
  regex-extracts the fenced draft blocks.
- `ensureCollectionAsstSession` / `createCollectionAsstSession` — hidden,
  reusable Helper session cached in `localStorage`, published on `window` so the
  global-events stream skips its session-scoped events.
- `buildAssistant()` — [web/settings.js](../../../web/settings.js) (~L9571): the
  in-Settings assistant, which already demonstrates that **settings.js reaches
  app.js globals** (`apiFetch`, `parseSSE`, `renderMarkdown`, `isRoutingTool`,
  `showToast`, `escHtml`, `tr`, `uiModalShell` are all top-level globals loaded
  before settings.js).

## Decisions (from brainstorming)

1. **Layout:** a focused **modal** that mirrors the collections assistant
   (chosen over an inline side-panel — the agent instruction editor sits in a
   narrow scrolling column with no room for a side-by-side chat).
2. **Fields drafted:** **both** the System instruction and the public
   Description (the two analogues of collections' instructions + memory).
3. **Context:** the assistant **is told the agent's capabilities** (tools,
   skills, model, team) — an improvement over collections' name-only context —
   so drafts reference what the agent can actually do.

## Design

### 1. Trigger & gating

A `✦ Assistant` button in the **Instruction Set** section header
([web/settings.js](../../../web/settings.js) `renderAgentDetail`, ~L4051-4056),
shown **only for editable agents** — the same condition that makes the
instruction textarea editable (`!isBuiltin` and not read-only). Built-in agents
render their read-only fields (built-in defaults) with **no** button — there is
nothing to apply or save for them.

### 2. Modal structure

Built with `uiModalShell(title)` + a new `agent-instr-modal` class. The body is
split `[fields | chat]`, **reusing the collections' presentational classes**
(`.cc-split`, `.cc-asst`, `.cc-asst-head/-transcript/-status/-composer/-input/
-send/-msg/-apply*`, `.cc-field-asst`, `.cc-ta-wrap`) so visual parity is
guaranteed and no CSS is duplicated. These classes are documented as **shared by
both the collection-context and agent-instruction assistants** (a comment in the
CSS partial + CLAUDE.md note).

- **Left (fields):**
  - a **Description** `<input>` (single line), seeded from the current value;
  - a **System instructions** `<textarea>`, seeded from the current value;
  - each carries a floating `✦` field-button (`.cc-field-asst` inside a
    `.cc-ta-wrap`) that opens/focuses the chat and sets a **field-specific
    composer placeholder** (a hint, not pre-filled text), exactly like
    collections.
- **Right (chat):** the Helper chat — transcript + status line + composer —
  **visible by default** (unlike collections, where the aside is hidden until a
  field button opens it; here the modal *is* the assistant, so it opens with the
  chat shown). A header with a title + close (`✕`) control.

### 3. Drafting protocol

The per-turn preamble instructs the model to wrap any proposed field text in
fenced blocks:

- ` ```instruction ` … ` ``` ` for the system instruction,
- ` ```description ` … ` ``` ` for the public description.

`extractAgentInstrDrafts(md)` (a parallel of `extractCollectionDrafts`)
regex-extracts them; each bubble that contains a draft renders an **Apply**
bar (`Apply to instruction` / `Apply to description`).

### 4. Apply & persistence (no working-copy ambiguity)

The modal's left fields **are** the drafting surface (not a throwaway copy).
Editing them — manually, or via an **Apply** button — **syncs straight through
to the inline settings fields**: set the inline element's `.value` and dispatch
an `input` event, reusing the existing inline handlers that set `a.instruction`
/ `a.description`, update the token count, and call `onChange()` (mark the form
dirty).

The modal has only **Close/Done** (+ `✕`); it **never writes to disk**.
Persistence stays with the Settings top-bar **Save**, and **Discard** is the
escape hatch — identical to any inline edit. This is simpler and more honest
than a modal-local Save/Cancel: the modal is just a roomier editing + chat
surface over the same fields.

### 5. Capability-aware preamble

Each send prepends (before the user request):

```
<intro: you help draft the system instruction and public description for the
 agent named "<name>">
AGENT CAPABILITIES:
  tools:   <resolved tool group keys>
  skills:  <skill names>
  model:   <model_ref / model>
  team:    <members / subagents>
CURRENT DESCRIPTION:
  <current description or (empty)>
CURRENT INSTRUCTION:
  <current instruction or (empty)>
USER REQUEST:
  <text>
```

The capability lists are read from the in-memory agent object `a` already held
by `renderAgentDetail` (no extra fetch).

### 6. Session

One hidden, reusable Helper session:

- created via `POST /api/sessions {squad:"helper", hidden:true, title:"Agent
  instruction assistant"}`;
- cached in `localStorage["agent_toolkit_agent_instr_assistant"]`;
- published as `window.__omnisAgentInstrAsstSessionId` and **added to the
  events-skip guard** at [web/app.js](../../../web/app.js) (~L7285) so its
  session-scoped global events never spawn a pane ask-widget, OS notification,
  or sidebar entry;
- `reset_context: true` on the **first send per modal-open**, so drafting for
  one agent never bleeds into the next (a per-open `fresh` flag, like
  collections' `caFreshOpen`);
- 404 self-heal: on a `404` from the messages POST, drop the cached id, recreate
  the session, retry once (mirrors collections).

### 7. SSE handling

Reuse `parseSSE(res)` and handle the same event subset the collections assistant
does: `token` / `message` (accumulate + `renderMarkdown`), `tool_call`
(status line, suppressing routing tools via `isRoutingTool`), `heartbeat`,
`error`, `done`. On finalize, extract drafts and render the Apply bar.

### 8. i18n

New keys under the `set.agent.asst*` namespace, added to
[web/i18n/en.json](../../../web/i18n/en.json) plus fr/es/de, then `make i18n` to
regenerate `web/i18n/locales.js`. Bump the `?v=` on the relevant script tags in
`web/index.html`. Keys cover: button label, modal title, greeting, placeholders,
starters, status strings (thinking/streaming/working), apply-button labels,
applied toast, error. Tool names / model ids / product nouns stay **untranslated**
per the project glossary.

### 9. Placement of the new code

- New self-contained function(s) in **settings.js** (the trigger, the agent
  object `a`, and the inline fields all live in `renderAgentDetail`): e.g.
  `openAgentInstructionAssistant(a, { instrEl, descEl })` plus a small
  `extractAgentInstrDrafts` helper and the session-management helpers. It reuses
  only the established app.js globals — the two IIFEs stay decoupled, mirroring
  how `buildAssistant` already lives in settings.js.
- One line added to the app.js events-skip guard (§6).
- CSS: reuse the existing `.cc-*` assistant classes; add only an
  `.agent-instr-modal` width/spacing rule if needed, and a shared-usage comment.

## Non-goals

- No CLI/TUI surface (Settings is web-only).
- No server changes — reuses the existing Helper squad + `POST
  /api/sessions[/:id/messages]` endpoints and the `hidden` session flag.
- No drafting for built-in agents (their fields are read-only).
- No auto-write to disk from the modal; the Settings Save/Discard governs
  persistence.

## No-op / regression contract

- Built-in agents: byte-identical (no button, read-only fields unchanged).
- Non-settings surfaces: untouched.
- With the modal never opened: no hidden session is created, no events change.
- The reused `.cc-*` CSS classes keep serving the collections assistant
  unchanged (shared, additive usage only).

## Test / verification plan

Manual, in a live web UI (per the project's branch smoke recipe:
`OMNIS_WEB_DIR=$(pwd)/web` + no token):

1. Custom agent → Instruction Set → `✦ Assistant` opens the modal with chat
   shown; built-in agent shows **no** button.
2. Ask the assistant to draft an instruction; verify a fenced block renders with
   an **Apply to instruction** button; Apply fills the modal field **and** the
   inline field (token count updates, form marked dirty).
3. Same for description.
4. Close the modal, confirm the inline field retains the applied text; Settings
   **Save** persists; **Discard** reverts.
5. Open the assistant for a *second, different* agent → first send resets context
   (no bleed from the previous agent).
6. Confirm the hidden assistant session does not appear in the sidebar and raises
   no OS notification / pane ask-widget.
7. `make i18n` regenerates `locales.js`; spot-check fr/es/de fall back to English
   gracefully if a key is missing.
