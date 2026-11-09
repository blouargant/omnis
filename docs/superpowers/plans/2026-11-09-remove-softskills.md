# Remove soft-skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove soft-skills and everything that exists only to produce or serve them (curator, reflector, precedents, load recorder, idle curator, `/learn`, `/learn-now`), while every config that still names them keeps loading.

**Architecture:** Pure removal, driven by the compiler, in an order where the tree builds and tests pass after every task: first the config layer learns to ignore the removed names (so the curator/reflector can no longer resolve), then the server stops using agent-side symbols, then the agent/core/internal code and the CLI/TUI are deleted, then shipped config + packaging, then the web UI, then docs.

**Tech Stack:** Go 1.26 (ADK v2), vanilla JS web UI, node tests (`make web-test`), i18n JSON + `make i18n`.

**Spec:** `docs/superpowers/specs/2026-11-09-collection-memory-design.md` — sections "Migration" (soft-skill bullets) and "Removal", step 3 of "Order of work". The collection memory (step 1) is already merged; the distiller is already gone.

## Global Constraints

- Configs that still name removed things must keep loading: a `softskills` entry in an agent's `tools`, `curator` / `reflector` in the `agents` list or a squad, `softskills_dir`, the `OMNIS_CURATOR_*` variables — all ignored with **at most one log line**, never an error.
- Soft-skills on disk (`$OMNIS_HOME/softskills`) are left where they are and no longer read. Not migrated, not deleted.
- KEEP: the idle indexer (`server/idle_indexer.go`) and `EventSessionIndexNow` (session search uses them); `EventSubAgentStart/End` and `agent/subagent_event.go` (lifecycle `SubagentStop` hooks use them); the compression State Log (`agent_statelog_*`, written by `internal/compress` — it is part of compression); the `AskUserQuestion` `subtle` flag (generic, merely first used by wrap-session).
- No new dependency. Code, comments, docs in English. Do not touch `../omnis-benches`. Do not merge or push.
- Every task ends with `go build ./... && go vet ./... && go test ./...` green (Task 5 also `make web-test`).
- Bump the `?v=` query of every script whose content changes in `web/index.html` (app.js, settings.js, tool-summary.js, i18n/locales.js).

## Review Focus

1. A per-user `agents.json` overlay (legacy full file) listing `curator` and `reflector` in `agents` and `curator` in a squad's `members` → resolves; neither agent exists in `RuntimeSettings.Agents`; one log line. (Task 1 test.)
2. A user registry fork `$OMNIS_HOME/registry/agents/curator/agent.json` (partial overlay, e.g. only `model_ref`) left after upgrade → the name is still dropped, never built as a sub-agent nor absorbed into the synthesized default squad. (Task 1 test.)
3. An agent whose `tools` contains `"softskills"`, or whose `subagents` contains `"curator"` → no error, no tool, no edge. (Task 1 + Task 3 tests.)
4. An old `conversation_<id>.json` carrying `"harvested": true` and a `skills` entry of kind `"softskill"` → loads, lists, exports and re-imports without error. (Task 2 test.)
5. `OMNIS_CURATOR_ENABLED=true`, `OMNIS_CURATOR_IDLE_TIMEOUT=30m`, `OMNIS_SOFTSKILLS_DIR=/x`, and the CLI flags `-softskills x` / `-curator-enabled true` → no error, no effect. (Task 1 + Task 3 tests.)

---

### Task 1: Config layer ignores the removed agents

**Files:**
- Modify: `agent/runtime_config.go` (agents-list load ~L1396, `defaultAgents`, `applyCuratorEnabledOverride`, `resolveSquadEntries` curator check ~L920, `synthesizeDefaultSquad`, env/options curator handling ~L1429–1488)
- Modify: `agent/nested_subagents.go` (`validateSubAgentGraph` curator case)
- Modify: `config/agents.json` (drop `curator`, `reflector`)
- Delete: `registry/agents/curator/`, `registry/agents/reflector/`
- Test: `agent/runtime_config_test.go`, `agent/nested_subagents_test.go`, `agent/squads_test.go`

**Interfaces:**
- Produces: `var removedAgentNames = map[string]bool{"curator": true, "reflector": true}` in `agent/runtime_config.go`, and `func dropRemovedAgents(cfg *AgentsConfig) []string` which strips those names from `cfg.Agents`, from every squad's `Members` (and a squad whose `Leader` is one of them gets `Leader` cleared) and returns the dropped occurrences for logging. Agent `subagents` edges to a removed name are dropped in `resolveAgentEntries`/normalisation (same set). After this task `RuntimeSettings` never contains an agent named `curator` or `reflector`, so the existing curator/reflector hooks become dead code (deleted in Task 3).

- [ ] **Step 1: Write the failing tests** in `agent/runtime_config_test.go`:
  - `TestRemovedAgentsInConfigAreIgnored`: an agents.json naming `["leader","investigator","curator","reflector"]` with a squad `{name:"system", leader:"leader", members:["investigator","curator"]}` and no registry dir for curator/reflector → `ResolveRuntimeSettings` returns no error; no agent named `curator`/`reflector`; squad members == `["investigator"]`.
  - `TestRemovedAgentRegistryForkIsIgnored`: same but a registry dir containing `curator/agent.json` = `{"name":"curator","model_ref":"x"}` → no `curator` agent; synthesized default squad (no `squads` block) has no `curator` member.
  - `TestCuratorEnvAndOptionsAreIgnored`: `t.Setenv("OMNIS_CURATOR_ENABLED","true")`, `OMNIS_CURATOR_MIN_TURNS=5`, `OMNIS_CURATOR_IDLE_TIMEOUT=30m` → resolves, no `curator` agent.
  - In `agent/nested_subagents_test.go`: `TestSubAgentsNamingRemovedAgentAreDropped`: an agent with `subagents:["curator","investigator"]` → resolves; its `SubAgents == ["investigator"]` (replaces any existing test that expects the curator edge to be fatal).

- [ ] **Step 2: Run them, confirm they fail** — `go test ./agent -run 'RemovedAgent|CuratorEnv|SubAgentsNamingRemoved'` → FAIL (registry-not-found error / curator present).

- [ ] **Step 3: Implement.** Call `dropRemovedAgents` on the merged `AgentsConfig` before the agents-list registry load; log once per resolution when it dropped anything (`log.Printf("config: ignoring removed agents %v (soft-skills were removed)", dropped)`). Remove `curator` from `defaultAgents`; delete `applyCuratorEnabledOverride` and the code that reads `OMNIS_CURATOR_ENABLED` / `OMNIS_CURATOR_MIN_TURNS` / `OMNIS_CURATOR_MIN_SUB_AGENT_CALLS` / `OMNIS_CURATOR_IDLE_TIMEOUT` and `opts.CuratorEnabled` / `opts.DisableAutoCurate` (leave the `Options` fields for Task 3 to delete). Remove the now-impossible curator checks in `resolveSquadEntries`, `synthesizeDefaultSquad`, `validateSubAgentGraph`. Delete the two registry dirs and the two names from `config/agents.json`. Fix any existing test that asserted curator behaviour.

- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "refactor(config): ignore the removed curator and reflector agents"`

### Task 2: Server stops serving soft-skills

**Files:**
- Delete: `server/idle_curator.go`
- Modify: `server/main.go` (`startIdleCurator` call, `OMNIS_SOFTSKILLS_DIR` option), `server/server.go` (`curateSSETimeout`, `POST /sessions/:id/curate`, `EventCuratorStart/End` broadcast), `server/config.go` (`softskills_dir` in the parsed GET and the PUT allowlist), `server/loaded_skills.go` (`load_softskill` → kind mapping), `server/export_import.go` (comment), `server/gc.go`, `server/idle_indexer.go` (comments only)
- Modify: `internal/sessions/sessions.go` (`SessionMeta.Harvested`, `MarkHarvested`, reset in `Touch`), `internal/sessions/history.go` (`ConversationFile.Harvested`, `SetConversationHarvested`, resets, `LoadPersistedSessions` mapping), `internal/sessions/loaded_skills.go` (`SkillKindSoftskill`)
- Test: `internal/sessions/history_test.go`, `internal/sessions/loaded_skills_test.go`, `server/loaded_skills_test.go`, `server/config_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: no server code references `toolkitagent.RequestCurateSession`, `events.EventCurateNow`, `events.EventCuratorStart/End`, `Instance.CuratorIdleTimeout`, `Options.SoftSkillsDir`, `sessions.MarkHarvested`. Task 3 deletes those symbols.

- [ ] **Step 1: Write the failing test** `TestLegacyConversationWithHarvestedAndSoftskillLoads` in `internal/sessions/history_test.go`: write a raw `conversation_<id>.json` containing `"harvested": true` and `"skills":[{"kind":"softskill","name":"wrap-session","agent":"leader","count":1}]` → `LoadConversationFile` succeeds, the turns and the skill entry are present; `LoadPersistedSessions` registers it. (It passes today; it pins the compat once the field is gone — keep it green through Step 3.)
- [ ] **Step 2: Run** `go test ./internal/sessions -run LegacyConversation` → PASS (baseline).
- [ ] **Step 3: Remove** everything listed under Files. `load_softskill` is no longer recorded (only `load_skill`). An existing session's recorded `softskill` entries stay in the file and are served as they are (no filter). Drop the `softskills_dir` key from `server/config.go` both ways; update the tests that asserted it or the soft-skill kind.
- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./...` → PASS (the Step 1 test included).
- [ ] **Step 5: Commit** — `git commit -m "refactor(server): drop the idle curator, /curate route and the harvested flag"`

### Task 3: Delete the soft-skill pipeline (agent, core, internal, CLI, TUI)

**Files:**
- Delete: `internal/softskills/`, `internal/precedents/`, `agent/curator_hook.go`(+test), `agent/load_recorder.go`(+test), `agent/subagent_hook.go`(+test), `agent/session_signals.go`, `agent/curate_tool.go`, `agent/precedents_hook.go`, `agent/softskills_seed_test.go`, `curate.go`, `reindex_precedents.go`, `examples/s23_softskills/`
- Modify: `agent/agent.go` (`Options.CuratorIdleTimeout/SoftSkillsDir/DisableAutoCurate/CuratorEnabled`, curator description, `softSkillTS` param + `softskills` group in `toolsForAgentConfig`, `RecallProtocolAddendum`, sub-agent catalogue curator filter, `CuratorIdleTimeout` copy), `agent/build_toolsets.go` (soft-skill toolset; drop the `softSkillTS` return), `agent/squad.go` (`curateSessionTool`, `NewFeedbackTool`, `softSkillTS`, comments), `agent/build_subagents.go`, `agent/builtin.go` (curator), `agent/instance.go` (seeding, load-recorder/sub-agent-load/curator hook registration, precedents tool, `CuratorIdleTimeout`), `agent/embedder.go` + `agent/infrastructure.go` (`Precedents()` and its cache), `agent/call_cap.go` (`callCapFreeTools` soft-skill names), `agent/claude_plugins.go` (`omnisUnderscoreTools`: `curate_session`, `list_softskills`, `recall_precedents`, `recall_softskills`, `record_session_feedback`), `agent/routing.go` (`cfg.SoftSkillsDir`), `agent/runtime_config.go` (`SoftSkillsDir` on `AgentsConfig`/`AgentEntry`/`RuntimeAgentConfig`/`RuntimeSettings`, `CuratorMinTurns/MinSubAgentCalls/IdleTimeout`), `agent/event_log.go`, `agent/build_plugins.go`/`agent/subagent_event.go` (comments), `core/events/events.go` (`EventCurateNow`, `EventCuratorStart`, `EventCuratorEnd`, `EventSessionReflected`), `internal/paths/paths.go` (`SoftSkillsDir`), `main.go`, `reindex_docs.go`, `reindex_sessions.go`, `internal/tui/tui.go` (`/learn-now`, `EventCurateNow`), `examples/s18_events/main.go`, `internal/skills/deps_gate.go`, `internal/fsutil/symlink_fs.go`, `internal/semindex/semindex.go` (comments)
- Test: `agent/runtime_config_test.go`, `agent/websearch_provider_test.go`, `agent/call_cap_test.go`, `agent/leaderless_subagents_test.go`, `agent/nested_subagents_test.go`, `agent/squads_test.go`, `internal/paths/paths_test.go`, plus a new `main_flags_test.go` at the repo root if none covers flag parsing.

**Interfaces:**
- Consumes: Task 1 (no curator/reflector ever resolves), Task 2 (server uses none of the deleted symbols).
- Produces: `toolsForAgentConfig(ctx, cfg, runtime, skillTS tool.Toolset, leaderMCPHandles, pool, codeIdx, regIdx, docIdx, sessIdx, attestStore, asLeader, emb)` — the `softSkillTS` parameter is gone; `buildLeaderToolsets` returns `(skillTS tool.Toolset, mcpToolsets, allToolsets []tool.Toolset, mcpHandles []*mcpcfg.Handle)`. The tool key `"softskills"` is an explicit no-op case in `toolsForAgentConfig` (one comment, no log — it is in every user overlay).

- [ ] **Step 1: Write the failing tests:**
  - `TestSoftskillsToolGroupIsIgnored` (`agent/runtime_config_test.go` or a squad build test): an agent with `tools:["fs","softskills"]` builds; its tool names contain none of `list_softskills`, `load_softskill`, `recall_softskills`.
  - `TestLeaderHasNoSessionLifecycleTools`: a built coordinating leader has neither `curate_session` nor `record_session_feedback`.
  - `TestRemovedFlagsAreAccepted` (repo root, `package main`): parsing `-softskills /x -curator-enabled true` with the CLI flag set returns no error (the flags stay registered as hidden no-ops).
- [ ] **Step 2: Run** them → FAIL (tools present).
- [ ] **Step 3: Delete and rewire** everything under Files; let `go build ./...` drive the list. In `main.go` keep `-softskills` and `-curator-enabled` registered but ignored (usage text says "deprecated, ignored"); remove the `curate` and `reindex-precedents` subcommands (an unknown subcommand is then treated as a prompt, which is acceptable — note it in the commit body). Keep `EventSessionIndexNow`, `EventSessionEnd`, `EventSubAgentStart/End`. Update or delete tests that only exercised removed code.
- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./...` → PASS; `grep -rniE 'softskill|curator|reflector|precedent' --include='*.go' .` shows only the compat shims, the State Log comments that remain true, and test names.
- [ ] **Step 5: Commit** — `git commit -m "refactor: remove soft-skills, the curator, the reflector and precedents"`

### Task 4: Shipped config, instructions and packaging

**Files:**
- Modify: every `registry/agents/*/agent.json` (drop `"softskills"` from `tools` and the `"softskills_dir": ""` key), `registry/agents/{leader,knowledge_leader,k8s_leader}/instruction.md` (the "Session wrap-up … load_softskill wrap-session" line), `registry/agents/coder/instruction.md` ("Skills / soft-skills" → "Skills"), `registry/skills/ask-user/SKILL.md` ("skill or softskill" → "skill")
- Modify: `config/permissions.json` (the two `softskills/` deny regexes, allow entries `list_softskills`, `load_softskill`, `load_softskill_resource`, `curate_session`, `record_session_feedback`, `recall_softskills`, `recall_precedents`), `core/permissions/permissions_test.go` (`TestShippedConfigParity`)
- Delete: top-level `softskills/`, `packaging/softskills_assets_test.go`, `scripts/run_softskills_e2e.sh`
- Modify: `.goreleaser.yaml` (archive `softskills` src, nfpms `/etc/omnis/softskills` and `/var/lib/omnis/softskills`, Homebrew `pkgshare.install "softskills"`), `.github/workflows/release.yml` (MSI copy), `scripts/build_wheels.py` (sysconf copy), `packaging/README.md`, `packaging/container_test.go` (only if it asserts soft-skill paths)
- Test: `agent` tests that load the shipped registry (e.g. `TestShippedReadOnlyClaimsAreEnforced`, `TestOnlyTheValidatorCanAttest`)

**Interfaces:**
- Consumes: Task 3 (the `softskills` group is already a no-op, so this is cleanup, not a behaviour change).

- [ ] **Step 1: Write the failing test** `TestShippedAgentsDeclareNoSoftskills` (`agent/`, reading `../registry/agents/*/agent.json` like `shippedAgentNames`): no shipped agent has `"softskills"` in `tools` or a `softskills_dir` key; no shipped `instruction.md` contains `softskill`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Edit** the files above. `make package-check` if goreleaser is installed (skip otherwise, say so in the commit body); `python3 -m py_compile scripts/build_wheels.py`.
- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./...` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "chore: drop soft-skills from shipped agents, permissions and packaging"`

### Task 5: Web UI

**Files:**
- Modify: `web/app.js` (`isSkillLoadTool` regex → skills only; skills-dock `softskill` label at ~L2274 and ~L13507; `list_softskills` XML parse / `CURATOR_META_SOFTSKILL` / `countSoftskillsExcludingCurator` / `stripCuratorSoftskill` and their use ~L6387–6417; curator block `appendCuratorBlock`/`resolveCuratorBlock`; `/learn` and `/learn-now` in `BUILTIN_SLASH_COMMANDS`, `COMMON_ORDER`, `handleSlashCommand`, help text ~L11738; the soft-skill note at ~L5342), `web/settings.js` (`TOOL_GROUPS` `softskills` + its desc/icon/display entries, env editor `softskills_dir` section and the `["softskills_dir","softskills_dir"]` map entry, curator filters at ~L2085/2196/2199/4353/4456/6417, recall comment ~L3196), `web/tool-summary.js` (`load_softskill`), `web/css/features/composer.css` (`.skills-dock-kind.kind-softskill`), `web/i18n/{en,fr,es,de}.json` (keys left unused: at least `tool.desc.softskills`, `tool.display.softskills`, `set.env.softskillsDesc`, `skillsDock.softskill`, `app.curation.*`, and any `/learn` strings — find them with `grep -n` per removed call site), `web/i18n/locales.js` (regenerated), `web/index.html` (`?v=` bumps), `internal/usercommands/usercommands.go` (`learn-now` and `learn` in `ReservedNames`)
- Test: `web/tests/` (add one), `internal/usercommands` tests if they list reserved names

**Interfaces:**
- Consumes: Task 2 (`/api/sessions/:id/curate` is gone).

- [ ] **Step 1: Write the failing test** `web/tests/no-softskills.test.mjs` (node, same style as the existing tests): reads `web/app.js`, `web/settings.js`, `web/tool-summary.js` as text and asserts none contains `softskill`, `/learn-now`, `"/learn"`, `/curate`, `Curator`; and that `web/i18n/en.json` has no key containing `softskill` or `curation`.
- [ ] **Step 2: Run** `make web-test` → FAIL.
- [ ] **Step 3: Remove** the items above; then run `make i18n` and check it reports no missing keys. Re-read the slash menu and `/help` builder (`buildHelpBody`) to confirm the Skills section still renders or is skipped when empty.
- [ ] **Step 4: Run** `make web-test && go test ./internal/usercommands/... && node --check web/app.js web/settings.js web/tool-summary.js` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "refactor(web): remove soft-skill, curator and /learn UI"`

### Task 6: Documentation

**Files:**
- Modify: `web/docs/20-learning-and-recall.md` (keep only collection memory; drop soft-skills, curator, reflector, precedents, `/learn`, `/learn-now`, wrap-session), and the soft-skill mentions in `web/docs/{00-about,02-composer,03-sessions,04-settings-panel,10-architecture,11-skills,14-config,15-providers,16-env-vars,18-commands,19-agents}.md`
- Modify: `docs/{architecture,configuration,context-management,examples-catalog,extending,notebooks,semantic-recall,skills}.md`, `README.md`, `AGENT05.md`, `examples/index.ipynb`, `examples/s18_events/s18_events.ipynb`, `examples/s21_skills/s21_skills.ipynb`, `examples/s22_subagents/s22_subagents.ipynb` (prose cells only)
- Delete: `ace-plan.md`, `softskills_design_plan.md` (design notes for the removed system)
- Leave untouched: `docs/superpowers/**`, `docs/architecture-review-2026-07.md` (historical records)
- Modify: `CLAUDE.md` — remove the soft-skill/curator/reflector/precedents material: agent topology lines (`reflector`, `curator`), "Soft-skill reflection pipeline", "Sub-agent reflection pipeline (Phase 6)", `run_id` paragraph's soft-skill purpose, `softskills/` and `precedents` rows in Key packages, `recall_softskills` and `recall_precedents` items in "Semantic recall" (renumber; idle-indexer text stays for session search), "Skill discovery reads the in-prompt catalogue" soft-skill half, "Built-in soft-skills (seeded, not read in place)", `softskills/` in Configuration files and Filesystem layout (`_stats.json`, `.builtin_seeded.json`, `wrap-session`, `agent_feedback_*`), `OMNIS_CURATOR_*` and `OMNIS_SOFTSKILLS_DIR` env rows, `curate`/`reindex-precedents` in Commands, the `curate_session`/`record_session_feedback` allow-list paragraph, the `Harvested` flag mentions, `/learn-now` in the slash menu section, the curator exclusions in squad rules / fan-out / Settings notes; add one short "Soft-skills removed (2026-11-09)" note under "Collection memory" listing the compatibility shims.
- Modify: `internal/features/FEATURES.md` — under the `2.2 (in development)` section add one bullet: soft-skills, the curator and `/learn` / `/learn-now` are removed; collection memory replaces them.

- [ ] **Step 1: Edit** the files. For each, `grep -niE 'soft.?skill|curator|reflector|precedent|learn-now|harvest'` must come back empty except intentional history notes.
- [ ] **Step 2: Run** `go test ./internal/features/...` (FEATURES.md parse guard) → PASS; repeat the grep over `web/docs docs/*.md README.md CLAUDE.md` and inspect the remaining hits.
- [ ] **Step 3: Commit** — `git commit -m "docs: remove soft-skills, curator and precedents"`

### Task 7: Whole-branch verification

- [ ] **Step 1:** `go build ./... && go vet ./... && go test ./... && make web-test` → all green.
- [ ] **Step 2:** Boot smoke: with a temp `OMNIS_HOME` holding a legacy `agents.json` (curator + reflector + `softskills_dir`, a squad member `curator`) and an agent overlay with `tools:["softskills"]`, run `env -u OMNIS_CONFIG_PATH OMNIS_SYSTEM_CONFIG_DIR=$(pwd)/config OMNIS_HOME=<tmp> go run . version`-style resolution (or a small `go run` that calls `ResolveRuntimeSettings`) → no error, one log line. If a server is started for this, stop only its own pid.
- [ ] **Step 3:** Final whole-branch review (`superpowers:requesting-code-review`), fix findings, commit.
