# Prompt overrides (omnis) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a layered `prompt_overrides.json` replace omnis's shared prompt texts
(operating contract, policy blocks, skill and memory rules, omnis tool
descriptions) by stable id, expose them with the agents each reaches at
`GET /api/prompts`, and let the omnis-benches GEPA tool evolve them.

**Architecture:** Overrides are resolved with the rest of the config into
`RuntimeSettings.PromptOverrides` (hot-reloadable, unknown ids become
`ConfigWarnings`). Each place that writes a shared text asks
`promptText(runtime, id, default)` and records `(id, agent)` in the generation's
reach map. Tool descriptions are replaced by a wrapper that packs itself. No file
⇒ every request byte-identical to today.

**Deviation from the spec:** the spec allowed named placeholders for texts with
generated parts. None are needed: each overridable id covers only fixed wording
(a block's generated list, header or conditional sentence stays in code, and a
conditional sentence gets its own id), so an override cannot drop a placeholder.

**Tech Stack:** Go (ADK v2), gin; Python for the omnis-benches step.

**Spec:** `docs/superpowers/specs/2026-11-23-gepa-prompt-evolution-design.md` (Part 1;
"Part 2" is the omnis-benches tool already built on `feat/gepa-optimizer`).

## Global Constraints

- **No-op contract:** with no `prompt_overrides.json` in any layer, every
  system instruction, tool declaration and user-message reminder is byte-identical
  to today (golden tests, Task 1).
- File shape `{"prompts": {"<id>": "<text>"}}`, merged across layers by key
  (`configedit` `mapColl` on `prompts`, tombstone `prompts_removed`).
- An unknown id, an empty text or a `tool.<Name>` outside the v1 tool list ⇒
  default text, one log line, one entry in `RuntimeSettings.ConfigWarnings`.
  Never a failed resolution.
- Resolved in `ResolveRuntimeSettings`, so `POST /api/config/reload` applies it.
- Ids (v1): `contract.root`, `contract.subagent`, `block.subagents`,
  `block.handoff`, `block.language`, `block.language.delegates`,
  `block.language.evidence`, `block.choice`,
  `block.choice.handoff`, `block.steering`, `skills.rules`, `memory.rules`,
  `memory.reminder`, and `tool.<Name>` for `Read`, `Write`, `Edit`, `MultiEdit`,
  `Grep`, `Glob`, `Bash`, `AskUserQuestion`, `WebFetch`, `WebSearch`,
  `teammate_ask`, `teammate_tell`, `teammate_check`, `teammate_list`,
  `bash_background`, `monitor`, `bg_list`, `bg_cancel`, `bg_output`,
  `todo_write`, `todo_update`, `todo_read`.
- `GET /api/prompts` is token-gated, returns texts only, and is not shown in the
  Settings UI in v1.
- English only in code comments and docs; CLAUDE.md gets a section (Self-Maintenance Rule).

## Review Focus

1. **A conditional block keeps its condition**: overriding `block.choice` must not
   make the handoff sentence appear on a root with routing off, nor
   `block.language.delegates` on a leaderless root — Task 3.
2. **Sub-agents vs roots**: `contract.subagent` must reach sub-agents only, and a
   tool override must reach the tool both on a root and on a sub-agent holding it
   (including a read-only `Bash` and a deferred tool) — Tasks 2 and 5.
3. **Prompt-cache stability**: an override changes nothing between two calls of
   the same generation (the memory block's render cache must key on the rules text,
   or a reload would keep serving the old rules) — Task 4.
4. **A reload removing an override** brings the default back on the next
   generation, and sessions draining on the old one keep the old text — Task 1/2.
5. **A tool override never breaks dispatch**: the model's call reaches the real
   tool, `tool_search` lists the overridden description — Task 5.

---

### Task 1: Resolve overrides, warnings, golden no-op

**Files:**
- Create: `agent/prompt_overrides.go`, `agent/prompt_overrides_test.go`,
  `agent/testdata/prompts_golden/` (one file per golden text)
- Modify: `internal/configedit/merge.go` (sectionSpecs), `agent/runtime_config.go`
  (`RuntimeSettings.PromptOverrides map[string]string`, call in `ResolveRuntimeSettings`)

**Interfaces:**
- Produces:
  - `PromptIDs` — the fixed v1 ids above (exported slice, ordered); `OverridableTools` (the v1 tool names).
  - `resolvePromptOverrides() (map[string]string, []string)` — merged file via
    `configedit.LoadMergedSection("prompt_overrides.json")`, returns valid
    overrides and warnings (`prompt override "<id>": unknown id`, `… empty text`).
  - `promptText(runtime RuntimeSettings, id, def string) string` — override or `def`.

- [ ] **Step 1: Golden capture first.** Write `TestPromptGoldenDefaults` that renders, with today's code and default settings: `agentkit.SystemPrompt`, `agentkit.SubAgentSystemPrompt`, `choicePolicyBlock(true)`, `choicePolicyBlock(false)`, `languagePolicyBlock(true)`, `languagePolicyBlock(false)`, `steeringAwarenessBlock()`, `routerHandoffProtocolBlock()`, the three fixed paragraphs of `buildSubAgentCapabilitiesBlock` (render with one fake agent), `skills` system instruction, `collmem.Rules`, `memoryTurnReminder`, and each v1 tool's `Description()` from its constructor; compare to files in `testdata/prompts_golden/` (written with `-update` flag the first run). Run `go test ./agent -run TestPromptGoldenDefaults -update`, then without `-update`: PASS. Commit the goldens alone (`test(agent): golden prompt texts before overrides`).
- [ ] **Step 2: Write failing tests** `TestResolvePromptOverridesMergesLayers` (system layer sets `block.choice`, user layer sets `contract.root` and `prompts_removed: ["block.choice"]` ⇒ only `contract.root`), `TestUnknownPromptIDIsAWarning` (`block.nope`, `tool.lsp_hover`, empty text ⇒ 3 warnings, map empty, resolution succeeds), `TestPromptTextFallsBackToDefault`.
- [ ] **Step 3: Run** `go test ./agent ./internal/configedit -run 'Prompt' -v` — FAIL.
- [ ] **Step 4: Implement** (`"prompt_overrides.json": {{path: ["prompts"], kind: mapColl}}`; warnings appended to `ConfigWarnings`).
- [ ] **Step 5: Run** — PASS, and `go test ./agent ./internal/configedit` all green.
- [ ] **Step 6: Commit** `feat(agent): resolve prompt_overrides.json`.

### Task 2: Operating contract + reach map

**Files:**
- Modify: `core/agentkit/agentkit.go` (`AgentConfig.Contract string`: non-empty replaces `SystemPrompt`/`SubAgentSystemPrompt`; ignored with `NoContract`), `agent/squad.go`, `agent/build_subagents.go`, `agent/instance.go`
- Create: `agent/prompt_reach.go`, tests in `agent/prompt_overrides_test.go`, `core/agentkit/agentkit_test.go`

**Interfaces:**
- Consumes: `promptText` (Task 1).
- Produces:
  - `type promptReach struct{ mu sync.Mutex; m map[string]map[string]bool }` with `add(id, agent string)` and `snapshot() map[string][]string` (sorted).
  - `Instance.PromptReach map[string][]string` filled at the end of `BuildInstance`.
  - Roots (non-router) record `contract.root`; sub-agents record `contract.subagent`.

- [ ] **Step 1: Write failing tests** `TestAgentkitContractOverride` (Contract replaces the default, NoContract still wins), `TestContractOverrideReachesRootsAndSubAgents` (build a test instance with one squad + one sub-agent and overrides for both ids: the root's instruction starts with the root text, the sub-agent's with the sub-agent text, the router's has neither), `TestPromptReachRecordsAgents` (`contract.root` ⇒ the squad roots, `contract.subagent` ⇒ the sub-agent names), `TestReloadDropsOverride` (two `BuildInstance` calls, second without the file ⇒ default text; the first instance's agent unchanged).
- [ ] **Step 2: Run** `go test ./agent ./core/agentkit -run 'Contract|PromptReach|ReloadDropsOverride' -v` — FAIL.
- [ ] **Step 3: Implement.** Use the test helpers that already build instances in `agent/*_test.go` (e.g. those of `leaderless_subagents_test.go`) rather than new scaffolding.
- [ ] **Step 4: Run** — PASS; `go test ./agent ./core/...` green.
- [ ] **Step 5: Commit** `feat(agent): contract overrides and per-generation prompt reach`.

### Task 3: Policy blocks

**Files:**
- Modify: `agent/choice_policy.go`, `agent/language_policy.go`, `agent/steer_plugin.go`, `agent/routing.go`, `agent/agent.go` (`buildSubAgentCapabilitiesBlock`), `agent/squad.go`, `agent/build_subagents.go`
- Test: `agent/prompt_overrides_test.go`

**Interfaces:**
- Each block function gains `runtime RuntimeSettings` as first parameter and builds
  its text from `promptText` per part:
  - `block.choice` = the main paragraph (without its leading `\n\n## Putting a choice to the user\n\n` header, which stays fixed), `block.choice.handoff` = the handoff sentence (appended only when `handsBack`).
  - `block.language` = bullet 1 (reply language), `block.language.delegates` = bullet 2 (only when `delegates`), `block.language.evidence` = bullet 3; the `## Language` header and the bullet order stay in code.
  - `block.steering`, `block.handoff`: the body after their `##` header.
  - `block.subagents`: the three fixed paragraphs of `buildSubAgentCapabilitiesBlock` (the `# Available Sub-Agents` header and the per-agent list stay generated).
- Every call site records `(id, agent)` in the reach map for the parts it actually used.

- [ ] **Step 1: Write failing tests** `TestBlockOverridesKeepConditions` (override `block.choice` and `block.choice.handoff`: with `handsBack=false` the handoff override is absent; `block.language.delegates` absent on a leaderless root), `TestBlockOverridesReplaceText` (each block id: overridden text present, default absent), `TestSubAgentBlockKeepsGeneratedList` (agent names/descriptions still listed under the overridden rules).
- [ ] **Step 2: Run** `go test ./agent -run 'Block|SubAgentBlock|Golden' -v` — new tests FAIL, golden still PASS.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./agent` — all green (goldens prove the no-op).
- [ ] **Step 5: Commit** `feat(agent): overridable policy blocks`.

### Task 4: Skill rules and memory texts

**Files:**
- Modify: `internal/skills/skills.go` (export `DefaultInstruction`; `ToolsetWith(ctx, names []string, instruction string)`; `Toolset` = `ToolsetWith(ctx, names, "")`), `agent/build_toolsets.go`, `agent/agent.go` (both `skills.Toolset` call sites), `internal/collmem/render.go` (`RenderWith(collection, label string, forRouter bool, rules string)`; `Render` = `RenderWith(..., Rules)`; the render cache key includes a hash of `rules`), `agent/collection_plugin.go` (plugin takes the generation's `memory.rules` / `memory.reminder`)
- Test: `internal/skills/skills_test.go`, `internal/collmem/render_test.go`, `agent/prompt_overrides_test.go`

**Interfaces:**
- Produces: `skills.ToolsetWith`, `collmem.(*Store).RenderWith`; `collectionCtxPlugin` gets `rules, reminder string` arguments (empty ⇒ defaults).
- Reach: `skills.rules` ⇒ agents given a skills toolset; `memory.rules`, `memory.reminder` ⇒ non-router roots.

- [ ] **Step 1: Write failing tests** `TestToolsetWithInstructionOverride`, `TestRenderWithRulesOverride` and `TestRenderCacheKeysOnRules` (same dir, two rules texts ⇒ two different blocks), `TestMemoryReminderOverride` (the appended reminder is the override text), `TestMemoryOverridesSkipRouter`.
- [ ] **Step 2: Run** `go test ./internal/skills ./internal/collmem ./agent -run 'ToolsetWith|RenderWith|RenderCache|MemoryReminder|MemoryOverrides|Golden' -v` — new tests FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/skills ./internal/collmem ./agent` — green.
- [ ] **Step 5: Commit** `feat: overridable skill rules and memory texts`.

### Task 5: Tool descriptions

**Files:**
- Create: `agent/described_tool.go`, `agent/described_tool_test.go`
- Modify: `agent/squad.go:406-407`, `agent/build_subagents.go:158-159`

**Interfaces:**
- Produces: `applyToolDescriptions(runtime RuntimeSettings, agent string, tools []tool.Tool, reach *promptReach) []tool.Tool` — for each tool whose name is in `OverridableTools` and has a `tool.<Name>` override, wraps it in `describedTool{inner runnableTool, desc string}`: `Description()` and `Declaration()` (a copy with `.Description` replaced) return the override; `ProcessRequest` packs **itself** via `toolutils.PackTool`; `Run` delegates. Records `tool.<Name>` ⇒ agent for every agent holding the tool, overridden or not (the reach map is the GEPA input).
- Order: after `applyReadOnly`, before `applyDeferredTools` (so the read-only Bash gets the override and `tool_search` lists the new text).

- [ ] **Step 1: Write failing tests** `TestDescribedToolOverridesDeclaration`, `TestDescribedToolDispatchesToInner` (ProcessRequest registers the wrapper under the tool name; Run returns the inner's result), `TestToolOverrideReachesDeferredAndReadOnly` (read-only agent's Bash and a deferred tool both carry the override; `tool_search` description lists it), `TestNonOverridableToolUntouched` (`lsp_hover` in the file ⇒ warning from Task 1, tool unchanged).
- [ ] **Step 2: Run** `go test ./agent -run 'DescribedTool|ToolOverride|NonOverridable' -v` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./agent` — green.
- [ ] **Step 5: Commit** `feat(agent): overridable tool descriptions`.

### Task 6: `GET /api/prompts`, docs

**Files:**
- Create: `server/prompts.go`, `server/prompts_test.go`
- Modify: `server/server.go` (register on the auth group), `CLAUDE.md` (new section "Prompt overrides (`prompt_overrides.json`)" + row in the Configuration files table + reach/`GET /api/prompts` notes), `internal/features/FEATURES.md` is **not** touched (not user-facing)

**Interfaces:**
- Consumes: `Instance.PromptReach`, `RuntimeSettings.PromptOverrides`, a `PromptCatalogue(runtime) []PromptEntry` in `agent` (`{ID, Default string}` for every fixed id plus `tool.<Name>` for tools present in the current generation, default = the inner tool's description).
- Produces: `GET /api/prompts` → `{"prompts": [{"id", "default", "override"?, "agents": [...]}]}`, sorted by id; `override` omitted when none.

- [ ] **Step 1: Write failing test** `TestPromptsRouteListsDefaultsOverridesAndReach` (drives the real router: an override for `block.choice`; the entry has `override`, `default` equal to the golden, `agents` contains the default squad root; a `tool.Bash` entry lists the agents holding Bash; no API key or env value appears in the body).
- [ ] **Step 2: Run** `go test ./server -run TestPromptsRoute -v` — FAIL.
- [ ] **Step 3: Implement** the route and the CLAUDE.md section.
- [ ] **Step 4: Run** `make test` — all green.
- [ ] **Step 5: Live check:** start a server from this checkout with a user-layer `prompt_overrides.json` setting `block.choice` to a marker text, `curl` `/api/prompts`, send one message to the System squad with `-d` logging and confirm the marker is in the root's system instruction; delete the file, `POST /api/config/reload`, confirm the default is back. Expected: both confirmations.
- [ ] **Step 6: Commit** `feat(server): GET /api/prompts; document prompt overrides`.

### Task 7: omnis-benches — enable `prompt:<id>` components

**Files (omnis-benches, branch `feat/gepa-optimizer`):**
- Modify: `gepa/gepa_omnis/components.py`, `gepa/gepa_omnis/worker.py`, `gepa/optimize.py`, `gepa/README.md`
- Test: `gepa/test_components.py`, `gepa/test_worker.py`

**Interfaces:**
- `components.read_seed(omnis_src, names, prompts=None)` — `prompt:<id>` from `prompts` (the `/api/prompts` catalogue: override if any else default).
- `components.apply_candidate` writes `prompt:<id>` components into `home/prompt_overrides.json` (`{"prompts": {...}}`, reset with the rest).
- `components.agents_reached(name, omnis_src, prompts=None)` — `prompt:<id>` ⇒ that entry's `agents`.
- `Worker.prompts() -> list[dict]` (GET `/api/prompts`); `Worker.apply` also checks each `prompt:<id>` candidate equals the served `override`.
- `optimize.prepare` starts one worker to read the catalogue when a `prompt:` component is configured.

- [ ] **Step 1: Write failing tests** `test_seed_reads_prompt_from_catalogue`, `test_apply_writes_prompt_overrides`, `test_reach_prompt_from_catalogue`, `test_apply_raises_when_prompt_override_not_served` (stub serves `/api/prompts`).
- [ ] **Step 2: Run** `.venv/bin/python -m pytest gepa -q` — new tests FAIL.
- [ ] **Step 3: Implement**; drop the `NotImplementedError` paths.
- [ ] **Step 4: Run** — all green.
- [ ] **Step 5: Commit** `gepa: prompt:<id> components through omnis prompt_overrides`.
