# Claude Code plugins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** omnis reads every Claude Code plugin installed under `/etc/agentskills/claude/` (first one: iapcli's `iaparc`) and runs it as a squad, with its skills and its `PreToolUse` guard, under Claude Code's permission semantics.

**Architecture:** A new discovery package reads plugins in place. The `agent` package converts them into synthesized agents and a squad during `ResolveRuntimeSettings`, so they are live after every reload and visible in Settings without ever being written to the user's config. Plugin hooks join the process-wide hooks engine as tagged layers. The engine adapts its input to Claude Code's shape for them. A per-call "clearance" passes a hook's `allow` (or an approved `ask`) to the permission gate, which honours it only over the mode's **default** ask.

**Tech Stack:** Go ≥ 1.26.6, `google.golang.org/adk/v2` v2.4.0, gin, vanilla JS web UI (no build step), Python-free tests. No new dependencies.

**Spec:** [docs/superpowers/specs/2026-10-28-claude-code-plugins-design.md](../specs/2026-10-28-claude-code-plugins-design.md). Read it before any task; section numbers below (§N) refer to it.

## Global Constraints

- Plugin root = `<agent-skills dir>/claude/` (`paths.agentSkillsDir()`); `OMNIS_AGENTSKILLS_DIR=""` disables it. omnis never writes there.
- A malformed plugin/agent/hook is skipped with a `log.Printf` line; config resolution never fails because of a plugin.
- Omnis agent name = `<plugin>_<agent>`, lower-cased, `-` → `_` (`iaparc_prod_support`). Qualified Claude name (for `agent_type`) = `<plugin>:<agent>` as written in the file (`iaparc:prod-support`).
- Default model map: `{"haiku": "hosted", "sonnet": "high", "opus": "premium"}`; `models.json` key `claude_model_map` overrides per alias.
- `agents.json` key `disabled_plugins: ["<plugin>"]` disables a plugin (agents, squad, skills, hooks).
- Plugin hooks: Claude Code semantics, no `fail_closed`, `CLAUDE_PLUGIN_ROOT` expanded and exported. Hooks never receive `WithShellEnv` values (the user's token).
- Hook `allow` / approved hook `ask` skip **only** the permission layer's mode-default ask (no rule matched, mode not `plan`). A matched `ask` rule still prompts; a `deny` still blocks; never cached, never persisted.
- No-op contract: no `<agent-skills dir>/claude/` ⇒ behaviour identical to today, except the hook/permission interplay (Task 5–6), which applies to all hooks.
- User-facing strings: i18n keys in `web/i18n/{en,fr,es,de}.json`, regenerate with `make i18n`; tool names, `Squad`, `MCP` stay untranslated. Docs in English.
- Run `go test ./...` and `make web-test` green before each commit that touches them; `go vet ./...` clean.

## Review Focus

1. **A plugin whose `source` escapes the root** (`"../../etc"`, absolute path) or whose `marketplace.json` is missing/malformed → skipped with a warning, nothing loaded from outside `<root>`. Test in Task 1.
2. **Two agents that map to the same omnis name** (`foo-bar.md` and `foo_bar.md`, or a plugin agent colliding with an `agents.json` agent) → the first/omnis one wins, the other is skipped with a log line, resolution still succeeds. Test in Task 3.
3. **The guard binary is gone but the plugin dir remains** (iapcli uninstalled, hook command exits 127) → the call proceeds to the permission layer with **no** clearance, so the default ask still shows. Test in Task 6.
4. **A `MultiEdit` with an empty `files` list or entries lacking `file_path`** → plugin hooks receive the original input unchanged (a Claude Code guard then refuses it), never zero hook runs. Test in Task 4.
5. **Two sessions whose provider reuses the same call ID** (`call_0`) → a clearance granted in one session is not taken by the other. Test in Task 5.

---

### Task 1: Discover plugins on disk

**Files:**
- Create: `internal/claudeplugins/discover.go`
- Create: `internal/claudeplugins/discover_test.go`
- Create: `internal/claudeplugins/testdata/agentskills/claude/.claude-plugin/marketplace.json`
- Create: `internal/claudeplugins/testdata/agentskills/claude/demo/.claude-plugin/plugin.json`
- Create: `internal/claudeplugins/testdata/agentskills/claude/demo/agents/{lead,worker,reader}.md`
- Create: `internal/claudeplugins/testdata/agentskills/claude/demo/skills/demo-skill/SKILL.md`
- Create: `internal/claudeplugins/testdata/agentskills/claude/demo/hooks/{hooks.json,record.sh}`
- Modify: `internal/claudeformat/parse.go` (add `ModelAlias`)
- Modify: `internal/paths/paths.go` (add `ClaudePluginsDir`, `SetPluginSkillDirs`)

**Interfaces:**
- Produces:
  - `claudeformat.AgentDef.ModelAlias string`: the raw frontmatter `model` value, trimmed (`"haiku"`); `Model` keeps its current normalized value.
  - `paths.ClaudePluginsDir() string`: `filepath.Join(agentSkillsDir(), "claude")`, or `""` when `agentSkillsDir()` is `""`.
  - `paths.SetPluginSkillDirs(dirs []string)`: process-wide. `SkillsAllSearchDirs()` appends these after `<agent-skills dir>/skills` (lowest precedence). Guard it with a mutex.
  - `claudeplugins.Agent{Name, Description, ModelAlias, Body string; Tools, Skills []string}`
  - `claudeplugins.Plugin{Name, Version, Description, Dir string; Agents []Agent; SkillsDir string; SkillNames []string; HooksPath string}`. `SkillsDir`/`HooksPath` are `""` when absent; `Agents` and `SkillNames` are sorted by name.
  - `claudeplugins.Discover(root string) (plugins []Plugin, warnings []string)`: reads `<root>/.claude-plugin/marketplace.json`, then each `plugins[]` entry whose `source` is a string relative path resolving **inside** `root`. Plugin name = `plugin.json` `name`, falling back to the marketplace entry's `name`; it must match `^[a-z0-9][a-z0-9-]{0,63}$`. Agents come from `agents/*.md` via `claudeformat.ParseMarkdown`, skills from `skills/<n>/SKILL.md`, and `hooks/hooks.json` when present. Anything unreadable becomes a warning and is skipped. A missing `root` returns `nil, nil`.

The fixture is shared by Tasks 3, 6 and 7 as `OMNIS_AGENTSKILLS_DIR=<repo>/internal/claudeplugins/testdata/agentskills`:
- `marketplace.json` lists `demo` (`"./demo"`), `escape` (`"../outside"`) and `remote` (`{"source":"github","repo":"x/y"}`).
- `lead.md`: tools `[Bash, Read, Agent, AskUserQuestion, SendMessage]`, model `sonnet`, body mentioning `demo:worker`, `demo:reader`, `other:automation` and `${CLAUDE_PLUGIN_ROOT}/skills/demo-skill/`.
- `worker.md`: tools `Bash, Read, Write, Edit, MultiEdit, NotebookEdit`, model `haiku`.
- `reader.md`: no `tools`, model `opus`, `skills: [demo-skill]`.
- `hooks.json`: `PreToolUse` matcher `Bash|Read|MultiEdit` → `${CLAUDE_PLUGIN_ROOT}/hooks/record.sh`, timeout 10.
- `record.sh` (mode 0755): appends its stdin, plus `CLAUDE_PLUGIN_ROOT=$CLAUDE_PLUGIN_ROOT`, `IAPCLI_TOKEN=${IAPCLI_TOKEN:-unset}` and `---`, to `$HOOK_RECORD`; answers `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"$HOOK_DECISION"}}` when `HOOK_DECISION` is set; exits 0.

- [ ] **Step 1: Write the failing tests**

```go
func TestDiscoverReadsTheDemoPlugin(t *testing.T) {
	ps, warns := Discover("testdata/agentskills/claude")
	// only "demo": "escape" leaves the root, "remote" is not a local path
	if len(ps) != 1 || ps[0].Name != "demo" { t.Fatalf("plugins = %+v", ps) }
	if len(warns) != 2 { t.Fatalf("want 2 warnings (escape, remote), got %v", warns) }
	p := ps[0]
	// agents sorted: lead, reader, worker
	// p.Agents[0].Tools == [Bash Read Agent AskUserQuestion SendMessage]; ModelAlias "sonnet"
	// p.SkillNames == [demo-skill]; p.SkillsDir == filepath.Join(p.Dir, "skills")
	// p.HooksPath == filepath.Join(p.Dir, "hooks", "hooks.json"); filepath.IsAbs(p.Dir)
}
func TestDiscoverMissingRootIsEmpty(t *testing.T)           // Discover(t.TempDir()+"/nope") → nil, nil
func TestDiscoverSkipsMalformedAgent(t *testing.T)          // copy fixture to TempDir, add agents/bad.md without frontmatter → demo still loads 3 agents, 1 extra warning naming bad.md
func TestDiscoverMalformedMarketplace(t *testing.T)         // marketplace.json = "{" → nil plugins, 1 warning
func TestParseMarkdownKeepsModelAlias(t *testing.T)         // in internal/claudeformat: "model: haiku" → ModelAlias "haiku", Model "claude-haiku-4-5-20251001"
func TestClaudePluginsDirFollowsAgentSkillsDir(t *testing.T) // in internal/paths: env "/x" → "/x/claude"; env "" → ""
func TestSkillsAllSearchDirsAppendsPluginSkillDirsLast(t *testing.T)
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/claudeplugins ./internal/claudeformat ./internal/paths -run 'Discover|ModelAlias|ClaudePluginsDir|PluginSkillDirs' -v`
Expected: FAIL (undefined: `Discover`, `ModelAlias`, `ClaudePluginsDir`, `SetPluginSkillDirs`).

- [ ] **Step 3: Implement `ModelAlias`, the two `paths` functions, and `Discover`**

Containment check: `filepath.Rel(root, resolved)` must not start with `..` and the source must not be absolute.

- [ ] **Step 4: Run the tests again**

Run: same command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/claudeplugins internal/claudeformat/parse.go internal/claudeformat/*_test.go internal/paths
git commit -m "feat(plugins): discover Claude Code plugins under the agent-skills root"
```

### Task 2: Convert a plugin into omnis agents and a squad

**Files:**
- Create: `agent/claude_plugins.go`
- Create: `agent/claude_plugins_test.go`

**Interfaces:**
- Consumes: `claudeplugins.Plugin`, `claudeplugins.Agent` (Task 1).
- Produces (all in package `agent`):
  - `var defaultClaudeModelMap = map[string]string{"haiku": "hosted", "sonnet": "high", "opus": "premium"}`
  - `func pluginAgentName(plugin, agent string) string`: `"iaparc","prod-support"` → `"iaparc_prod_support"`.
  - `type pluginAgent struct { Plugin string; Type string /* "iaparc:prod-support" */; Entry AgentEntry }`
  - `func convertPlugin(p claudeplugins.Plugin, modelMap map[string]string, catalog map[string]RuntimeModelConfig) ([]pluginAgent, *SquadEntry)`
  - `func resolveClaudeModelRef(alias string, modelMap map[string]string, catalog map[string]RuntimeModelConfig) string`: §5 steps 1–4. The ref must exist in `catalog`, else `""` with a log line.
  - `func mapClaudeTools(tools []string) []string`: keep `Bash, Read, Grep, Glob, Write, Edit, MultiEdit`; drop `Agent, AskUserQuestion, SendMessage` and unknown names; always add `Skill`; a nil/empty input yields `[Bash Read Grep Glob Write Edit MultiEdit Skill]`. Order: input order, `Skill` last.
  - `func pluginTranslationNote(p claudeplugins.Plugin) string`

Conversion rules (§2, §3, §4):
- `Entry.Name` = `pluginAgentName`; `Description` is verbatim; `Enabled` = true; `Leader` = true only for the leader; `BuiltIn` = false; `ModelRef` = `resolveClaudeModelRef(a.ModelAlias, …)`; `Skills` = `a.Skills` when non-empty, else `p.SkillNames`; `Tools` = `mapClaudeTools(a.Tools)`.
- `Instruction` = `a.Body` with every `${CLAUDE_PLUGIN_ROOT}` replaced by `p.Dir`, then `"\n\n"` + `pluginTranslationNote(p)`.
- Leader = the agent whose raw `Tools` contain `Agent`. If several, the one named `lead`, else the first by name; if none, no squad (`nil`).
- Two plugin agents mapping to the same omnis name: keep the first by file name and log the other.
- Squad = `SquadEntry{Name: p.Name, Description: <leader Entry.Description>, Leader: <leader omnis name>, Members: <other omnis names, sorted>}`.
- The note is in English, under a `## Running inside omnis` heading, and contains:
  - one line per sibling, e.g. `` `demo:worker` is the tool `demo_worker` ``;
  - "calling an agent with the Agent tool means calling its tool";
  - "SendMessage to an agent means calling the same agent again with `resume_session` set to the `session` handle its previous call returned";
  - "agents of other plugins (such as `other:automation`) are not available here: tell the user instead of calling them". Foreign names come from the `<x>:<y>` mentions in all bodies whose `x` ≠ `p.Name`, sorted and de-duplicated.

- [ ] **Step 1: Write the failing tests** (fixture via `claudeplugins.Discover("../internal/claudeplugins/testdata/agentskills/claude")`)

```go
func TestPluginAgentName(t *testing.T)            // ("iaparc","prod-support") → "iaparc_prod_support"; ("Demo","Lead") → "demo_lead"
func TestMapClaudeTools(t *testing.T)             // worker tools → [Bash Read Write Edit MultiEdit Skill]; nil → default 8; lead → [Bash Read Skill]
func TestResolveClaudeModelRef(t *testing.T) {
	cat := map[string]RuntimeModelConfig{"hosted": {}, "high": {}, "premium": {}}
	// "haiku"→"hosted"; "inherit"→""; ""→""; "high"→"high" (a catalogue ref used as is)
	// "gpt-9"→""; map{"haiku":"nope"} → "" (ref missing from catalogue)
}
func TestConvertDemoPlugin(t *testing.T) {
	// 3 agents; squad Name "demo", Leader "demo_lead", Members [demo_reader demo_worker]
	// squad Description == lead Entry.Description
	// demo_lead Leader true, ModelRef "high"; demo_worker ModelRef "hosted"; demo_reader ModelRef "premium", Skills [demo-skill]
	// demo_worker Skills == [demo-skill] (all plugin skills); Type "demo:worker"
	// lead Instruction: contains p.Dir+"/skills/demo-skill/", has no "${CLAUDE_PLUGIN_ROOT}",
	//   contains "`demo:worker` is the tool `demo_worker`", "resume_session", "other:automation"
}
func TestConvertPluginWithoutAgentToolHasNoSquad(t *testing.T)
func TestConvertPluginTwoAgentHoldersPrefersLead(t *testing.T)
func TestConvertPluginDuplicateNamesKeepsFirst(t *testing.T)  // agents "foo-bar" and "foo_bar" → one "demo_foo_bar" (the first by name), no panic (Review Focus 2)
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./agent -run 'PluginAgentName|MapClaudeTools|ResolveClaudeModelRef|ConvertDemoPlugin|ConvertPlugin' -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement the functions in `agent/claude_plugins.go`**
- [ ] **Step 4: Run the tests again.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(plugins): convert Claude Code plugin agents into omnis agents and a squad`

### Task 3: Plugins in the resolved configuration

**Files:**
- Modify: `agent/runtime_config.go` (`runtimeConfigFile.DisabledPlugins`, `modelsConfigFile.ClaudeModelMap`, `RuntimeAgentConfig.Plugin/PluginType`, `RuntimeSquadConfig.Plugin`, `RuntimeSettings.PluginHookLayers`, the `ResolveRuntimeSettings` integration, `loadPluginAgentEntry`)
- Modify: `agent/claude_plugins.go` (catalogue cache and accessors)
- Modify: `agent/instructions.go` (plugin-aware instruction readers)
- Modify: `internal/configedit/overlay.go` (`SetAgentBaseProvider`)
- Test: `agent/claude_plugins_resolve_test.go`, `internal/configedit/overlay_test.go`

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces:
  - `runtimeConfigFile.DisabledPlugins []string \`json:"disabled_plugins,omitempty"\``; `modelsConfigFile.ClaudeModelMap map[string]string \`json:"claude_model_map,omitempty"\``. The effective map is `defaultClaudeModelMap` overlaid by the file's map (lower-cased keys).
  - `RuntimeAgentConfig.Plugin string` (`"demo"`), `RuntimeAgentConfig.PluginType string` (`"demo:worker"`), `RuntimeSquadConfig.Plugin string`.
  - `RuntimeSettings.PluginHookLayers []hooks.PluginLayer`: one per **enabled** plugin with a `HooksPath` (type defined in Task 4; define it there first if Task 4 is not done yet, signature `hooks.PluginLayer{Name, Root, Path string}`).
  - `type PluginInfo struct { Name, Version, Description, Squad string; Enabled bool; Agents []string }`.
  - `func ClaudePlugins() []PluginInfo`: every discovered plugin, disabled ones included, sorted by name.
  - `func IsPluginAgent(name string) bool`: true for an agent of any discovered plugin, enabled or disabled.
  - `func PluginAgentType(name string) string`: qualified name for an **enabled** plugin agent, else `""`.
  - `func pluginAgentBase(name string) (AgentEntry, bool)`: the converted entry.
  - `configedit.SetAgentBaseProvider(fn func(name string) map[string]any)`: `agentEntryBaseBelow` prepends `fn(name)` as the lowest layer when it is non-nil. The `agent` package installs it in an `init()` from `pluginAgentBase` (`json.Marshal` → map).
  - The catalogue (`atomic.Pointer[pluginCatalog]`) is replaced at each `ResolveRuntimeSettings`. Each resolution also calls `paths.SetPluginSkillDirs` with the enabled plugins' `SkillsDir`.

Integration in `ResolveRuntimeSettings`, after the model catalogue is normalized and before `resolveAgentEntries`:
1. `Discover(paths.ClaudePluginsDir())` and log each warning.
2. Convert each plugin not listed in `cfg.DisabledPlugins`.
3. Skip a plugin agent whose name is in `cfg.Agents` or was already produced (log).
4. `loadPluginAgentEntry(base, registryDirs)`: the base marshalled as the lowest layer, then every registry `agent.json` for that name (low→high), merged with `configedit.MergeGeneric`. The instruction is the highest registry `instruction.md` for that name, else `base.Instruction`.
5. Append these entries to `entries` (also when `cfg.Agents` is empty: the plugin entries are added to whatever the agents list resolves to).
6. Set `Plugin`/`PluginType` on the resulting `RuntimeAgentConfig`s.
7. Merge each plugin `SquadEntry` into `cfg.Squads` before `resolveSquadEntries`. A same-named authored squad keeps its non-empty fields; its empty `Leader`/`Members`/`Description` are filled from the plugin squad. Otherwise the plugin squad is appended. Then set `RuntimeSquadConfig.Plugin`.

`ReadAgentInstruction(name)` and `ReadAgentInstructionBelowLayer(name, layer)`: when no registry `instruction.md` exists for a plugin agent, return its converted instruction instead of `default.md`.

- [ ] **Step 1: Write the failing tests.** Use `t.Setenv("OMNIS_AGENTSKILLS_DIR", "../internal/claudeplugins/testdata/agentskills")`, `t.Setenv("OMNIS_HOME", t.TempDir())`, `t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", <temp dir with agents.json listing "leader" and models.json with hosted/high/premium>)`, and `t.Setenv("OMNIS_CONFIG_PATH", "")` unset via `os.Unsetenv` (it bypasses the merge; see the memory note in CLAUDE.md).

```go
func TestResolveIncludesPluginAgentsAndSquad(t *testing.T) // demo_lead/demo_worker/demo_reader present, Plugin "demo"; squad "demo" Leader demo_lead, Plugin "demo"; ModelRef hosted/high/premium
func TestResolveHonoursDisabledPlugins(t *testing.T)       // agents.json disabled_plugins ["demo"] → no demo_* agent, no squad, no PluginHookLayers; IsPluginAgent("demo_lead") still true; PluginAgentType("demo_lead") == ""
func TestResolveClaudeModelMapOverride(t *testing.T)       // models.json claude_model_map {"haiku":"premium"} → demo_worker ModelRef "premium"
func TestResolveUserOverlayOverPluginAgent(t *testing.T)   // $OMNIS_HOME/registry/agents/demo_worker/agent.json {"model_ref":"high"} → ModelRef "high", Description unchanged; instruction.md overlay wins
func TestResolvePluginNameCollisionKeepsOmnisAgent(t *testing.T) // registry agent "demo_worker" listed in agents.json → that one wins, resolution succeeds (Review Focus 2)
func TestResolveSetsPluginSkillDirsAndHookLayers(t *testing.T)   // SkillsAllSearchDirs() ends with <fixture>/claude/demo/skills; PluginHookLayers == [{demo, <dir>, <dir>/hooks/hooks.json}]
func TestReadAgentInstructionFallsBackToPlugin(t *testing.T)
func TestAgentEntryOverlayAgainstProviderBase(t *testing.T) // in configedit: provider returns {"description":"d","tools":["Bash"]}; desired same → overlay "{}"; desired tools ["Read"] → {"tools":["Read"]}
```

- [ ] **Step 2: Run them to see them fail.** Run: `go test ./agent ./internal/configedit -run 'Resolve.*Plugin|ClaudeModelMap|PluginSkillDirs|FallsBackToPlugin|ProviderBase' -v`. Expected: FAIL.
- [ ] **Step 3: Implement the integration.**
- [ ] **Step 4: Run the tests again, then `go test ./agent/... ./internal/... ./server/...`** (the existing config tests must stay green). Expected: PASS.
- [ ] **Step 5: Commit** `feat(plugins): resolve plugin agents, squads, skills and hook layers`

### Task 4: Plugin hooks in the hooks engine

**Files:**
- Modify: `internal/hooks/hooks.go` (`PluginRef` on `Matcher`, `Match` aliasing)
- Modify: `internal/hooks/run.go` (`Input.AgentType`, plugin input adaptation, env)
- Modify: `internal/hooks/reloader.go` (`PluginLayer`, `SetPluginLayers`)
- Modify: `core/tools/bash.go` (`RunShellCapturedEnv`)
- Test: `internal/hooks/plugin_test.go`, `core/tools/bash_test.go`

**Interfaces:**
- Produces:
  - `hooks.PluginLayer{Name, Root, Path string}`; `hooks.PluginRef{Name, Root string}`; `Matcher.Plugin *PluginRef \`json:"-"\``.
  - `(*Reloader).SetPluginLayers(layers []PluginLayer)`: replaces the plugin layers (compared by value; no-op when unchanged), adds their paths to the mtime polling, and forces a reload. Plugin layer matchers are tagged with their `PluginRef` and merged after the base and overlays.
  - `hooks.Input.AgentType string \`json:"agent_type,omitempty"\``.
  - `fstools.RunShellCapturedEnv(ctx context.Context, command, cwd string, stdin []byte, timeout time.Duration, env []string) CapturedRun`: like `RunShellCaptured`, with `cmd.Env = append(os.Environ(), env...)` when `env` is non-nil. `RunShellCaptured` calls it with `nil`. It must not read `WithShellEnv`.
- Behaviour of `(*Config).Match` and `(*Config).Run` for a matcher with `Plugin != nil`:
  - It matches against `claudeToolName(subject)` (`bash_background`, `monitor` → `Bash`; anything else unchanged).
  - Command: `${CLAUDE_PLUGIN_ROOT}` is replaced by `Plugin.Root`; env `CLAUDE_PLUGIN_ROOT=<Root>`.
  - Input (`claudeInputs(in Input) []Input`):
    - `ToolName` = `claudeToolName`;
    - `bash_background`/`monitor` → `ToolInput = {"command": <command>}`;
    - `MultiEdit` with `files[].file_path` → one input per file, `{"file_path", "edits"}`;
    - `MultiEdit` with no usable file entry → the original input unchanged (Review Focus 4);
    - everything else → as is.
  - Every adapted input runs and aggregates with the existing rules (Block > Ask > Allow > Proceed). Native matchers behave exactly as today.

- [ ] **Step 1: Write the failing tests** (copy the fixture's `record.sh`, run with `HOOK_RECORD` in `t.TempDir()`)

```go
func TestPluginMatcherSeesMultiEditPerFile(t *testing.T)   // MultiEdit {files:[a,b]} → 2 records, each with "file_path" and "edits", tool_name "MultiEdit"
func TestPluginMatcherMultiEditWithoutPathsPassesOriginal(t *testing.T) // files: [] → 1 record with "files"
func TestPluginMatcherAliasesBackgroundShell(t *testing.T) // Match(PreToolUse,"bash_background") non-empty; record has tool_name "Bash", tool_input {"command":"iapcli x"}
func TestNativeMatcherUnchanged(t *testing.T)              // untagged matcher "Bash" does not match "bash_background"; MultiEdit input keeps "files"
func TestPluginCommandGetsPluginRoot(t *testing.T)         // record contains CLAUDE_PLUGIN_ROOT=<root>; command "${CLAUDE_PLUGIN_ROOT}/hooks/record.sh" resolves
func TestPluginHookNeverSeesShellEnv(t *testing.T)         // ctx = fstools.WithShellEnv(ctx, []string{"IAPCLI_TOKEN=secret"}) → record has IAPCLI_TOKEN=unset
func TestAgentTypeIsSerialised(t *testing.T)               // Input{AgentType:"demo:worker"} → stdin JSON has "agent_type":"demo:worker"
func TestSetPluginLayersReloads(t *testing.T)              // empty base, SetPluginLayers([{demo, root, fixture hooks.json}]) → Snapshot().Match(PreToolUse,"Bash") has 1 command; SetPluginLayers(nil) → 0
func TestRunShellCapturedEnv(t *testing.T)                 // env ["FOO=bar"], command `printf %s "$FOO"` → Stdout "bar"
```

- [ ] **Step 2: Run them to see them fail.** Run: `go test ./internal/hooks ./core/tools -run 'Plugin|AgentType|NativeMatcher|RunShellCapturedEnv' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the tests again, then `go test ./internal/hooks/... ./core/tools/... ./packaging/...`** (the k8s hook tests must stay green). Expected: PASS.
- [ ] **Step 5: Commit** `feat(hooks): run Claude Code plugin hooks with Claude-shaped input`

### Task 5: Permission verdicts and per-call clearances

**Files:**
- Modify: `core/permissions/permissions.go`
- Create: `core/permissions/clearance.go`
- Test: `core/permissions/clearance_test.go`, `core/permissions/permissions_test.go`

**Interfaces:**
- Produces:
  - `type Verdict struct { Decision Decision; Reason string; DefaultAsk bool }`.
  - `func (c *Config) CheckArgsVerdict(toolName string, args map[string]any, cwd string) Verdict`: today's logic. `DefaultAsk` is true only on the final mode-default branch (step 8, returning `DecisionAsk`) **and** when the mode is not `plan`. `CheckArgs` returns `(v.Decision, v.Reason)`.
  - `type Clearances struct{ … }`, `func NewClearances() *Clearances`, `func (c *Clearances) Grant(sessionID, callID, toolName string)`, `func (c *Clearances) Take(sessionID, callID, toolName string) bool` (consume once). `Grant` evicts entries older than 10 minutes. Nil-safe: a nil `*Clearances` grants nothing and takes false.
  - `PluginConfig.Clearances *Clearances` and `PluginConfig.DefaultAllow func(tc adk.ToolContext, toolName string) bool` (optional).
  - Gate behaviour:
    - at the top of the callback, `cleared := cfg.Clearances.Take(sid, tc.FunctionCallID(), t.Name())`;
    - then, when the verdict is `DecisionAsk` with `DefaultAsk` and (`cleared` or `cfg.DefaultAllow(tc, t.Name())`), allow without asking and without caching;
    - every other path is unchanged.

- [ ] **Step 1: Write the failing tests**

```go
func TestVerdictDefaultAskOnlyForUnmatchedCalls(t *testing.T) // no rule → DefaultAsk true; Bash(iapcli *) in ask → Decision Ask, DefaultAsk false; deny rule → Deny
func TestVerdictPlanModeNeverDefaultAsk(t *testing.T)         // defaultMode plan, unmatched MCP tool → DefaultAsk false
func TestClearanceTakenOnce(t *testing.T)                     // Grant(s,c,t); Take → true; Take → false
func TestClearanceIsPerSession(t *testing.T)                  // Grant("A","call_0","Bash"); Take("B","call_0","Bash") false (Review Focus 5)
func TestNilClearancesAreInert(t *testing.T)
func TestGateClearanceSkipsDefaultAskOnly(t *testing.T)       // fake asker counting calls: cleared + unmatched → 0 asks; cleared + ask rule → 1 ask; cleared + deny rule → "[DENIED]"
func TestGateDefaultAllowHook(t *testing.T)                   // DefaultAllow returns true for "demo_worker" → unmatched call allowed, 0 asks; a deny rule on Agent(demo_worker) still denies
func TestGateClearanceIsNotCached(t *testing.T)               // cleared call, then same args with another call ID → asks
```

Build `adk.ToolContext` fakes the way the existing gate tests do (look in `core/permissions/*_test.go` for the current helper and reuse it).

- [ ] **Step 2: Run them to see them fail.** Run: `go test ./core/permissions -run 'Verdict|Clearance|GateClearance|GateDefaultAllow' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./core/permissions/...`.** `TestShippedConfigParity` must stay green. Expected: PASS.
- [ ] **Step 5: Commit** `feat(permissions): honour hook allow over the default ask, per call`

### Task 6: Wire plugins, hooks and the gate together

**Files:**
- Modify: `agent/infrastructure.go` (`HookClearances`), `agent/hooks_plugin.go` (`Hooks` sets plugin layers; `hookToolCallbacks` signature), `agent/build_plugins.go` (`buildPermissionGate`), `agent/squad.go` (call sites)
- Test: `agent/claude_plugins_wiring_test.go`

**Interfaces:**
- Consumes: Task 3 (`RuntimeSettings.PluginHookLayers`, `RuntimeAgentConfig.Plugin/PluginType`), Task 4 (`SetPluginLayers`, `Input.AgentType`), Task 5 (`Clearances`, `PluginConfig.DefaultAllow`).
- Produces:
  - `Infrastructure.HookClearances *permissions.Clearances`, built in `BuildInfrastructure`.
  - `(*Infrastructure).Hooks(runtime)` calls `reloader.SetPluginLayers(runtime.PluginHookLayers)` on **every** call, after the memoised construction.
  - `hookToolCallbacks(engine *hooks.Reloader, reg *askuser.Registry, state *hookstate.Store, attestStore *attest.Store, clearances *permissions.Clearances, agentType func(agentName string) string, isRouter bool)`:
    - sets `in.AgentType = agentType(tc.AgentName())`;
    - calls `clearances.Grant(realSessionID(tc), tc.FunctionCallID(), t.Name())` when `out.Decision == hooks.DecisionAllow` or when an `Asks()` escalation is approved;
    - `buildHooksPlugin` takes the same two new parameters.
  - `buildPermissionGate(runtime RuntimeSettings, asker permissions.Asker, bus *events.Bus, clearances *permissions.Clearances)`. It sets `PluginConfig.Clearances` and `PluginConfig.DefaultAllow = samePluginDelegation(runtime)`. It also sets `SessionFunc: realSessionID`. Today's inline func (`agent/build_plugins.go:278`) lacks `realSessionID`'s `events.RootSessionFromContext` fallback, so in an injected turn a sub-agent's clearance would be granted under one id and looked up under another. One resolver on both sides is the fix.
  - `func samePluginDelegation(runtime RuntimeSettings) func(tc adk.ToolContext, toolName string) bool`: true iff both `tc.AgentName()` and `toolName` are agents of the same non-empty `Plugin`.
  - `agentType` comes from `runtime.Agents`' `PluginType` (a map lookup, `""` for native agents).

- [ ] **Step 1: Write the failing tests.** Drive the real callbacks with the fixture plugin (env as in Task 3), `HOOK_RECORD` in a temp dir, a fake `permissions.Asker` that counts calls, and a tool context fake (reuse `agent` test helpers that already build `adk.ToolContext`; grep `_test.go` for one).

```go
func TestPluginHookAllowSkipsDefaultPermissionAsk(t *testing.T) // HOOK_DECISION=allow, Bash call by demo_worker → hook ran, gate asked 0 times
func TestPluginHookAskApprovedSkipsSecondCard(t *testing.T)    // HOOK_DECISION=ask, askuser registry answers "allow" → exactly 1 question total (the hook's), tool proceeds
func TestPluginHookAllowDoesNotBeatAskRule(t *testing.T)       // permissions ask rule Bash(echo *) + HOOK_DECISION=allow → gate asks once
func TestMissingGuardLeavesDefaultAsk(t *testing.T)            // layer whose command is "/nonexistent/guard" → hook non-blocking, gate asks once (Review Focus 3)
func TestHookInputCarriesAgentType(t *testing.T)               // record has "agent_type":"demo:worker" for demo_worker; no agent_type for "coder"
func TestSamePluginDelegationIsDefaultAllowed(t *testing.T)    // tc agent demo_lead, tool demo_worker → true; tool "investigator" → false; tc agent "leader", tool demo_worker → false
func TestDisabledPluginHasNoHooks(t *testing.T)                // disabled_plugins ["demo"] → Hooks(runtime).Snapshot().Match(PreToolUse,"Bash") empty
func TestClearanceMatchesUnderRootSessionOnly(t *testing.T)   // ctx carries events.WithRootSession("real") but no steer tag, tc.SessionID() "ephemeral" → HOOK_DECISION=allow, gate asks 0 times
```

- [ ] **Step 2: Run them to see them fail.** Run: `go test ./agent -run 'PluginHook|MissingGuard|AgentType|SamePluginDelegation|DisabledPluginHasNoHooks' -v`. Expected: FAIL.
- [ ] **Step 3: Implement and update every call site** (`agent/squad.go:239`, `:253-254`, `agent/build_plugins.go:85`, and any test that calls these functions).
- [ ] **Step 4: Run `go build ./... && go test ./agent/... ./core/... ./internal/... ./server/...`.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(plugins): wire plugin hooks, agent_type and hook clearances into the gate`

### Task 7: Server — Settings round trip and the plugins routes

**Files:**
- Modify: `server/config.go` (GET agent map `plugin`; PUT names list and sweep)
- Create: `server/plugins.go`
- Modify: `server/server.go` (register routes in the `auth` group next to `registerConfigRoutes`)
- Test: `server/plugins_test.go`, `server/config_plugin_agents_test.go`

**Interfaces:**
- Consumes: `agent.ClaudePlugins()`, `agent.IsPluginAgent()`, `RuntimeAgentConfig.Plugin` (Task 3).
- Produces:
  - GET `/api/config/parsed/agent`: each plugin agent's map carries `"plugin": "<name>"`. The key is display-only and absent from the PUT allowlist (`cleanAgent`).
  - PUT `/api/config/parsed/agent`: a plugin agent (`agent.IsPluginAgent(name)`) still gets its overlay computed and written (now diffed against the plugin base). Its name is **not** added to the persisted `agents` list but **is** added to the sweep's keep-set. The keep-set also includes `IsPluginAgent` names of disabled plugins, so a disabled plugin's overrides survive a save.
  - `GET /api/plugins` → `{"plugins": [PluginInfo…]}`.
  - `PUT /api/plugins/:name` with `{"enabled": bool}`:
    - 404 for an unknown plugin;
    - otherwise reads `configedit.LoadMergedSection("agents.json")`, adds or removes the name in `disabled_plugins` (sorted, deduplicated; removes the key when the list is empty), and calls `configedit.WriteSection("agent", merged)`;
    - then `manager.Reload` when the manager is non-nil, and returns `{"plugins": …, "reloaded": bool}`.

- [ ] **Step 1: Write the failing tests** (router built the way `server/config_test.go` builds it; env as in Task 3)

```go
func TestParsedAgentGetMarksPluginAgents(t *testing.T)            // demo_worker map has "plugin":"demo"; "leader" has no "plugin"
func TestParsedAgentRoundTripDoesNotForkPluginAgents(t *testing.T) // GET→PUT verbatim → user agents.json "agents" has no demo_*; no $OMNIS_HOME/registry/agents/demo_*; no "squads" entry "demo"
func TestParsedAgentEditForksOnlyTheDelta(t *testing.T)            // change demo_worker model_ref to "high" → registry/agents/demo_worker/agent.json == {"model_ref":"high"}; no instruction.md
func TestSaveKeepsDisabledPluginOverrides(t *testing.T)            // overlay for demo_worker exists, disabled_plugins ["demo"], GET→PUT → overlay still on disk
func TestPluginsRouteListsAndToggles(t *testing.T)                 // GET → demo enabled; PUT {enabled:false} → disabled_plugins ["demo"] in user agents.json, GET → enabled false; PUT {enabled:true} → key gone; PUT /api/plugins/nope → 404
```

- [ ] **Step 2: Run them to see them fail.** Run: `go test ./server -run 'PluginAgents|ForksOnlyTheDelta|DisabledPluginOverrides|PluginsRoute' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./server/...`.** The existing round-trip tests (`config_agent_model_test.go`, `config_agent_lists_test.go`, `config_agent_squads_test.go`) must stay green. Expected: PASS.
- [ ] **Step 5: Commit** `feat(plugins): Settings round trip for plugin agents and /api/plugins`

### Task 8: Web UI — plugin badge and the Plugins toggle

**Files:**
- Modify: `web/settings.js` (agents list grouping, badge, detail panel, Plugins block)
- Modify: `web/css/settings/*.css` (the file that styles `.source-badge`; add `.source-badge-plugin`)
- Modify: `web/i18n/{en,fr,es,de}.json`, regenerate `web/i18n/locales.js` (`make i18n`)
- Modify: `web/index.html` (bump the `?v=` of `settings.js` and `i18n/locales.js`)

**Interfaces:**
- Consumes: the GET `plugin` key and `/api/plugins` (Task 7).
- Behaviour:
  - Plugin agents form their own group in the fleet list per plugin, labelled `tr("set.plugins.groupLabel", {name})` ("Plugin: {name}"), after the custom group and before built-ins.
  - Their list item and detail title carry `<span class="source-badge source-badge-plugin">{plugin}</span>`.
  - Their detail panel hides the delete/remove control. Every other field stays editable (an edit forks the delta).
  - A **Plugins** block at the top of Settings → Agents & squads (one row per `GET /api/plugins` entry: name, version, description, agent count, a toggle) is hidden when the list is empty.
  - The toggle calls `PUT /api/plugins/:name`, then re-fetches the agent section and re-renders. It shows the existing reload/restart banner only if the response says `reloaded: false`.
  - i18n keys: `set.plugins.title`, `set.plugins.groupLabel`, `set.plugins.enabled`, `set.plugins.disabled`, `set.plugins.agentsCount` (plural via `trN`), `set.plugins.toggleFailed`, all four locales.

- [ ] **Step 1: Add the i18n keys and run `make i18n`.** Expected: no missing-key warning for the new keys.
- [ ] **Step 2: Implement the UI changes.**
- [ ] **Step 3: Run `make web-test`.** Expected: PASS.
- [ ] **Step 4: Smoke-test in a browser.** Start with the fixture: `OMNIS_AGENTSKILLS_DIR=$(pwd)/internal/claudeplugins/testdata/agentskills OMNIS_WEB_DIR=$(pwd)/web go run ./server` (no token). Check:
  - the Plugins block lists `demo`;
  - the three agents are grouped and badged;
  - toggling off removes them after reload and toggling on restores them;
  - a no-op Save writes nothing under `~/.omnis/registry/agents/demo_*` (`ls`).
- [ ] **Step 5: Commit** `feat(web): plugin badge and Plugins toggle in Settings`

### Task 9: Documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `internal/features/FEATURES.md` (bullet under `## 2.1 (in development)`)
- Create: `web/docs/23-plugins.md`
- Modify: `web/docs/22-hooks.md`, `web/docs/13-permissions.md` (one paragraph each, linking to 23)

**Required content:**
- **CLAUDE.md**
  - A new section "Claude Code plugins (`/etc/agentskills/claude/`)" covering discovery, conversion rules, the squad, the translation note, `claude_model_map`, `disabled_plugins`, plugin hooks (aliases, MultiEdit split, `CLAUDE_PLUGIN_ROOT`, `agent_type`), clearances, the round-trip rules, and the known limit (lost specialist session).
  - Rewrite the "Before-tool chain order" bullet and the Kubernetes "permission card does NOT (yet) carry the hook's diff" text where they say `DecisionAllow` is dead code: it is now consumed as a clearance over the default ask only.
  - Add `claude_model_map`/`disabled_plugins` to the configuration table, `internal/claudeplugins` to Key packages, and `/etc/agentskills/claude/` to the filesystem layout's registry-only layer.
- **FEATURES.md:** `- **Claude Code plugins** — omnis runs the agents, skills and guard of Claude Code plugins installed under /etc/agentskills/claude (e.g. iapcli's IA Parc squad), and lets you turn each plugin off in Settings.`
- **`web/docs/23-plugins.md`:** a user-facing page covering what gets loaded, the squad, confirmations (lead's question + the guard's card), the model mapping, turning a plugin off, and editing a plugin agent.

- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Run `go test ./internal/features/...`** (FEATURES.md parser guard). Expected: PASS.
- [ ] **Step 3: Commit** `docs: Claude Code plugins`

### Task 10: Live acceptance with the real iaparc plugin

No code, unless a check fails, in which case fix it in the owning task's files with a regression test first. On this machine (iapcli ≥ 0.36 installed, `/etc/agentskills/claude/` present), build and run the server from the branch:

```bash
make build && OMNIS_WEB_DIR=$(pwd)/web bin/omnis-server --no-browser
```

- [ ] **Step 1: Check spec acceptance items 1, 2, 7, 8, 9, 10 in the UI/filesystem.**
  - squad `iaparc` and 11 `iaparc_*` agents with badges, both `iaparc_reviewer` and `reviewer`;
  - the router sends "où en est mon budget LLM Gateway" to `iaparc` (visible in the routing chip);
  - toggle off/on;
  - edit a file in a **copy** of the plugin (`OMNIS_AGENTSKILLS_DIR` pointed at a temp copy) → reload shows it, nothing under `~/.omnis`;
  - no-op Save;
  - a broken `agents/x.md` in the copy is skipped with a log line.
- [ ] **Step 2: Check items 5, 6, 11 against the real guard.** Use a chat in the `iaparc` squad, or Go tests pointed at `/etc/agentskills` with `-run` on the Task 6 tests.
  - `iaparc_prod_support` writing → denied;
  - a Coding `MultiEdit` → not blocked;
  - `MultiEdit` on `~/.kube/config` → blocked;
  - `bash_background` running `iapcli teams delete x` → guard card;
  - a `deny` rule beats a hook `allow`.
- [ ] **Step 3: Items 3–4 (reads with no card; a write = lead question + one guard card, resumed in the same specialist session) need a logged-in IA Parc context.** Run them on the IA Parc test deployment, or with `iapcli login` here. If neither is available, report them as not verified.
- [ ] **Step 4: Report the results.** Commit any fixes made along the way.
