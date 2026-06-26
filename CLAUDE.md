# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Self-Maintenance Rule

After every major change (new agent, new squad, new tool, new skill, new config file, new package under `core/` or `internal/`, new env var, new HTTP route, new SSE event, new MCP wiring, search-chain/precedence changes, hot-reload behavior changes, architectural shifts), update this CLAUDE.md file to reflect the current state. Specifically:

- Add new agents/squads/tools/skills/packages to the relevant tables and sections below (Agent topology, Key packages, Configuration files, Environment variables, Filesystem layout).
- Update the "Adding a new sub-agent", "Adding a new squad", "Adding a skill", and A2A sections when their procedures change.
- Add any new gotchas, precedence rules, or patterns where they belong (e.g. write-layer routing, MCP dedup, session pinning across hot-reload).
- Keep the configuration precedence chain and search chain accurate when either changes.
- Keep this file as the single source of truth for AI sessions working on this project.

## Vendored frontend library upkeep

The web UI vendors two third-party JS libraries **offline** (no runtime CDN), each
pinned to a version in the Makefile and committed under `web/`:

| Library | Pinned var (Makefile) | Vendored into | Re-vendor command |
|---|---|---|---|
| Monaco Editor | `MONACO_VERSION` | `web/monaco/vs/` | `make vendor-monaco` |
| xterm.js + fit addon | `XTERM_VERSION`, `XTERM_FIT_VERSION` | `web/xterm/` | `make vendor-xterm` |

**Periodically check for upstream updates and keep these current — do not let them
lag behind.** At the start of a session that touches the editor or terminal (and
opportunistically otherwise), check the latest published versions and, if newer:

- Monaco: `npm view monaco-editor version` — compare against `MONACO_VERSION`.
- xterm: `npm view xterm version` and `npm view xterm-addon-fit version` — compare
  against `XTERM_VERSION` / `XTERM_FIT_VERSION`. (Note: xterm has since moved to the
  scoped `@xterm/xterm` + `@xterm/addon-fit` packages; when bumping across that
  rename, update the Makefile `vendor-xterm` package names and the global names
  used in [web/app.js](web/app.js) `ensureXterm` — classic builds expose
  `window.Terminal` / `window.FitAddon`.)

To update: bump the version var(s) in the [Makefile](Makefile), run the matching
`make vendor-*` target, smoke-test the editor / terminal in the web UI, and commit
the refreshed `web/monaco` or `web/xterm` files together with the Makefile bump.
Lazy-loading is unaffected (`ensureMonaco` / `ensureXterm` resolve paths at
runtime), so only the vendored files + the pinned version change.

## Web UI internationalisation (i18n)

The web UI is localised into **English (base), French, Spanish, German**. There
is no build step / framework, so the i18n machinery is a tiny synchronous runtime
plus per-locale JSON catalogues, mirroring the existing theme/notifications
preference pattern (localStorage cache + server `preferences.json` +
`GET/PUT /api/preferences`).

**Pieces:**

| File | Role |
|---|---|
| [web/i18n/<locale>.json](web/i18n/) | Authoring source — flat `"area.key": "string"` catalogues (`en`/`fr`/`es`/`de`). `en` is the base + fallback. |
| [web/i18n/locales.js](web/i18n/locales.js) | **Generated** bundle (`window.OMNIS_I18N = {en,fr,es,de}`), committed like the vendored Monaco/xterm assets. Regenerate with `make i18n` ([scripts/build_i18n.mjs](scripts/build_i18n.mjs), node). |
| [web/i18n.js](web/i18n.js) | Runtime: `window.tr(key, vars?)`, `window.trN(key, count, vars?)` (plurals via `Intl.PluralRules`), `window.I18N` = `{ locale, LOCALES, setLocale(id), translateDom(root), reconcileServerLocale(s) }`. |
| [server/preferences.go](server/preferences.go) | `preferences.Locale *string` — persists the chosen locale (merged PUT, like theme/notifications). |

**Load order** ([web/index.html](web/index.html)): `locales.js` → `i18n.js` →
`app.js` → `settings.js`, all `defer` (defer runs in document order after parse),
so `tr()`/`I18N` exist before app/settings render. `app.js` also installs an
**i18n safety shim** at the top (if `window.tr` is missing it stubs `tr`/`trN`/
`I18N`) so a failed `i18n.js` degrades to English keys instead of crashing.

**How strings are localised:**
- **Static markup** — annotate elements with `data-i18n` (textContent),
  `data-i18n-html` (innerHTML, for the few strings with inline `<code>`),
  `data-i18n-tip` / `data-i18n-placeholder` / `data-i18n-aria-label` /
  `data-i18n-title` (attributes). Keep the English text in the markup as the no-JS
  fallback. `I18N.translateDom(document.body)` runs once at boot; `createPanel`
  calls `I18N.translateDom(frag)` on each cloned `#chat-pane-tpl`.
- **Dynamic JS strings** — `tr("area.key"[, vars])`; interpolate `{placeholder}`
  from `vars`; pluralise with `trN("area.key", count)` (catalogue holds
  `key.one`/`key.other`). The function is named **`tr`, not `t`** (`t` is a loop
  variable throughout settings.js). Date/number formatting passes `I18N.locale`
  to `toLocale*` where localised output matters.
- **Language picker** — Settings → Appearance has a Language section
  (`renderAppearance`); selecting a locale calls `I18N.setLocale(id)`, which
  persists (localStorage + `PUT /api/preferences {locale}`) and **reloads** so
  every string re-renders. `syncThemeFromServer` reconciles a different
  server-side locale once per tab (guarded), so a second browser/device converges.
- **First-run language: ask, never auto-switch.** The UI **defaults to English**
  and `resolveLocale` ([web/i18n.js](web/i18n.js)) **no longer auto-applies
  `navigator.language`** — a non-English user may prefer the English UI. Instead,
  when the browser prefers a *supported, non-English* language (`detectBrowserLocale`
  → `I18N.detectedLocale`) and there is **no recorded choice** (no localStorage
  `agent_toolkit_locale` and no `preferences.json` `locale`), `maybePromptLocale`
  ([web/app.js](web/app.js), run once at boot before `maybePromptNotifications`,
  awaiting `Settings.prefsReady`) shows a **bilingual** `uiConfirm` — title +
  message rendered in **both English and the detected language** via
  `I18N.trIn(localeId, key, vars)` (look up a key in a *specific* locale), with the
  Switch/Keep buttons in their respective languages. "Switch" → `I18N.setLocale`
  (persist + reload); "Keep English" → `I18N.persistLocale("en")` (records the
  choice **without** a reload, since the page is already English) so it's asked at
  most once per home dir. `uiConfirm` gained an optional `cancelText`, and
  `.ui-modal-message` is `white-space: pre-line` so the two-paragraph bilingual
  message keeps its line break. Catalogue keys: `app.locale.{offerTitle,offerMsg,
  useLanguage,keepEnglish}` ({language} = the native locale label from `I18N.LOCALES`).

**Translation policy / glossary** — do **not** translate tool names (`Bash`,
`Read`, …), config keys, model ids, file paths, slash-command names, or product
nouns (`Omnis`, `Monaco`, `MCP`, `A2A`, `Squad`, `Skills`); translate everything
else (labels, tooltips, hints, placeholders, dialog/status/toast/menu text).

**Adding/maintaining strings:** add the key to **`web/i18n/en.json`** (+ the three
translations), replace the literal with `tr(...)`/`trN(...)` or a `data-i18n*`
attribute, then run `make i18n` to regenerate `locales.js` and commit it with the
JSON. The generator **warns** when a non-`en` locale is missing keys (the runtime
falls back to English, so partial coverage is safe). Bump the `?v=` query on the
`i18n/locales.js` + `i18n.js` (and `app.js`/`settings.js`) script tags in
index.html when their contents change.

**Coverage status:** the **entire Web UI** is translated (en/fr/es/de at full key
parity — ~610 keys/locale) — all static markup, the whole **chat surface**
(`app.js`: status, dialogs, ask-user wizard, folder/session/turn context menus,
update + provider-health modals, toasts, errors) **and all of `settings.js`**
(navigation + every panel: Agents/Squads/Globals, Models & Providers, Permissions,
MCP, A2A, Hooks, Skills, Commands, Registries hub + remote browse/detail views, the
banner/reload/restart flow, and all confirm/prompt/status messages). Untranslated
**data** is intentional: theme proper-names, doc-page titles in the Documentation
TOC, tool/config-key identifiers, and `${input:id}`/code snippets. **Not extracted
(graceful English fallback):** the `web/docs/*.md` documentation *content* and Go
server-emitted strings. Extending is mechanical: same `tr()` + `en.json` +
translate + `make i18n` pattern. **Gotcha:** `t` and `tr` appear as local/loop
variable names in `settings.js` (e.g. a trash-button `const`); never shadow the
global `tr()` — the i18n pass renamed one such `const tr` to `delBtn`.

## Distribution / packaging

omnis ships through several channels, all driven by `.goreleaser.yaml` +
`.github/workflows/release.yml` and the assets under `packaging/`:
goreleaser raw binaries (`make release`), `.deb`/`.rpm` (nfpms → `/etc/omnis`),
a Homebrew formula (`brews:` → `$(brew --prefix)/share/omnis`), a Windows MSI
([packaging/windows/omnis.wxs](packaging/windows/omnis.wxs) → `C:\ProgramData\Omnis`),
and **pip wheels** (`make wheels`). All non-FHS wrappers rely on omnis embedding
**no** defaults — they bundle the config/registry/web tree and point the binaries
at it via `OMNIS_SYSTEM_CONFIG_DIR` + `OMNIS_WEB_DIR` (see the env-var table).

**pip — `omnis-agent`** ([packaging/pip/](packaging/pip/), built by
[scripts/build_wheels.py](scripts/build_wheels.py)): per-platform binary wheels
(`py3-none-<plat>`) that bundle the two static Go binaries + a `sysconf/`
(config JSONs + `filters/` + `registry/`) + `web/` as package data. Because the
binaries are `CGO_ENABLED=0` static builds, **all six platform wheels
cross-compile on one Linux host** (no per-OS CI matrix); platform tags are
`manylinux2014_{x86_64,aarch64}`, `macosx_{10_13_x86_64,11_0_arm64}`,
`win_{amd64,arm64}`. The console scripts stay `omnis` / `omnis-server`; the
distribution name is `omnis-agent`. The thin Python launcher
([packaging/pip/src/omnis/launcher.py](packaging/pip/src/omnis/launcher.py)) sets
`OMNIS_WEB_DIR`/`OMNIS_SYSTEM_CONFIG_DIR` to the bundled tree **only when unset**,
**seeds `~/.omnis`** (config + registry, never overwriting existing files —
[seed.py](packaging/pip/src/omnis/seed.py), also the `omnis-seed --force` command),
then `exec`s the real binary. The build version is normalised to PEP 440
(`v1.2.3-rc1` → `1.2.3rc1`). CI publishes via the `pypi` job (`twine upload`,
needs a `PYPI_API_TOKEN` secret); rc/beta tags become PEP 440 pre-releases.
This needed **no Go changes** — seeding into `~/.omnis` reuses the existing
per-user config layer (`paths.Home()`).

## Commands

```bash
# Build (production binaries only)
make build              # bin/omnis + bin/omnis-server (host platform)
make build-root         # bin/omnis only
make build-server       # bin/omnis-server (HTTP API)
make examples          # opt-in: build all examples under bin/
make release            # cross-platform raw binaries → dist/
make package            # cross-platform + .deb + .rpm + .zip → dist/ (requires goreleaser)
make package-check      # validate .goreleaser.yaml without building
make wheels             # per-platform pip wheels (omnis-agent) → dist/wheels/ (override WHEEL_PLATFORMS=)

# Test
make test               # all unit tests
make env-tests          # LLM integration tests (requires .env with API keys)
go test ./core/tools -run TestRunBashSafetyFloorAndOutput   # single test
go test ./internal/a2a/...                                  # A2A unit tests
make a2a-smoke A2A_URL=http://127.0.0.1:8091/              # live A2A smoke test

# Code quality
make fmt                # go fmt ./...
make vet                # go vet ./...
make tidy               # go mod tidy

# Web UI
make i18n               # regenerate web/i18n/locales.js from web/i18n/<locale>.json (see "Web UI internationalisation")

# Run — three usage modes
go run .                                # CLI: REPL when TTY, one-shot when piped
go run . "explain main.go"              # CLI one-shot with prompt argument
echo "summarize repo" | go run .        # CLI one-shot reading stdin
go run . tui                            # TUI: tview chat interface
make run-server                         # Server: HTTP API + web UI (needs OMNIS_SERVER_TOKEN)
bin/omnis-server                         # Server in the foreground (default form)
bin/omnis-server start [--no-browser]    # Server detached in the background (frees the terminal)
bin/omnis-server stop                    # Stop the background server (graceful SIGTERM)
bin/omnis-server status                  # Report whether the background server is running

# Auxiliary subcommands
go run . -d                             # debug: log full payloads (any mode)
go run . curate --user u --session s    # manual soft-skill curation
go run . reindex-precedents              # rebuild the cross-session precedent index (needs an embedder)
go run . reindex-docs                    # rebuild the documentation semantic index (needs an embedder)
go run . version                        # version info

# Examples (opt-in; not part of `make build`)
make build-example-s11_todo    # build a single example
go run ./examples/s21_skills   # run an example directly
```

## Architecture

**Design contract**: the same binary becomes a code reviewer, Kubernetes triage assistant, or DBA helper purely by mounting different tools, skills, and MCP servers. No code changes required to retarget the agent.

Built on [google.golang.org/adk](https://pkg.go.dev/google.golang.org/adk) for the agent loop, session, plugins, and runner.

### Agent topology

```
main.go / server/
    └── agent.NewAgent()            ← single wiring entry point
            ├── Squads              ← one wired tree per squad in agent.json
            │    ├── "omnis"        ← Omnis ROUTER squad (default for new chats) — leaderless
            │    │    └── omnis              ← routes each request to the best squad (route_to_squad), then steps out
            │    ├── "default"      ← leader + full team (used when a session omits a squad / routed to)
            │    │    ├── leader              ← coordinator (fs tools + planning + mailbox + handoff_to_router)
            │    │    │    └── a2a_<name>…   ← one tool per peer in a2a_config.json
            │    │    ├── investigator        ← read-only evidence gatherer (tool-wrapped, not transfer_to_agent)
            │    │    ├── web_agent           ← web search + fetch
            │    │    ├── summariser          ← condenses bulk output
            │    │    └── agentmd_reviewer     ← read-only fresh-eyes verifier for /init-generated AGENT.md
            │    └── "research"     ← leader + smaller team, selectable per session
            │         ├── leader
            │         ├── web_agent
            │         └── summariser
            ├── reflector           ← post-session LLM analyst that tags loaded soft-skills (one hook per generation; optional — heuristic fallback when disabled)
            └── curator             ← process-wide post-session soft-skill distiller (one hook per generation)
```

A **squad** is a named group `{ leader, members[] }` composed from the
agents defined in `agents.json`. Each chat session selects one
squad at creation (default when none is chosen); the server resolves
`Instance.Squad(name).Runner` per session, so two sessions running on the
same generation can use different squads. Squads only *reference* agents
— skills, tools and MCP servers stay on the agent definitions, so two
squads that share a member also share that member's wiring (and the MCP
pool dedups any subprocess backing it).

**Leaderless squads** — a squad with `leader` set to `"none"` (or empty)
and **exactly one member** runs that single agent **directly as the runner
root**, with no coordinator: no sub-agent delegation tools, no coordinator
instruction, and tools limited to exactly what the agent declares (plus the
always-on essentials below). This is the right shape for a specialist that
has nobody to coordinate (e.g. the `Helper` squad). ≥2 members require a real
leader; the default squad always has one. ([agent/squad.go](agent/squad.go)
keys on `RuntimeSquadConfig.Leader == ""`; `resolveSquadEntries` in
[agent/runtime_config.go](agent/runtime_config.go) normalises `"none"`→`""`
and enforces the one-member rule.)

### Omnis router (default chat routing)

**Omnis** is a special **leaderless squad** (single member: the `omnis` agent)
that is the **default agent for every new chat**. Unlike a squad leader — which
orchestrates its own members and keeps control — the router *transfers control
of the conversation*: it reads the user's request, picks the squad best able to
handle it, and hands over; that squad's leader then answers directly. When the
user later changes topic to something outside the active squad's scope, the
squad hands control **back** to the router, which re-routes. When intent is
unclear and no squad fits, Omnis converses with the user instead of routing.

The whole mechanism is **host-side and config-driven** ([agent/routing.go](agent/routing.go)):

- **Two tools, gated in [agent/squad.go](agent/squad.go) `buildSquadInstance`.**
  `route_to_squad(squad, reason)` is mounted **only** on the router root
  (and validates `squad` against the non-router squad catalogue, which is also
  injected into the router's instruction). `handoff_to_router(reason)` is
  mounted as an **always-on** tool on **every non-router squad root** when routing
  is enabled (a short "hand back if out of scope" block is appended to those
  leaders' instructions). Both tools only **record a per-session directive**
  (target + reason) in the process-wide `RouteRegistry` on `Infrastructure`
  (`RouteDirectives`); they never run another runner themselves. Note they carry
  **no `prompt`** — the squad always receives the user's verbatim message (see
  dispatch loop), so the router cannot paraphrase or twist the request.
  **Both tools end the run the instant they record a directive** by setting
  `ctx.Actions().SkipSummarization = true`, which makes their function-response
  event `IsFinalResponse()` (ADK `session.Event`) — terminating the agent flow
  loop immediately. This is a **host-side guarantee**, not an instruction the
  router may ignore: the ADK LLM flow loop ([internal/llminternal/base_flow.go](file:///home/bertrand/go/pkg/mod/google.golang.org/adk@v1.2.0/internal/llminternal/base_flow.go)
  `Flow.Run`) has **no iteration cap** and only stops when the model returns a
  tool-call-free response, and the directive is consumed by the dispatch loop
  only *after* `Runner.Run` returns — so without this, an unlucky router sample
  that narrates or re-calls tools after routing would spin the hop forever
  ("working… (Ns)" with no reply, the reported bug). `ask_squad` does **not**
  set it (the router must act on the verdict).
- **Capability probe (`ask_squad`)** — also router-only. When the router is
  *unsure* which squad fits, it privately checks a candidate before committing:
  `ask_squad(squad, request)` ([agent/routing.go](agent/routing.go)
  `askSquadTool`/`probeSquadCapability`) resolves that squad's lead from the
  `runtime` snapshot and makes **one isolated, non-streamed
  `model.LLM.GenerateContent` call** (lead's own model + instruction, **no
  runner/tools/sub-agents/event bus** — the one-off-LLM pattern from
  [internal/compress/statelog.go](internal/compress/statelog.go) `extractStateLog`),
  returning the lead's `CAN_HANDLE`/`CANNOT_HANDLE` verdict
  (`parseCapabilityVerdict`). Because it touches neither the SSE stream nor the
  shared bus it is **hidden by construction**; it does not hand over control
  (only `route_to_squad` does). The router probes the next candidate on a
  decline and, when all plausible squads decline, asks the user instead of
  force-routing. It's a *scope judgment only* — the probed squad never runs its
  tools. Confident routes skip the probe entirely. **Probes are capped per turn**
  (`RouteRegistry.IncProbe`/`ResetProbes`, limit `len(squads)+2`, reset at the
  start of each `RunWithRouting` turn): over budget, `ask_squad` returns an error
  nudging the router to decide now — a defense-in-depth backstop (alongside the
  `SkipSummarization` termination above and `routerMaxHops`) against a router
  that keeps probing without committing. The router's own routing/probe
  tool-call frames (`route_to_squad`/`handoff_to_router`/`ask_squad`) are
  suppressed in the web UI by `isRoutingTool` ([web/app.js](web/app.js), mirroring
  the `isTodoTool` special-case) so the negotiation stays hidden — the `routing`
  chip is the only visible signal.
- **Dispatch loop** = `Manager.RunWithRouting(ctx, user, session, startSquad,
  initialParts, routerParts, run, notify)`. It runs the starting squad via the
  surface-supplied `run` callback (which streams/echoes the hop), then `Take`s the
  directive: a `route` switches to the named squad, a `handoff` switches back to
  the router; it re-dispatches and repeats, up to `routerMaxHops` (4) — the
  directives decide *where* control goes, never *what* the squad receives.
  **Handoff decline-tracking**: when a squad calls `handoff_to_router` the loop
  records it (and its reason) in a per-turn declined set, and on the *next router
  hop* appends a synthetic `routerDeclineNote` part to `routerParts` naming the
  squads that already handed this request back ("Do NOT route to those squads
  again …"). Without it the router re-saw the identical clean view and bounced
  the request straight back to the squad that just declined it, looping until
  `routerMaxHops` — so the note is what lets a handoff make the router pick a
  different squad or ask the user instead.
  **Two part-views**: every **answering (non-router) hop** gets the user's
  **original turn input (`initialParts` — verbatim text + any attached files)
  unchanged**, so attachments always reach the answering squad and the request
  can't be twisted; the **router hop** instead gets `routerParts`, a **clean
  text-only view** the surface builds (the user's prompt + a one-line "a file is
  attached" note when relevant). The router has **no file tools**, so it must not
  be fed the inline attachment blobs or the "use the `mime`/`read` tools"
  attachment note baked into `initialParts` — doing so made it try to "read" an
  attached PDF and then hallucinate an "update your plan?" `ask_user` step. Pass
  `nil` for `routerParts` to feed the router the original parts too (the loop
  keys router-view vs. original on `squadName == routerSquad`). `notify(from,to,
  reason)` lets the surface show the transition and persist the new squad. All
  three surfaces use it — server ([server/sse.go](server/sse.go) `handleMessages`,
  building `routerParts` from `req.Prompt` + file count, emitting a `routing` SSE
  event + `Registry.SetSquad`), TUI ([internal/tui/tui.go](internal/tui/tui.go)
  send path, `routerParts == nil` since TUI turns are text-only), and CLI
  ([internal/cli/cli.go](internal/cli/cli.go) `runTurn`, `routerParts` = the
  prompt only, so the router never sees inlined `@file` contents; `runCLI`
  re-wired through `Infrastructure`+`Manager` like the TUI).
- **Router-hop text is suppressed (host-side, not just by instruction).** The
  router model often narrates ("Routed to the default squad; it will take over…")
  alongside its `route_to_squad` call despite the instruction telling it to stay
  silent — instructions can't *guarantee* silence. So each surface **withholds the
  router hop's assistant text** during the stream and decides afterwards via
  `Manager.PendingRoute(sessionID)` (a non-clearing `RouteRegistry.Peek` of the
  pending directive): **a route is pending ⇒ the text was chatter — discard it
  from both the chat and the persisted turn** (the `run` callback returns `""`);
  **no route ⇒ the text is a genuine reply** (a clarifying question) and is flushed
  to the user. The only visible routing signal is the `routing` chip / "── routed
  to X squad ──" line from `notify`. Server: `streamEvents` gained a `suppressText`
  arg (withholds `token`/`message` frames, still streams tool calls / `ask_user` /
  bus events, still accumulates the text for the return value); `runHop` flushes a
  single `message` frame on the no-route path. TUI/CLI mirror it (TUI suppresses
  mid-stream `flushMarkdown`/`appendChat` and renders only on no-route; CLI's
  `stream(seq, quiet)` withholds stdout text). Routing tool-call frames are
  *additionally* hidden in the web UI by `isRoutingTool`.
- **Hallucinated tool calls on the router hop are swallowed host-side.** The
  router LLM sometimes pattern-matches an execution request ("run … in the
  background") to a tool it doesn't have (`bash_background`) and calls it; ADK
  answers with a tool-not-found error that would render as a scary `ERROR` block
  before the router recovers and routes. On the router hop (`suppressText`),
  `streamEvents` ([server/sse.go](server/sse.go)) drops any `tool_call` /
  `tool_result` whose name isn't in `routerVisibleTools` (the routing/ask/teammate
  tools the router legitimately has), so the hop stays silent and only the route
  happens. The Omnis instruction ([registry/agents/omnis/instruction.md](registry/agents/omnis/instruction.md))
  also explicitly tells it to treat run/execute/file requests as a routing signal
  and never call an execution tool — but the host-side drop is the guarantee, since
  instructions can't *prevent* the hallucination.
- **Per-squad context is retained within a session.** Because each
  `SquadInstance` owns a private `session.InMemoryService` and is stable across
  turns within a pinned generation, going squad A → B → A returns the **same** A
  runner whose history still holds A's earlier turns (the loop only re-resolves
  runners via `Manager.LookupSquad`, never rebuilds them; the user turn is
  appended, not a reset). Retention does not survive a hot-reload (fresh
  in-memory sessions) — same boundary as a single-squad session today.
- **Default for everyone, opt-out.** `router_squad` in `agents.json` (or
  `OMNIS_ROUTER_SQUAD`) names the router squad; **absent ⇒ defaults to `omnis`**,
  `"none"` disables. `ensureRouterSquad` ([agent/routing.go](agent/routing.go)),
  called in `BuildInstance` (not `ResolveRuntimeSettings`, so config-only tests
  are untouched), **injects** the `omnis` agent (registry def if present, else the
  built-in `defaultRouterInstruction`, inheriting the leader's model) and a
  leaderless `omnis` squad when a config doesn't already declare them. New
  sessions default to `Manager.RouterSquad()` on every surface; when routing is
  disabled the path is byte-identical to before (no tools mounted, single hop).

**Config-driven root tools** — both a coordinating leader and a leaderless
root build their tools from the root agent's declared `tools` groups via
`toolsForAgentConfig` (the same resolver sub-agents use), so a squad root is
limited to exactly the capability groups it declares — it no longer inherits a
fixed coordinator toolset. Infra-scoped coordination groups are declarable
keys: `planning` (todo + task graph), `worktree`, `bg`. **Always-on for any
squad root** (not gated): the teammate **mailbox** (so the root stays
reachable cross-session — another squad can `teammate_ask` the Helper to
install a skill) and **ask_user**. **Coordinating-leader-only** (skipped when
leaderless): sub-agent delegation tools, `curate_session`,
`record_session_feedback`. A coordinating leader additionally keeps
embedder-backed soft-skill recall (`toolsForAgentConfig`'s `asLeader` path);
sub-agents and leaderless roots use the glob-only per-agent soft-skill loader.
Because the default `leader` and `skill_editor` previously got several tools
unconditionally, their `agent.json` now declares them explicitly
(`planning`/`worktree`/`bg`, plus `softskills`/`calc` for `skill_editor`) so
behaviour is unchanged.

Sub-agents are wrapped via `agenttool.New()` and exposed as **tools** on
the leader (not via `transfer_to_agent`), so control always returns to
the leader after a sub-agent call. By default each sub-agent runs one at a
time ([agent/non_concurrent_tool.go](agent/non_concurrent_tool.go)
`newNonConcurrentTool`, single-task schema): a mutex rejects a duplicate
concurrent call of the same sub-agent with an "already running" error. The
wrapper's `ProcessRequest` packs **itself** (via `packToolDecl`), not the
inner agenttool — ADK dispatches function calls by the object stored in
`req.Tools[name]`, so registering the inner there would call the inner's
`Run` directly and bypass the mutex (the declaration is identical either way,
so the model sees no difference). Setting an agent's
`max_instances > 1` in its `agent.json` swaps that wrapper for
[agent/parallel_agent_tool.go](agent/parallel_agent_tool.go)
`newParallelAgentTool`: the leader then sees a **batch/fan-out** tool whose
schema is `{ tasks: [ <inner-input>, … ] }` (the per-task shape mirrors the
sub-agent's own input schema), capped at `max_instances`. `Run` fans the
tasks out concurrently — each via the inner `agenttool.Run`, which builds
its own runner+session per call, so concurrent invocations of one
stateless sub-agent are safe — bounded by a semaphore of width
`max_instances`, preserves order, isolates per-task errors into their slot,
and returns `{ results: [ … ] }`. Because ADK dispatches function calls via
`req.Tools[name]`, the parallel tool's `ProcessRequest` packs **itself**
(not the inner) via the local `packToolDecl` (a copy of ADK's unexported
`toolutils.PackTool`) so the model gets the batch declaration and the runner
dispatches the fan-out. `max_instances` defaults to `1` and is per-agent.
The web UI Settings → Agent panel exposes it as a **Max parallel instances**
numeric field (a `Parallelism` section, hidden for the leader and curator
since both are excluded from fan-out); the value round-trips through the
editor save and the GET only surfaces it when `> 1` to keep agent.json clean.
The curator stays a single per-generation hook listening across every
squad.

**Durable / re-attachable sub-agent sessions (`resumable_sessions`)** — by
default each sub-agent call is a stateless pure function (agenttool builds a
throwaway session per call). Setting `"resumable_sessions": true` on an agent in
its `agent.json` swaps the inner agenttool for
[agent/resumable_agent_tool.go](agent/resumable_agent_tool.go)
`newResumableAgentTool`, which owns **one persistent runner + session service**
and a `handle → session` map: each call **returns a `session` handle**, and the
leader can pass it back as **`resume_session`** to CONTINUE that exact
conversation (the sub-agent keeps its prior context/work) instead of starting
fresh. It implements the same `runnableTool` interface, so it slots into the
**same** non-concurrent / parallel wrappers — **durability composes with
`max_instances`** because identity is the per-call handle, not the agent name:
each parallel task mints its own handle, resume always addresses one specific
handle (an in-use handle can't be double-resumed — guarded). Sessions are bounded
**without any cross-session GC**: a 30-min idle TTL + 32-session LRU cap, swept
on each call; handles are generation-scoped (a hot-reload drops them, and a stale
handle silently falls back to a fresh session — the same retention boundary the
leader's own sessions have). This is what lets the leader **resume** a sub-agent
it stopped for mid-turn steering (see "Mid-turn steering") rather than re-running
it from scratch. Off by default ⇒ byte-identical to the stateless agenttool.

**Soft-skill reflection pipeline** — at `EventSessionEnd`, [agent/load_recorder.go](agent/load_recorder.go)
drains its in-memory bucket (leader-loaded skills, tool errors), runs
the deterministic `softskills.ReflectHeuristic`, applies the heuristic
tags to `softskills/_stats.json`, and emits `EventSessionReflected`
with the gathered payload. [agent/curator_hook.go](agent/curator_hook.go)
subscribes to that event: when a `reflector` agent is enabled it runs
the LLM Reflector ([internal/softskills/reflector.go](internal/softskills/reflector.go))
with a 60-second timeout, merges its Outcome over the heuristic (LLM
wins on overlap), `Retag`s the stats to reflect the override, then
gates and runs the curator. `EventCurateNow` (manual `/learn-now`)
bypasses the reflector and drives the curator directly.

**Sub-agent boundary events** — sub-agents run inside agenttool's private
runner, so neither `EventRunStart/End` nor `EventBeforeRun/AfterRun` fire
on the shared bus for their internal turns. To give reflection hooks a
clean "one sub-agent invocation finished" signal, [agent/subagent_event.go](agent/subagent_event.go)
subscribes to the leader's `EventBeforeTool / EventAfterTool / EventToolError`
and re-emits any payload whose `tool` name matches a sub-agent as
`EventSubAgentStart / EventSubAgentEnd`. Payload keys: `agent` (the
sub-agent), `caller_agent` (the leader), `user_id`, `session_id`, `input`,
`output` (end only), `duration` (end only), `error` (end only, on tool
error), `call_id`, `run_id`. Registered once per Instance from sub-agent
names spanning every squad; subscriptions detach on hot-reload.

**`run_id` on every event** — `EventRunStart / EventRunEnd / EventBeforeTool /
EventAfterTool / EventToolError / EventBeforeModel / EventAfterModel`
all carry a `run_id` field set to ADK's `InvocationContext.InvocationID()`.
It is stable across BeforeRun + AfterRun for a single `Runner.Run` call
and lets [agent/subagent_hook.go](agent/subagent_hook.go) buffer all
sub-agent invocations observed during one leader turn for the Phase 6
per-invocation tagger. Sub-agent internal runs get their own (different)
`run_id`s, so the leader-side `EventSubAgentStart/End` events keep the
leader's `run_id` (which is what we group on).

**Sub-agent reflection pipeline (Phase 6)** —
[agent/subagent_hook.go](agent/subagent_hook.go) opens a per-`run_id`
buffer at each `EventSubAgentStart`, attributes `load_softskill` events
and `tool_error`s to the open invocation, captures the leader's
`AfterModel` text for the lexical reaction scan, and at `EventRunEnd`
walks the buffer to call `softskills.TagInvocation` per invocation
(retry detection via "same sub-agent appears later in the same run",
`Error:` / empty output detection, leader reaction via
`ClassifyLeaderReaction`'s approval / retry / unknown classifier).
Resulting tags are applied to `_stats.json` via `Stats.RecordTag`.

### Key packages

| Path | Role |
|---|---|
| `agent/` | `NewAgent()` — wires all components; `ResolveRuntimeSettings()` — config precedence; `ResolveEmbedder()` — builds the semantic embedder from `embed_model_ref`/`OMNIS_EMBED_*` |
| `core/agentkit/` | `New()` — thin ADK agent constructor |
| `core/llm/` | Multi-provider dispatcher: `anthropic`, `openai`, `gemini`, `openai_compat` |
| `core/embed/` | Text→vector embedder mirroring `core/llm`: `Embedder` iface, `Selection`, `NewWithSelection`; providers `openai`/`openai_compat`/`gemini` (anthropic ⇒ `ErrUnsupported`); L2-normalised output + content-hash on-disk cache. Powers all semantic recall |
| `core/tools/` | File-system tools: `Read`, `Write`, `Grep`, `Glob`, `revert`, `Bash` (with safety floor) |
| `core/permissions/` | Permission gating in Claude Code nomenclature: `permissions.{allow,ask,deny}` of `Tool(specifier)` rules, deny→ask→allow; auto-converts old `always_*` files |
| `core/events/` | Event bus + file logger; before/after model/tool callbacks + session lifecycle |
| `internal/tasks/` | Durable task graph; persisted to `logs/agent_tasks_<u>_<ts>.json` |
| `internal/todo/` | Lightweight scratch list; persisted to `logs/agent_todo_<u>_<ts>.json` |
| `internal/bg/` | Per-session background task queue + **task registry**: `bash_background` (one-shot), `monitor` (streaming line-matcher, [monitor.go](internal/bg/monitor.go)), and lifecycle `bg_list`/`bg_cancel`/`bg_output` ([tasks.go](internal/bg/tasks.go)) — named with a `bg_` prefix to avoid colliding with the `planning` group's task-graph `task_list`. Every launch registers a `Task{ID,Kind,Status,…}`; completions/streamed matches push a `Notification` consumed by the host (see "Background task notifications") |
| `internal/worktree/` | Git worktree isolation tools |
| `internal/steer/` | Per-session **mid-turn steering** store (extra info the user types while a turn is computing): `Enqueue`/`Drain`/`TakeConsumed`/`TakePending`/`Forget`. Drained into the running turn by the steering plugin and looped by each surface (see "Mid-turn steering") |
| `internal/teammates/` | Inter-agent mailbox FSM: `teammate_ask/tell/check/list`. The leader's `teammate_check` is suppressed when the host drains the inbox in the background (see "Background mailbox delivery") |
| `internal/skills/` | Skill loader: `load_skill`, `list_skills` (reads `registry/skills/<name>/SKILL.md`). `load_skill` is wrapped by a process-wide dependency gate (`SetDepGate`/`RequiresFor`, [internal/skills/deps_gate.go](internal/skills/deps_gate.go)) — see "Tool dependency enforcement" |
| `internal/deps/` | Runtime tool-dependency gate: `Requirement`/`Install` (a binary that must be on PATH + a scalar-or-per-OS install command, parsed from YAML **and** JSON), `Present`/`Missing` (PATH check via `exec.LookPath`), and `Ensure` (check → ask user → install via the Bash safety floor → recheck). `NewAskuserConfirmer` + `BashInstaller` adapters. Backs the skill `requires:` load_skill gate and the MCP `requires` connect gate |
| `internal/shellcomplete/` | Dependency-free bash-like tab completion (`Complete(line, cwd)`): `$PATH` executables for the first token, filesystem paths otherwise. Backs the `!` shell-escape completion in TUI + web. `CompletePath(token, cwd)` is the path-only variant backing `@file` reference completion |
| `internal/fileref/` | "@path" chat file references: `Spans`/`Tokens`/`Classify`/`Resolve`/`Context`. Parses `@`-prefixed path tokens (at line start or after whitespace, so emails are excluded), classifies them as file/dir/missing, and inlines referenced **file** contents as an extra user-turn part. Shared by the server, TUI, and CLI send paths; the grammar is mirrored in `web/app.js` |
| `internal/agentmd/` | AGENT.md project memory (omnis's `CLAUDE.md` equivalent): `Resolve(cwd)` discovers + concatenates AGENT.md across layers (system → user → `.agents/` → project walk-up) with a per-cwd mtime cache; `InitPrompt()` is the shared `/init` bootstrap prompt; `AppendMemory(cwd, line)` backs the `#` shortcut. Injected into the leader/root system instruction per turn by the `agentmd` plugin ([agent/agentmd_plugin.go](agent/agentmd_plugin.go), registered in [agent/build_plugins.go](agent/build_plugins.go)) |
| `internal/softskills/` | Curator output: `load_softskill`, `list_softskills` (reads `softskills/`); `Stats` sidecar + `ReflectHeuristic` (deterministic per-skill helpful/harmful/neutral tagging); `recall.go` adds the embedder-gated `recall_softskills` semantic-rank tool |
| `internal/semindex/` | Reusable persistence + query layer over a go-turbovec `IdMapIndex` (`.tvim` + `.meta.json` sidecar + manifest); `Open`/`Upsert`/`Query`/`Remove`/`Save`. Backs all five recall features; nil-embedder handles degrade with `ErrNoEmbedder` |
| `internal/precedents/` | Cross-session precedent index over `semindex` at `index/precedents`; indexes each session's goal + decisions; `recall_precedents` tool |
| `internal/codeindex/` | Per-repo semantic code index over `semindex` (line-window chunks, `git ls-files`-aware, content-hash incremental); `search_code` + `reindex_code` tools |
| `internal/regindex/` | Semantic index over **remote registry** items of **all seven kinds** (skills, agents, mcp, a2a, squads, commands, permissions) over `semindex` at `index/registries`; metadata-only (name+description+tags, no extra fetch beyond a browse); accurate `installed` flags via per-kind installed-name thunks on `Config` (shared with `buildRegistriesDeps`); `search_registries` + `reindex_registries` tools. Rebuilds on registry-set change (corpus-hash self-heal in `Search` + `registries.OnSave` background hook) |
| `internal/docindex/` | Semantic index over **omnis's own documentation** (user docs `web/docs` + developer docs `docs` → `/usr/share/doc/omnis/docs`; roots from `Roots()`, override `OMNIS_DOCS_DIRS`) over `semindex` at `index/docs`; markdown line-window chunks, content-hash incremental, heading-aware, stores the quotable text in chunk meta; `search_docs` + `reindex_docs` tools plus always-on `list_docs`/`read_doc`/`grep_docs` glob fallback (`NewNavTools`). Mounted on the `helper` agent via the `docs` tool group; built/refreshed in the background at server startup |
| `internal/compress/` | Per-session context compression plugin + audit/statelog files |
| `internal/cache/` | Prompt cache hit-rate stats plugin |
| `internal/mcp/` | MCP config loader (path resolved from search chain) |
| `internal/a2a/` | A2A protocol client (`client.go`) + ADK tool wiring (`tools.go`); config types in `a2a.go` |
| `internal/tui/` | tview chat UI (trace pane + streaming chat) |
| `internal/selfupdate/` | In-app package auto-update: `DetectMethod` (deb/rpm/brew/msi/pip/raw, runtime detection of how the running binary was installed), `CheckLatest` (GitHub `/releases/latest`, stable-only, semver-gated), `Install` (per-method, `sudo -S` for deb/rpm), `ManualInstructions` fallback. Server-only; see "Self-update" |
| `server/` | HTTP API server with Bearer token auth |
| `server/a2a_server.go` | Receives inbound A2A `tasks/send` / `tasks/sendSubscribe` calls; routes by squad + session |

### Semantic recall (embedder + vector indexes)

Five **additive, embedder-gated** recall features share `core/embed` +
`internal/semindex` (a wrapper over the `go-turbovec` pure-Go ANN index,
BitWidth 4 + UnitNorm cosine):

1. **`recall_softskills`** (leader) — semantically ranks curator-distilled
   soft-skills for the user's task; mounted beside the glob `list_softskills`
   ([internal/softskills/recall.go](internal/softskills/recall.go)). Index
   refreshes on call, content-hash gated.
2. **`recall_precedents`** (reflector + curator) — recalls past sessions' goals
   + decisions ([internal/precedents/](internal/precedents/)). Indexed on
   `EventSessionReflected` by [agent/precedents_hook.go](agent/precedents_hook.go).
   Web UI sessions never fire `EventSessionEnd` (so never `EventSessionReflected`),
   so the server also indexes them via the lightweight, indexing-only
   `EventSessionIndexNow` trigger (same hook): the idle indexer
   ([server/idle_indexer.go](server/idle_indexer.go)) fires it once a session
   has been idle ≥ 5 min (fixed threshold, independent of the curator's
   `OMNIS_CURATOR_IDLE_TIMEOUT`), and the archive handler fires it immediately on
   `POST /api/sessions/:id/archive`. An in-memory `SessionMeta.Indexed` flag
   (set by `Registry.MarkIndexed`, cleared by `Touch`) stops re-indexing every
   scan tick. Backfill via `omnis reindex-precedents`.
3. **`search_code` / `reindex_code`** (investigator) —
   semantic code search over the repo ([internal/codeindex/](internal/codeindex/)),
   `git ls-files`-aware, content-hash incremental.
4. **`search_registries` / `reindex_registries`** (helper) —
   semantic search over **every kind** advertised by the configured remote
   registries — skills, agents, mcp, a2a, squads, commands, permissions
   ([internal/regindex/](internal/regindex/)). Mounted alongside the
   glob `browse_registry` whenever the `registries` tool group is present and an
   embedder resolves. The crawler's `browse_registry` / `get_remote_item` /
   `install_remote_item` tools likewise cover all seven kinds (command install
   writes the per-user `user_commands.json` via the shared
   [internal/usercommands/](internal/usercommands/) package, which also backs the
   web-UI command editor; permission install merges rule-sets into
   `permissions.json`). **Metadata-only**: embeds the name/description/tags a
   browse already returns, so no HTTP fetch beyond a normal browse. Indexing is
   lazy (first `search_registries` call) and self-healing (a corpus hash of the
   registry set — ids+urls+kinds — triggers a rebuild in `Search` when it
   changes); a `registries.OnSave` hook also rebuilds in the background after
   any web-UI/tool edit to `remote_registries.json`. Remote *content* drift
   (same URL, changed skills) is only caught by explicit `reindex_registries`.
5. **`search_docs` / `reindex_docs`** (helper) — semantic search over **omnis's
   own documentation** so the Helper can answer questions about omnis and quote
   the source ([internal/docindex/](internal/docindex/)). Indexes markdown across
   every doc root from `docindex.Roots()` — the web UI user docs (`web/docs` →
   `/usr/share/omnis/web/docs`) and the developer docs (`docs` →
   `/usr/share/doc/omnis/docs`), override with `OMNIS_DOCS_DIRS`. Mounted via the
   `docs` tool group alongside the always-on glob `list_docs` / `read_doc` /
   `grep_docs` (`NewNavTools`), which are the no-embedder fallback. Chunking is
   line-window + heading-aware and content-hash incremental; each hit carries the
   source `path`, `heading`, line range and the quoted `text`. Built/refreshed in
   the background at server startup ([server/docs_indexer.go](server/docs_indexer.go)
   `startDocsIndexer`): the incremental `Reindex` builds on first boot and after
   docs/embedder change, no-op otherwise. Backfill via `omnis reindex-docs`.

The embedder and all index handles are process-wide on `Infrastructure`
(`Embedder()`, `Precedents()`, `CodeIndex()`, `RegistryIndex()`, `DocIndex()` in [agent/embedder.go](agent/embedder.go)),
built lazily and surviving hot-reload. **Contract: when no embedder resolves,
none of the recall tools are mounted and every path falls back to glob/grep —
behaviour is byte-identical to a build without these features.** See
[agent/embedder.go](agent/embedder.go) `ResolveEmbedder` for the
`embed_model_ref` → `OMNIS_EMBED_*` precedence. The cached `Infrastructure.Embedder()`
accessor additionally **health-probes** a freshly-resolved embedder with one tiny
`Embed("ping")` (`probeEmbedder`, 15s timeout, background ctx): because
`embed.NewWithSelection` only *builds* the HTTP client and never contacts the
endpoint, a config that resolves cleanly but is rejected at request time (e.g. a
model id the gateway answers with HTTP 400) would otherwise only fail mid-session
on every recall call — the probe demotes it to nil so the same glob/grep fallback
applies. The probe runs once (memoised with the embedder), so a transient blip at
first use disables recall until restart; the explicit `omnis embed-test` /
`reindex-*` CLI commands bypass the probe and surface the real build/request error.

**Embedding dimension is the dominant cost lever.** go-turbovec builds, per
index, a `dim×dim` rotation matrix (Π) and a `dim×dim` QJL matrix (S) — `O(dim²)`
memory and `O(dim³)` to construct (Modified Gram-Schmidt QR). The matrices are
*not* stored on disk; only their seeds are, so each index reconstructs them on
load. At `dim=4096` (e.g. `qwen3-embedding-8b`) that is ~134 MB **per index** and
a multi-second QR; with docs+registries+precedents+code that is ~500 MB of RAM
and seconds of CPU. **Prefer an embedding `dim` in go-turbovec's design range
(~768–1536).** The OpenAI-compat embedder sends a `dimensions` request for any
pinned non-default `dim` ([core/embed/openai.go](core/embed/openai.go)), so a
Matryoshka model (qwen3 family, OpenAI `text-embedding-3-*`) can be truncated to
1024/768 purely by setting the model's `dim` in `models.json` (the embed cache
key includes `dim`, [core/embed/cache.go](core/embed/cache.go), so a dim change
never returns stale vectors). Changing `dim` (or the model) invalidates the
persisted index: [internal/semindex/](internal/semindex/) `Open` rebuilds when
the manifest model/dim differ, and docindex/codeindex drop their per-file hash
cache when the store comes up empty so every file is re-embedded.

Two mechanisms keep the matrix cost bounded:
- **Shared matrices.** Every semindex index is built with a fixed `Seed`
  (`indexSeed` in [internal/semindex/semindex.go](internal/semindex/semindex.go))
  instead of go-turbovec's default random seed, and go-turbovec memoises
  `rotation.New` / `quant.NewQJL` by `(dim, seed)` — so all same-dim indexes
  **share one Π and one S** (built/loaded once) rather than each allocating its
  own pair. (Requires go-turbovec ≥ the memoised build; omnis currently pins it
  via a local `replace` in `go.mod` — publish + bump for release.)
- **Deferred load.** `semindex.Open` reads only the cheap metadata sidecar and
  marks the `.tvim` as `pendingLoad`; the expensive `LoadIdMapFile` (the QR) is
  deferred to first real `Query`/`Upsert`/`Save` via `ensureLoadedLocked`, off
  the server-boot path. `Len()`/`Manifest()` answer from the persisted manifest
  without forcing the load, so an unchanged-corpus restart (docs Reindex is a
  no-op, registries `EnsureBuilt` is a no-op) never reconstructs a matrix — boot
  reaches `ListenAndServe` immediately and the QR happens lazily on first search.

### Configuration files

Config files are resolved through a **3-layer search chain** (high → low precedence):
`.agents/` (or `agents/` as a dotless alias; both participate when both exist, `.agents/` first) → `$HOME/.omnis/` (per-user) → `/etc/omnis/` (system). Agent and skill registries live under `registry/agents/` and `registry/skills/` inside whichever layer you're targeting.

| File | Purpose |
|---|---|
| `agents.json` | List of enabled agent names, squad composition, global paths, and `router_squad` (Omnis router squad; absent ⇒ `omnis`, `"none"` disables) |
| `models.json` | Providers (credentials + endpoint) and reusable model profiles referenced by agents via `model_ref`. Per-model `"disable_streaming": true` forces agents using that model onto the non-streaming endpoint (for backends whose streamed output misbehaves). Per-model `"prompt_cache": true` adds Anthropic `cache_control` breakpoints for an upstream LiteLLM proxy (see "Prompt caching via LiteLLM" below). Also: embedding models (`"embedding": true` + `"dim"`) and `"embed_model_ref"` selecting the internal embedder for semantic recall |
| `registry/agents/<name>/agent.json` | Per-agent definition (model_ref, tools, skills, builtin flag, etc.) |
| `registry/agents/<name>/instruction.md` | Per-agent system instruction (markdown) |
| `registry/agents/default.md` | Fallback system instruction for agents without their own |
| `registry/skills/<name>/SKILL.md` | Authored skill playbooks (YAML front matter: name, description) |
| `mcp_config.json` | MCP server definitions (name, command, args, env) |
| `a2a_config.json` | Remote A2A agent endpoints; each entry becomes an `a2a_<name>` tool on the leader |
| `permissions.json` | Tool permission rules in Claude Code nomenclature (`permissions.{allow,ask,deny}` + `defaultMode`); old `always_*` files auto-convert on load |
| `hooks.json` | Claude Code-style lifecycle hooks: shell commands fired on tool/prompt/session/compaction events (`hooks.{PreToolUse,PostToolUse,UserPromptSubmit,Stop,SubagentStop,SessionStart,SessionEnd,PreCompact,Notification}`). See "Lifecycle hooks" |
| `filters/` | Bash output filter patterns (token optimization, JSON files) |
| `softskills/` | Curator-distilled procedures from past sessions |

Agent definitions live in `registry/agents/<name>/` directories — mirroring
the skills layout. `agents.json` no longer contains inline agent
objects; its `agents` field is a list of names that reference the registry:

```json
{
  "agents": ["leader", "investigator", "web_agent", "skill_editor", "helper", "summariser", "curator"],
  "squads": [ ... ]
}
```

The `models` block lives in its own `models.json` file alongside `agents.json`.
A startup-time check rejects configs that still declare `models` inline in
`agents.json` — move the block to `models.json` (the loader points at the
expected path in the error message). The file holds two top-level sections:

```json
{
  "providers": {
    "openai-prod": {
      "kind": "openai_compat",
      "base_url": "OPENAI_BASE_URL",
      "api_key":  "OPENAI_API_KEY"
    }
  },
  "models": {
    "premium": {
      "provider_ref": "openai-prod",
      "model": "claude-sonnet-4-6",
      "context_length": 200000,
      "input_token_price_per_million": 5,
      "output_token_price_per_million": 26
    }
  }
}
```

A model's `provider_ref` inherits `kind` (as `provider`), `base_url`, and
`api_key` from the referenced provider; inline `provider`/`base_url`/`api_key`
on a model still override the inherited values when set.

**Embedding model selection (semantic recall).** A model entry may be flagged
`"embedding": true` (with an optional `"dim"`); such entries are *not* picked by
agents via `model_ref` — they show up in the Web UI Models panel's "internal
embedding model" selector and in nothing else. The top-level `models.json`
field `"embed_model_ref"` names which embedding model is the active internal
embedder for all semantic recall (soft-skills, precedents, codebase). It can be
overridden by `embed_model_ref` in `agents.json` and then by the
`OMNIS_EMBED_MODEL_REF` env var; when none resolves (and no `OMNIS_EMBED_*` env is
set) the embedder is absent and every recall feature silently falls back to its
glob/grep path. The embedder is process-wide (built once on `Infrastructure`,
survives hot-reload like the MCP pool); changing `embed_model_ref` needs a
server restart to take effect.

**Models editor auto-fill (web UI).** The Settings → Models panel can prefill
model fields from the provider instead of asking the user to type them. Two
server helper routes back this (both resolve credentials via `provider_ref` —
no secrets cross the wire — or explicit `provider`/`api_key`/`base_url`
overrides; see [server/provider_models.go](server/provider_models.go)):
`GET /api/providers/models` lists the provider's models (the model combobox's
⟳ button) and `GET /api/providers/embedding-dim?model=…` probes the embeddings
endpoint with one tiny request and returns the vector length, filling the DIM
field via the ⟳ button beside it ([web/settings.js](web/settings.js) `dimField`).
Dimension detection requires both a provider and a model id and reports the
model's native dimension.

**Server boots even with an unconfigured/unreachable model.** Missing model
credentials (no `OPENAI_BASE_URL` / `OPENAI_API_KEY`, etc.) no longer abort
server startup — the provider-health banner below is the user-facing signal
instead. `Options.DeferModelErrors` (set only by the server, [server/main.go](server/main.go))
makes `buildSquadInstance`'s `modelForAgent` closure use
`llm.NewDeferredWithSelection` ([core/llm/deferred.go](core/llm/deferred.go))
for the leader + every sub-agent: a **valid** selection still builds eagerly
(pure no-op), but one whose eager `NewWithSelection` fails (e.g.
`openai_compat requires OPENAI_BASE_URL`) returns a `deferredLLM` that
re-attempts the build at first `GenerateContent` and surfaces the real error
**there** — so the process starts, the web UI loads, and an actual turn (not
boot) is what fails. CLI/TUI leave `DeferModelErrors` false and keep failing
fast (a one-shot run is useless without a working model). Curator/reflector/
`ask_squad` model builds are unchanged (they already degrade gracefully on a
build error).

**Provider connection health (web UI).** On boot (and after every config
reload) the web UI probes model-provider connectivity via
`GET /api/providers/health` ([server/provider_models.go](server/provider_models.go)),
which resolves the live `models.json` catalogue and concurrently lists each
configured provider's models (`fetchProviderModels`, the same call backing the
combobox) with a 12 s per-provider timeout. It returns
`{ ok, providers:[{ref,kind,base_url,has_api_key,ok,error}] }` — `base_url` is
echoed for display (not a secret) and the API key value is **never** returned,
only `has_api_key`. When any provider fails, an orange warning banner
(`.provider-warn-banner`) is revealed **inside every chat pane** — above the
composer in an active chat and above the "Start a new chat" button in an
empty/draft pane (each pane carries both variants; the `.pane-picker` overlay
decides which is on screen, and `.editing`/`.terminal` tabs hide both). The
banners live in the pane template ([web/index.html](web/index.html)), are styled
in [web/css/features/dialogs.css](web/css/features/dialogs.css), and are toggled
per-pane by `renderProviderWarning`/`applyProviderWarning` ([web/app.js](web/app.js),
also re-applied in `attachPaneHandlers` so a split/new pane reflects the state).
Clicking a banner opens a popup (`checkProviderHealth`/`openProviderHealthModal`,
styled `.provider-health-*` in the same CSS partial) listing the unreachable
providers with editable base URL / API key fields. Each card has a
**Test connection** button that probes the *edited* values without saving via
`POST /api/providers/test` `{ref,kind,base_url,api_key}` → `{ok, model_count}` /
`{ok:false, error}` — any blank field falls back to the saved provider named by
`ref` (so a blank key tests the real stored credentials), and a POST body keeps
a typed key out of access logs. Saving GETs the
**raw** `models.json` (so env-var refs and untouched fields survive), patches the
failing providers' `base_url`/`api_key` (a blank key keeps the existing value),
`PUT`s it back via `/api/config/parsed/models`, then `POST`s `/api/config/reload`
and re-probes — so a reload that fixes (or breaks) a connection updates the icon.

The model list is metadata-aware for **LiteLLM** proxies (ChapsVision's gateways
are LiteLLM): `fetchOpenAIStyleModels` first tries `GET {base}/v1/model/info`
and, when present, maps each model's `model_info` — `max_input_tokens` →
`context_length`, `input/output/cache_read cost_per_token` → the per-million
prices, `output_vector_size` → `dim`, and `mode == "embedding"` → the embedding
flag. Selecting such a model in the combobox prefills all of these (without
overwriting fields the user already set) and re-renders the card. Plain OpenAI /
Ollama / vLLM endpoints (no `/model/info`) fall back to `GET /v1/models`, which
returns ids only.

Each `registry/agents/<name>/agent.json` is the full `AgentEntry`. A
`"builtin": true` flag marks agents shipped with omnis (leader,
skill_editor, helper, summariser, curator); custom agents added
by the user omit the flag. The web UI groups them under separate
**Built-in** and **Custom** sections in the agents list.

The registry directory uses the same 3-layer lookup as config files:
`.agents/registry/agents` (and `agents/registry/agents` when that alias dir
exists), `$HOME/.omnis/registry/agents`, then `/etc/omnis/registry/agents`, then
finally the **shared Agent-Skills registry** `/etc/agentskills/agents` —
first existing directory wins. (The registry subdirs sit one level below
their layer's config files: e.g. system has `/etc/omnis/agents.json` next
to `/etc/omnis/registry/agents/`.)

**Shared Agent-Skills registry (`/etc/agentskills`).** An extra,
**lowest-precedence, registry-only** layer (`paths.AgentSkillsDir`, default
`/etc/agentskills`, override/disable via `OMNIS_AGENTSKILLS_DIR`). When the
directory exists it is treated like a `/etc/omnis/registry` **root** — its
`agents/` and `skills/` subdirectories (note: no `registry/` prefix) contribute
agent and skill definitions — letting omnis pick up an Agent-Skills registry
installed by other tools. It sits **below** `/etc/omnis` in both the agent
(`agentsRegistrySearchDirs`) and skill (`SkillsAllSearchDirs` /
`skillsRegistrySearchDirs`) search chains, so omnis's own system registry wins on
name conflicts. It is **not** in `ConfigSearchDirs` (no `agents.json` etc. read
from there) and is **never written to** — items edited via the web UI fork into
the user layer, exactly like `/etc/omnis` (`paths.Layer` classifies it as
`system`). Absent ⇒ byte-identical no-op (consumers stat-and-skip).

### Prompt caching via LiteLLM (`prompt_cache`)

Anthropic prompt caching is a **prefix match keyed off explicit `cache_control`
breakpoints** — an un-annotated request never caches (Anthropic reports a 0% hit
rate). omnis reaches Anthropic through a **LiteLLM proxy** on the `openai_compat`
provider, so the native `core/llm/anthropic.go` adapter is bypassed; the request
is built by the OpenAI-compat adapter ([core/llm/openai.go](core/llm/openai.go)).

A per-model **`"prompt_cache": true`** flag in `models.json` opts that model into
caching (mirrors the `disable_streaming` plumbing exactly: `ModelEntry` →
`RuntimeModelConfig`/`RuntimeAgentConfig` → `llm.Selection.PromptCache` →
`applyModelPrefs` sets `openAI.promptCache`). When set, `buildRequest` calls
`markCacheablePrefix`, which places **ephemeral `cache_control` breakpoints** on
the request's **system message** (in Anthropic's `tools → system → messages`
render order this also caches the tool catalogue) **and** its **final message**
(incremental multi-turn reuse). LiteLLM accepts the annotation by reading
`cache_control` on a **structured content part** (the adapter converts the
string body to array form to carry it) and forwards it to Anthropic as the
native breakpoint. Two breakpoints, well within Anthropic's 4-breakpoint cap; a
sub-minimum/empty prefix is a **silent upstream no-op**, never an error
(`markMessageCacheable` also skips a content-less assistant/tool-call turn).

**Opt-in by design.** A plain OpenAI endpoint caches automatically server-side
and may reject the unrecognised `cache_control` field, so the flag defaults off
and only the `openai`/`openai_compat` adapters honour it (no effect on gemini or
the native anthropic adapter). Cache reads still surface in the cache-stats
plugin via the existing `usage.prompt_tokens_details.cached_tokens` mapping
([core/llm/openai.go](core/llm/openai.go) `cachedRead`), which LiteLLM populates
from Anthropic's `cache_read_input_tokens`. The web UI exposes it as a **Prompt
cache** toggle beside **Streaming** in Settings → Models. **No-op contract:**
with the flag off the request is byte-identical to before.

### Filesystem layout

Two roots, resolved by [internal/paths/paths.go](internal/paths/paths.go):

- **Read root for config**: a 3-layer search chain, high → low precedence.
  Whichever layer has a given file wins for that whole file (file-level
  override, not deep merge):

  1. `.agents/` (canonical) and/or `agents/` (dotless alias) — project-local
     directories (CWD-relative, highest priority). Both are accepted; when
     both exist, `.agents/` wins and `agents/` is searched right after.
  2. `$HOME/.omnis/` — per-user state root
  3. `/etc/omnis/` — system-wide install (lowest priority). Agent/skill
     registries live at `/etc/omnis/registry/agents` and
     `/etc/omnis/registry/skills`; every other config file is directly
     under `/etc/omnis/`.

  Plus one **registry-only** layer below all of the above, in the agent/skill
  search chains only (not for config files): `/etc/agentskills/`
  (`paths.AgentSkillsDir`, `OMNIS_AGENTSKILLS_DIR`) — a `/etc/omnis/registry`-shaped
  root (`agents/` + `skills/` subdirs) shared with other Agent-Skills tools. See
  the "Shared Agent-Skills registry" note above.

  Override the chain via `OMNIS_CONFIG_DIRS` (colon-separated; replaces
  the chain wholesale).

- **Write root for state**: `$HOME/.omnis/` by default (override via `OMNIS_HOME`).
  Agent runtime state (logs, mailboxes, softskills, registry installs) always
  lands here. For user-edited config (the web UI editor + the auto-install
  helpers), omnis is **layer-aware**: when the edited file or any of its
  references already lives in the project-local `.agents/` (or `agents/`)
  layer, the save is routed back to that layer so a local-only project
  never grows orphaned references under `$HOME/.omnis/`. Files originally
  in `/etc/omnis` still fork into `$HOME/.omnis/` on first edit (the system
  layer is read-only). Other state files (logs, mailboxes, softskills)
  remain anchored under `$HOME/.omnis/` regardless of layer:

  ```
  $HOME/.omnis/
  ├── agents.json       # editor writes — user config overrides
  ├── permissions.json  # editor writes — user permission overrides
  ├── logs/             # agent_tasks_*, agent_todo_*, agent_memory_*,
  │   │                 #   agent_statelog_*, agent_events_*, conversation_*
  │   └── uploads/      # web UI file uploads (per-session)
  ├── mailboxes/        # JSONL inter-agent mailboxes
  ├── index/            # semantic vector indexes (paths.IndexDir())
  │   ├── embed_cache/  #   content-hash embedding cache (sha256(model+text))
  │   ├── softskills.tvim + .meta.json   # recall_softskills index
  │   ├── precedents.tvim + .meta.json   # recall_precedents index
  │   ├── registries.tvim + .meta.json   # search_registries index (remote skills+agents)
  │   ├── docs.tvim + .meta.json         # search_docs index (omnis's own docs)
  │   │                 #   + docs.files.json (per-file hash→chunk-ids)
  │   └── <repo-hash>/  #   per-repo code index: codebase.tvim + .meta.json
  │                     #   + codebase.files.json (per-file hash→chunk-ids)
  ├── softskills/       # curator-distilled procedures (read AND write)
  │   ├── _stats.json   # per-skill load/helpful/harmful/neutral counters
  │   │                 #   sidecar; keyed by <agent>/<name> or bare <name>
  │   │                 #   for leader. Maintained by agent/load_recorder.go.
  │   └── wrap-session/ # built-in soft-skill (deletable) that asks one
  │                     #   wrap-up question on interactive surfaces and
  │                     #   persists the answer via record_session_feedback.
  ├── logs/
  │   └── agent_feedback_<key>.json  # Phase 5 wrap-session sidecar; one
  │                                  #   record per session: {question,
  │                                  #   answer, timestamp}. Consumed by
  │                                  #   the heuristic + LLM reflectors.
  └── registry/
      ├── skills/       # web UI installed skills (override via OMNIS_SKILLS_REGISTRY_DIR)
      └── agents/       # web UI installed agents (override via OMNIS_AGENTS_REGISTRY_DIR)
  ```

  The web UI editor reads from the search chain and writes to the same
  layer the source file lives in — local files stay local, user files
  stay user, and system files fork to user. For `agents.json` specifically,
  saves are promoted to the **local** layer when the file references any
  agent or skill that only resolves in `.agents/` (or `agents/`), so
  every reference remains satisfied after the write.

  The skill registry (`registry/skills/`) follows the same lookup as
  agent definitions: `.agents/registry/skills` (and `agents/registry/skills`
  when present), `$HOME/.omnis/registry/skills`, `/etc/omnis/registry/skills`
  — first existing directory wins. (The `registry/` sub-tree is the only
  thing that lives one level deeper than its layer's config files.)

### Configuration precedence

`defaults → agents.json → ENV → Options (struct/flags)`

`api_key` and `base_url` values in the config file are resolved as environment variable names first (if an env var with that name exists and is non-empty, its value is used).

### Environment variables

| Variable | Purpose |
|---|---|
| `OMNIS_PROVIDER` | `anthropic` / `openai` / `gemini` / `openai_compat` (default) |
| `OMNIS_MODEL` | Provider-specific model ID |
| `OMNIS_BASE_URL` | API endpoint (OpenAI/compat/Anthropic) |
| `OMNIS_API_KEY` | Provider API key (also: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`) |
| `OMNIS_ROUTER_SQUAD` | Overrides `router_squad` from `agents.json` — names the Omnis router squad; `"none"` disables routing (new chats then use the default squad) |
| `OMNIS_EMBED_PROVIDER` | Embedder provider for semantic recall (default: `OMNIS_PROVIDER`, else `openai_compat`). `anthropic` unsupported — use Voyage/OpenAI via `openai_compat` |
| `OMNIS_EMBED_MODEL` | Embedding model id (default `text-embedding-3-small`) |
| `OMNIS_EMBED_BASE_URL` | Embeddings endpoint (default `OMNIS_BASE_URL`/`OPENAI_BASE_URL`) |
| `OMNIS_EMBED_API_KEY` | Embedder API key (default `OMNIS_API_KEY`/provider key) |
| `OMNIS_EMBED_DIM` | Expected embedding dimension (default `1536`, or learned from the first response) |
| `OMNIS_EMBED_MODEL_REF` | Overrides `embed_model_ref` from `models.json` — selects which catalogue model is the internal embedder |
| `OMNIS_DOCS_DIRS` | Colon-separated documentation roots for `search_docs`/`list_docs`; replaces the auto-discovered set (`<webDir>/docs`, `/usr/share/omnis/web/docs`, `docs`, `/usr/share/doc/omnis/docs`) |
| `OMNIS_CURATOR_ENABLED` | `true`/`false` — enable/disable post-session curator |
| `OMNIS_CURATOR_IDLE_TIMEOUT` | Duration (e.g. `30m`) after which the idle harvester triggers automatic curation for a Web UI session; session is then marked **Harvested** and skipped until new activity; `0` disables (default: disabled) |
| `OMNIS_CURATOR_MIN_TURNS` | Minimum model-response count before non-forced curation is considered (default: `3`) |
| `OMNIS_CURATOR_MIN_SUB_AGENT_CALLS` | Minimum sub-agent invocations required when no decision is recorded (default: `2`) |
| `OMNIS_SERVER_TOKEN` | Bearer token required to start the HTTP server |
| `OMNIS_SERVER_ADDR` | HTTP server listen address (default `:8080`) |
| `OMNIS_SERVER_GC_INTERVAL` | Period between sweeps that remove orphan files in `$OMNIS_HOME/logs` and `$OMNIS_HOME/logs/uploads` (default `1h`; `0` disables) |
| `OMNIS_SERVER_DAEMONIZED` | Set to `1` by `omnis-server start` on the detached child it spawns; marks the foreground process as a background daemon (informational) |
| `OMNIS_TASK_NOTIFY` | `true`/`false` (default `true`) — server-mode **active wake** for completed background tasks/monitors: when on, a result injects a guarded synthetic turn the model reacts to; when off it only fires a UI toast (result still readable via `bg_output`). Either way the bg watcher drains the queue, so it never wedges |
| `OMNIS_HOME` | Per-user state root for all mutable files (default `$HOME/.omnis`) |
| `OMNIS_CONFIG_DIRS` | Colon-separated config search chain, high→low precedence. Replaces the default `.agents:$OMNIS_HOME:/etc/omnis` |
| `OMNIS_SYSTEM_CONFIG_DIR` | Overrides **only** the system layer (`paths.SystemConfigDir`, default `/etc/omnis`), leaving `.agents` and `$HOME/.omnis` in the chain — unlike `OMNIS_CONFIG_DIRS` which replaces the whole chain. Used by non-FHS package wrappers (Homebrew formula → `$(brew --prefix)/share/omnis`; Windows MSI → `C:\ProgramData\Omnis`; pip wheel launcher → the bundled `_dist/sysconf`) to relocate bundled config/registry without a rebuild |
| `OMNIS_AGENTSKILLS_DIR` | Relocates (or, set empty, **disables**) the shared Agent-Skills registry layer (`paths.AgentSkillsDir`, default `/etc/agentskills`) — a lowest-precedence, registry-only `/etc/omnis/registry`-shaped root (`agents/` + `skills/` subdirs) appended below `/etc/omnis` in the agent/skill search chains |
| `OMNIS_CONFIG_PATH` | Explicit `agents.json` path; bypasses the chain |
| `OMNIS_SKILLS_REGISTRY_DIR` | Where the web UI installs imported skills (default `$OMNIS_HOME/registry/skills`) |
| `OMNIS_AGENTS_REGISTRY_DIR` | Where the web UI installs imported agents (default `$OMNIS_HOME/registry/agents`) |
| `OMNIS_DEBUG` | Log full conversation/event payloads + per-stream SSE timing line |
| `OMNIS_LLM_STREAM_STALL_TIMEOUT` | Max idle gap between streamed chunks before the LLM read is aborted (Go duration, default `10m`; `0` disables). Guards against an upstream/gateway that streams partial text then goes silent without `[DONE]` or closing — otherwise the turn freezes "mid sentence" until the 5-minute client timeout. Applies to both the OpenAI/compat and Anthropic adapters ([core/llm/stall.go](core/llm/stall.go)). |
| `OMNIS_UPDATE_CHECK` | `true`/`false` (default `true`) — server-mode self-update poller that checks GitHub for a newer stable release. Auto-off for `dev` builds (no real version to compare). See "Self-update". |
| `OMNIS_UPDATE_INTERVAL` | How often the self-update poller re-checks GitHub (Go duration, default `6h`; clamped to ≥ `1m`). |

### Permission nomenclature (Claude Code-style) + grant scopes

omnis's permission format **is Claude Code's nomenclature**
([core/permissions/](core/permissions/)): `permissions.json` holds a
`permissions` object with `allow`/`ask`/`deny` tiers of `Tool(specifier)`
strings plus a `defaultMode`. Precedence is **deny → ask → allow** (first match
wins; deny always wins), implemented by `(*Config).CheckArgs`
([core/permissions/permissions.go](core/permissions/permissions.go)). Unmatched
calls fall through to the mode default (ask in `default` mode).

- **Rule syntax** ([spec.go](core/permissions/spec.go)): `Bash(npm run *)`,
  `Read(.env)`, `Edit(/src/**)`, `mcp__server__tool`, `Agent(Name)`, bare `Read`.
  Tool fan-out via `toolClasses`: `Read` rules also cover `Grep`/`Glob`/`mime`;
  `Edit` covers `Write`/`revert`. Bash gets full Claude parity in
  [match_bash.go](core/permissions/match_bash.go) (glob with space/`:*`
  word-boundary, compound-command splitting, wrapper stripping, built-in
  read-only allowlist); paths use gitignore anchors in
  [match_path.go](core/permissions/match_path.go).
- **Modes** (`defaultMode`): `default`/`acceptEdits`/`plan`/`dontAsk`/
  `bypassPermissions`/`auto` — applied in `CheckArgs` (bypass & plan are
  authoritative; the rest only change the unmatched-call default). The Bash
  safety floor in [core/tools/bash.go](core/tools/bash.go) is the independent
  circuit breaker, enforced even under `bypassPermissions` and the `!` escape.
- **omnis extensions over Claude syntax**: an object rule
  `{rule, reason, cwd}` attaches a prompt reason and a project-scoping `cwd`
  (rules with `cwd` only apply inside that tree — `cwdMatches`); the
  `{regex, tools}` form (or `/regex/` string) is the **raw-regexp escape hatch**
  matched against `toolName <json args>`, scoped to `tools`. The shipped safety
  floor uses it for catastrophic Bash patterns the glob syntax can't express,
  tagged `"tools": ["Bash"]` so a `Write` whose *content* merely mentions
  `mkfs` is never denied.
- **Old-format auto-upgrade**: a file with top-level
  `always_deny`/`always_allow`/`ask_user` is detected
  ([legacy.go](core/permissions/legacy.go)) and converted on load
  ([convert.go](core/permissions/convert.go) `ConvertLegacy` → regex-escape-hatch
  rules, byte-identical behavior), and the Reloader rewrites it in the new shape
  with a `.bak` backup ([reloader.go](core/permissions/reloader.go)
  `upgradeIfLegacy`). CLI: `omnis permissions convert|import`
  ([permissions_cmd.go](permissions_cmd.go); `import` ingests a Claude Code
  `settings.json`).

When a tool call needs confirmation the plugin calls the configured `Asker`
(server/TUI: [agent/permission_asker.go](agent/permission_asker.go) SSE widget;
CLI: `StdinAsker`). The user picks one of five scopes (`AskOutcome`):

| Choice | Outcome | Effect |
|---|---|---|
| Deny | `OutcomeDeny` | Reject this call; next identical call asks again. |
| Allow once (this call) | `OutcomeAllowOnce` | Cache the **exact (tool, args)** probe for the session. |
| Allow all `<Tool>` this session | `OutcomeAllowToolSession` | Cache a **per-tool** grant for the session — every later call of that tool auto-allows regardless of args. In memory only; never persisted. |
| Allow in this project | `OutcomeAllowProject` | Persist an `allow` rule with `cwd` = project dir. |
| Allow always | `OutcomeAllowAlways` | Persist an `allow` rule with no `cwd`. |

The session-approval cache ([core/permissions/cache.go](core/permissions/cache.go))
holds two granularities: per-call (`m`) and per-tool (`tools`); a per-tool
grant short-circuits before per-call. Both are wiped by `Forget(sessionID)`
on `EventSessionEnd`.

**Persisted-rule breadth** ([core/permissions/persist.go](core/permissions/persist.go)
`buildApprovalRule`) differs by tool: file tools (`Read`/`Write`/`Edit`/`revert`)
broaden to a bare class spec (`Edit`, `Read`, `Write`) so approving the first of
N file writes covers the rest — the `cwd` field still scopes "Allow in this
project". `Bash` keeps an **exact-command** match via the regex escape hatch (a
blanket persisted shell allow is a footgun; use the ephemeral "Allow all Bash
this session" grant for command bursts instead). The web UI Permissions form
([web/settings.js](web/settings.js) `renderPermissionsForm`/`renderPermRule`)
edits the `deny`/`ask`/`allow` tiers + `defaultMode`, preserving `cwd`/`tools`
on edited rules.

### Session isolation

Every mutable component scopes its state by `(userID, buildTimestamp)`. Concurrent sessions never share task graphs, todo lists, memory, or mailbox namespaces. All session files land in `$OMNIS_HOME/logs/`:

- `agent_tasks_<u>_<ts>.json` — task graph
- `agent_todo_<u>_<ts>.json` — todo plan
- `agent_memory_<u>_<ts>.md` — compressed session memory
- `agent_statelog_<u>_<ts>.json` — full state log (consumed by curator)
- `agent_events_<ts>.log` — event audit log (global per build)
- `conversation_<id>.json` — Web UI turn history + title + `squad` name + `Harvested` flag + `Archived` flag (server only)

### Session states (active / archived / deleted)

A session is in one of three states:

- **active** — present in the registry, listed in the sidebar, chattable.
- **archived** — present and **viewable read-only**, but detached from its agent
  generation. Set via `POST /api/sessions/:id/archive` (and reversed by
  `…/unarchive`). The `Archived` flag lives on both `SessionMeta`
  ([internal/sessions/sessions.go](internal/sessions/sessions.go)) and
  `ConversationFile` ([internal/sessions/history.go](internal/sessions/history.go),
  the durable source of truth); `Registry.SetArchived` mirrors the in-memory
  flag and persists it asynchronously via `SetConversationArchived`. Archiving
  calls `PushMgr.Stop` + `Manager.Release`; unarchiving re-`Pin`s and re-`Watch`es.
  The turn handler (`handleMessages` in [server/sse.go](server/sse.go)) rejects
  new turns on an archived session with `409 Conflict` (read-only guard); the TUI
  `send` path blocks them similarly.
- **deleted** — registry entry removed, conversation + agent log files
  hard-deleted (unchanged behaviour).

**GC retention invariant**: archived sessions stay in `Registry.List()`, so the
GC ([server/gc.go](server/gc.go) `activeFromRegistry`) treats them as live and
retains their files — keeping them available for semantic-recall indexing.

Both UI surfaces render archived sessions in a **collapsible panel above the
Settings button**: the Web UI `#archived-panel` ([web/index.html](web/index.html),
[web/app.js](web/app.js) `renderSessions`/`archiveSession`/`unarchiveSession`,
collapse state in `localStorage`), and the TUI `archivedPane` in the left column
([internal/tui/tui.go](internal/tui/tui.go), toggled with **Ctrl-A**; `a` archives
the highlighted session, `u` unarchives, `d` deletes). Viewing an archived session
disables the composer in both surfaces.

### Automatic session titling (Web UI)

A brand-new chat starts with a petname id (e.g. `teaching-kite`); on its **first
user turn** the server auto-derives a topic-bearing `Title` from the prompt, so
the sidebar/tab label stops reading as a random petname without a manual rename.
**Hybrid, two-tier** ([agent/session_title.go](agent/session_title.go),
hooked in [server/sse.go](server/sse.go) `handleMessages`):

- **Instant heuristic** — `agent.HeuristicTitle(prompt)` strips fenced code,
  collapses whitespace, and truncates to ≤60 chars on a word boundary (pure, no
  model call). Applied synchronously at turn start, then a `session_renamed`
  broadcast updates every open browser.
- **Async LLM refinement** — a background goroutine calls
  `Manager.GenerateTitle(ctx, sessionID, prompt)`, which issues **one
  non-streamed completion on the session's leader model** (the same one-off-LLM
  pattern as the routing capability probe in [agent/routing.go](agent/routing.go)
  — no runner/tools/event bus, so nothing reaches the SSE stream), 30 s timeout
  on `rootCtx`. When it returns a different title it overwrites the heuristic and
  re-broadcasts; any failure silently keeps the heuristic. The overwrite is a
  **compare-and-swap** (`Registry.SetTitleIf(id, heur, refined)`) against the
  heuristic title it set — so if the user **manually renames the session during
  the ≤30 s LLM call** (the common case: they rename the fresh chat right after
  the first question), the swap is a no-op and their title is left untouched
  (no disk write, no re-broadcast). Without this, the late LLM title clobbered
  the manual rename.

Gated to `meta.Title == "" && meta.Turns == 0` (read before the producer
goroutine's `Touch` increments the counter, under the run-guard), so a session
**manually renamed before its first turn** and a **continued** one are never
re-titled, and an attachment-only first turn (empty heuristic) keeps the petname.
A session renamed *after* the first turn started is protected by the
`SetTitleIf` CAS above. Writes go through
the existing `Registry.SetTitle` (in-memory) + `sessions.SetConversationTitle`
(persisted via the conversation lock, so they serialise with the turn's own
persistence). CLI/TUI are untouched (titling is server-only). **No-op contract:**
nothing changes for sessions that already have a title or have prior turns.

### Project memory (`AGENT.md`), `/init`, and `#`

Omnis's equivalent of Claude Code's `CLAUDE.md`. `AGENT.md` files are discovered,
concatenated, and **injected into the leader/root system instruction on every
turn**, resolved against the **session's working directory** (the same per-session
`bashCwd` the Folders panel / `!cd` mutate) — so multiple sessions rooted in
different folders each get their own project memory.

- **Discovery + injection** ([internal/agentmd/](internal/agentmd/)
  `Resolve(cwd)`): concatenates AGENT.md ascending by precedence — system
  (`/etc/omnis`, via `paths.SystemDir()`), user (`$OMNIS_HOME`), each `.agents/`
  (and `agents/`) layer, then the project walk-up from the git/repo root down to
  `cwd` (most specific last). Wrapped in a `<project-context source="AGENT.md">`
  container; per-cwd cache keyed by contributing files' size+mtime, so per-turn
  calls are cheap. Empty when no AGENT.md exists anywhere → **byte-identical
  no-op**. Injected by the `agentmd` plugin ([agent/agentmd_plugin.go](agent/agentmd_plugin.go)),
  a `BeforeModelCallback` registered in [agent/build_plugins.go](agent/build_plugins.go)
  that prepends the block to `req.Config.SystemInstruction`. cwd inside the
  callback comes from `fstools.CwdFor(ctx, ctx.SessionID())` (the context-carried
  `WithCwd` value, falling back to the session resolver; new export in
  [core/tools/cwd.go](core/tools/cwd.go)). Because the block is stable per project
  across turns, the system-prompt prefix cache still hits.
- **`/init`** — `agentmd.InitPrompt()` is a shared bootstrap instruction sent to
  the leader as a normal turn (the agent explores the repo and writes
  `AGENT.md`). Wired as a built-in on all three surfaces: web
  ([web/app.js](web/app.js) `handleSlashCommand` `case "init"`, fetching
  `GET /api/agentmd/init-prompt` for one source of truth), TUI
  ([internal/tui/tui.go](internal/tui/tui.go) `handleShortcut`), CLI
  ([internal/cli/cli.go](internal/cli/cli.go) `runRepl` + `runOneShot`). Reserved
  in `usercommands.ReservedNames` so user commands can't shadow it. The prompt
  has a **verify-then-refine** phase: after writing `AGENT.md` the leader
  delegates a fresh-eyes review to the **`agentmd_reviewer`** sub-agent (a
  read-only Default-squad member, [registry/agents/agentmd_reviewer/](registry/agents/agentmd_reviewer/)),
  which reads the document as a newcomer, follows it against the real repo, and
  reports blockers/should-fixes/nits answering "would a reader be misled?". The
  leader applies the recommendations and loops until the reviewer reports no
  blockers; the reviewer never edits the file (the creator does). The prompt
  falls back to a self-review pass when no reviewer sub-agent is mounted.
- **`#` shortcut** — a composer line starting with `#` appends a one-line memory
  to the **project** `AGENT.md` (git root from cwd, else cwd) via
  `agentmd.AppendMemory`, **not** sent to the agent (symmetric with `!`). Server
  routes `POST /api/sessions/:id/agentmd/append` + session-less `POST /api/agentmd/append`
  ([server/agentmd.go](server/agentmd.go), same token-only host-fs trust model as
  the `!` escape / Monaco save). Web `runHashMemory`, TUI `send` `#` branch, CLI
  `runRepl`/`runOneShot` all handle it locally.

### Lifecycle hooks (`hooks.json`, Claude Code-compatible)

User-configured **shell commands that fire at lifecycle moments** — omnis's port
of [Claude Code hooks](https://code.claude.com/docs/en/hooks-guide). Hooks let
users enforce policy *in code* (block edits to protected files, run a formatter
after every Write, inject context on every prompt) instead of relying on the
model to follow an instruction — the same guarantee the permission layer gives
for tool gating. The on-disk format matches Claude Code's `hooks` block verbatim,
so an existing Claude Code config is portable.

- **Config** = `hooks.json` resolved through the 3-layer search chain
  (`paths.FindConfig("hooks.json")`), shape
  `{ "hooks": { "<Event>": [ { "matcher": "<regex>", "hooks": [ { "type":
  "command", "command": "...", "timeout": N } ] } ] } }`. `matcher` is a tool-name
  regexp for PreToolUse/PostToolUse (empty/`"*"` = all), the trigger for
  PreCompact, the sub-agent name for SubagentStop, ignored otherwise. Resolved
  path is `RuntimeSettings.HooksConfigPath` (override `hooks_config_path` in
  `agents.json`).
- **Engine** = [internal/hooks/](internal/hooks/): `hooks.go` (config + `Match`),
  `run.go` (Claude Code stdin **input** JSON + stdout/exit-code **output**
  protocol — exit 2 = blocking error, stderr = reason; JSON `decision`/
  `hookSpecificOutput.permissionDecision` (`allow`/`deny`/`ask`)/`additionalContext`/
  `continue`/`systemMessage`), `reloader.go` (mtime-poll hot-reload + additive
  `Merge` across layers, mirroring [core/permissions/reloader.go](core/permissions/reloader.go)).
  Commands run via `fstools.RunShellCaptured` (new in [core/tools/bash.go](core/tools/bash.go))
  — the same process-group-isolated shell + safety floor as `RunBash`, but with
  stdout/stderr/exit-code separated for the control protocol. Like the `!`
  shell-escape, hooks bypass the permission layer (they're user-authored config)
  but the hard safety floor still applies.
- **Wiring** = one process-wide engine on `Infrastructure` (`Infrastructure.Hooks(runtime)`,
  [agent/hooks_plugin.go](agent/hooks_plugin.go)), built once (memoised `sync.Once`,
  survives hot-reload; the Reloader hot-reloads config). It splits two ways:
  - **Per-squad runner plugin** (`buildHooksPlugin`, appended in
    [agent/build_plugins.go](agent/build_plugins.go) `buildPlugins`) carries the
    **blocking/injecting** hooks: PreToolUse→`BeforeToolCallback` (a deny returns
    a `{"output":"[BLOCKED BY HOOK] …"}` map — the identical short-circuit to the
    permissions `DENY` path), PostToolUse→`AfterToolCallback` (a block appends the
    reason to the tool output), UserPromptSubmit→`OnUserMessageCallback` (ADK
    replaces the user message with the returned `*genai.Content`, so
    additionalContext is appended; a block returns an error that aborts the turn),
    Stop→`AfterRunCallback`. **The router squad mounts none** (`isRouterSquad`):
    the Omnis router only routes, so hooks fire on the answering squad's hop, not
    the router hop — this is why UserPromptSubmit fires exactly once per turn even
    though `buildPlugins` runs per squad.
  - **Fire-and-forget bus listeners** (`wireHookListeners`, wired **once** under the
    `Hooks` `sync.Once` so they don't multiply per squad): `EventSubAgentEnd`→
    SubagentStop, `EventSessionStart`→SessionStart, `EventSessionEnd`→SessionEnd,
    `EventCompressionStart`→PreCompact, `EventAskUser`→Notification (async so a
    permission prompt is never blocked by a slow hook). v1 ignores their outcome.
- **Server-mode session lifecycle**: web-UI sessions never emit the bus
  `EventSessionStart`/`EventSessionEnd` (those are CLI/TUI front-end signals, and
  `EventSessionEnd` also drives the reflection/curation pipeline web sessions skip).
  So the server fires those two hooks **directly** via `Infrastructure.FireHook`
  (bypassing the bus) — SessionStart on `POST /api/sessions`, SessionEnd on
  `DELETE /api/sessions/:id` ([server/server.go](server/server.go)), both async.
- **Web UI**: Settings → **Hooks** panel (`renderHooksForm` in
  [web/settings.js](web/settings.js), styled by [web/css/settings/hooks.css](web/css/settings/hooks.css))
  — an event-grouped editor (matcher cards + command/timeout rows) reusing the
  generic config-editor routes (`GET/PUT /api/config/parsed/hooks`); `hooks` is
  registered in the config name-map in [server/config.go](server/config.go). No
  new server routes.
- **No-op contract**: an absent/empty `hooks.json` mounts an inert engine and the
  behaviour is byte-identical to a build without hooks. **Limitations (v1):**
  Stop/SubagentStop hooks fire as notifications but cannot force-continue; PreCompact
  cannot rewrite the compaction instructions; SubagentStop/PreToolUse do not fire
  for a sub-agent's *internal* tool calls (sub-agents run in agenttool's private
  runner without the plugin — SubagentStop covers their completion); SessionEnd on
  CLI one-shot is best-effort.

### Interactive shell-escape (`!` commands)

A composer prompt that starts with `!` is a **shell-escape**: the rest of the
line runs directly on the host instead of going to the agent. It works in both
the TUI and the web UI and **bypasses the permission layer by design** (the
user typed the command explicitly) — but the hard safety floor in
[core/tools/bash.go](core/tools/bash.go) (`rm -rf /`, `mkfs`, fork bomb) still
blocks. Output is rendered live and is **not** added to the conversation /
LLM history (a convenience, like the todo widget).

- **Execution**: [core/tools/bash.go](core/tools/bash.go) `RunBashInteractive(ctx, command, cwd, timeoutSec)`
  reuses RunBash's safety floor, timeout, output filtering, and truncation, but
  takes a working directory and returns the directory **after** the command ran.
  The platform `wrapCaptureCwd` (bash_unix.go / bash_windows.go) appends a
  `__OMNIS_CWD__:` sentinel line carrying `pwd`; `extractCapturedCwd` strips it
  and reports the new dir, so an embedded `cd` **persists per session** across
  separate `!` commands (CWD only — not env vars or shell functions, since each
  call is a fresh shell). The Unix wrapper preserves the command's exit status;
  the cmd.exe wrapper does not.
- **CWD store**: per-session, in-memory, never persisted. TUI keeps a
  `map[sessionID]string` in [internal/tui/tui.go](internal/tui/tui.go); the
  server keeps the process-wide `bashCwd *bashCwdStore` in
  [server/bash.go](server/bash.go) (defaults to the process CWD; also used when
  no session id is supplied, e.g. completion from a draft tab).
- **Web routes** ([server/bash.go](server/bash.go), registered in
  [server/server.go](server/server.go)): `POST /api/sessions/:id/bash`
  `{command}` → `{output, dir}` (rejects archived sessions); `GET /api/complete?line=…&session=…`
  → `{start, candidates}`.
- **Completion**: bash-like, served by [internal/shellcomplete/](internal/shellcomplete/)
  (no shell subprocess). In the TUI it extends the existing `SetAutocompleteFunc`
  dropdown (the `!` branch shows just the completed leaf and splices it onto the
  preserved prefix). In the web UI the shared `#slash-menu` element is reused —
  `menuMode` (`"slash"`/`"bang"`) routes the keydown nav (Tab/Enter) and
  selection; `renderBangMenu`/`applyBangCompletion`/`runBangCommand` plus the
  `bash-block` renderer live in [web/app.js](web/app.js), styled in
  [web/css/styles.css](web/css/styles.css).

### Web UI Folders panel

A collapsible **Folders** panel in the sidebar (`#folders-panel`, directly above
`#archived-panel`, same look/feel — chevron + folder icon + section label,
collapsed by default, collapse state in `localStorage["agent_folders_collapsed"]`)
browses the **active session's working directory** — or, when **no session is
active** (a Monaco editor tab or an empty draft is showing), the **global default
environment** (see below). It reads and mutates the **same process-wide `bashCwd`
store** the `!cd` shell-escape uses, so navigating folders here is equivalent to
typing `!cd` (and vice-versa — a `!cd` refreshes the open panel, see
`runBangCommand`).

**Global "no session" environment.** `bashCwd` carries **two** process-wide dirs
(both initialised to the process cwd): a **fixed initial `root`** and a
**navigable global browse cwd `def`** (`getGlobal`/`setGlobal`). `get(id)` falls
back to **`root`** when a session has no stored cwd, so **a new (or un-navigated)
chat session always starts at the fixed initial root** — independent of where the
global Folders panel has browsed. The Folders panel picks its endpoint via
`folderApiBase()` ([web/app.js](web/app.js)): the session route when
`activeSessionId` is set, else the session-less `GET/POST /api/folder`
(`handleGlobalFolder`, which navigates `def`). So folder browsing — and
double-click-to-open-in-Monaco — keep working with no chat session, and browsing
that global panel **never** changes where new chats start. To start a session
rooted at a specific folder, use the Folders panel's right-click **"Open Chat
here"** (the `dir` field on `POST /api/sessions` → `bashCwd.set(meta.ID, dir)`);
see the context-menu bullet below.

**This cwd is also the agent's tool working directory** (see "Per-session tool
working directory" below): navigating the panel changes where the agent's
`Bash`/`Read`/`Write`/`Edit`/`Grep`/`Glob` operate, not just the `!` shell-escape.

- **Server** ([server/bash.go](server/bash.go) `handleFolder` /
  `handleGlobalFolder`, sharing `resolveFolderTarget` + `writeFolderListing`,
  registered in [server/server.go](server/server.go)):
  `GET /api/sessions/:id/folder` → `{dir, entries:[{name,dir}]}` lists the
  session's current cwd (dirs first, then files, case-insensitive alphabetical;
  symlinked dirs resolved via `os.Stat`). `GET …/folder?sub=<rel>` lists a
  sub-directory relative to the cwd **without mutating** it (the tree-expansion
  path — returns that sub-dir's `{dir, entries}`). `POST …/folder` `{path}`
  resolves `path` against the cwd (relative joined, absolute as-is, `..` walks
  up), validates it is a directory, calls `bashCwd.set`, and returns the new
  listing. The session-less `GET/POST /api/folder` mirror these against the
  global default cwd (`getGlobal`/`setGlobal`). Read-only host file access, same
  trust model as the `!` shell-escape and `GET /api/file`.
- **Upload to host** ([server/uploads.go](server/uploads.go) `handleFolderUpload`
  / `handleGlobalFolderUpload`, sharing `writeFolderUploads` + `safeJoinUnder`):
  `POST /api/sessions/:id/folder/upload` (and session-less `POST /api/folder/upload`)
  take a multipart form `files` and an optional `dest` sub-directory field, and
  **write the files directly onto the host filesystem** inside the Folders-panel
  cwd (or `dest` under it), recreating any folder structure (each file's relative
  path is carried in its multipart filename). Distinct from `handleFileUpload`
  (`POST …/files`), which stages chat attachments under `$OMNIS_HOME/logs/uploads`.
  `safeJoinUnder` rejects absolute paths and `../` escapes so an upload can never
  land outside the target dir. Same token-only trust model as the Monaco Save
  route — bypasses the agent permission layer. The web UI drives it from the
  Folders panel via **drag-and-drop** (`collectDropEntries`/`walkDropEntry`
  recurse dropped directories via the webkit entries API; dropping onto a folder
  row targets that sub-dir via its `li.dataset.rel`) and **Ctrl/Cmd+V paste**
  (gated by `foldersHover`, uploads `clipboardData.files`), both calling
  `uploadEntriesToFolder` → `folderUploadBase()`.
- **Copy/Paste on the host** ([server/uploads.go](server/uploads.go)
  `handleFolderCopy` / `handleGlobalFolderCopy`, sharing `doFolderCopy` +
  `copyPath`/`uniquePath`): `POST /api/sessions/:id/folder/copy` (and session-less
  `POST /api/folder/copy`) take `{src, dest}` and **copy a host file/dir** (`src`)
  into the destination directory (`dest`), both resolved against the cwd
  (`resolveAgainstCwd`). `copyPath` recurses directories and replicates symlinks;
  `uniquePath` auto-renames on collision ("… copy", "… copy 2"); a guard refuses
  copying a directory into its own subtree. Same token-only trust model as the
  upload route. The Folders-panel context menu drives it: **Copy** on any
  file/dir stores its abs path in the in-app `folderClipboard`
  (`setFolderClipboard`); **Paste** on a directory row (or "Paste here" on the
  path-header / empty-list context menu) calls `folderPasteInto`. Distinct from
  the existing **Copy path** item, which copies the path *string* to the OS
  clipboard.
- **Standard filesystem ops** ([server/folder_ops.go](server/folder_ops.go),
  registered in [server/server.go](server/server.go)): each has a session route
  and a session-less global route, sharing `sessionCwdOr404` + `resolveAgainstCwd`
  and the host-fs trust model:
  - `GET …/folder/download?path=` — `doFolderDownload` streams a file via
    `c.FileAttachment`, or a directory as an on-the-fly `archive/zip` stream
    (rooted at the dir's own name). The client (`folderDownload`) fetches with the
    auth header and saves the blob via an object-URL `<a download>`.
  - `POST …/folder/delete` `{path}` — `os.RemoveAll` (guards the cwd root itself).
  - `POST …/folder/new` `{dir,name,kind}` — creates an empty file or dir
    (`validLeafName` rejects separators / `.` / `..`; errors on collision).
  - `POST …/folder/rename` `{src,name}` — in-place rename (errors on collision).
  - `POST …/folder/move` `{src,dest}` — `movePath` (os.Rename with a
    copy-then-delete fallback across filesystems); `uniquePath` auto-renames on
    collision; refuses moving a dir into its own subtree.
  The Folders-panel context menus drive these via `folderDownload` / `folderDelete`
  (themed `uiConfirm`) / `folderNewEntry` / `folderRename` / `folderMoveTo` /
  `folderCopyTo` (themed `uiPrompt`), all funnelled through `runFolderOp` →
  `folderOpBase(op)`. **Cut** (`setFolderClipboard(…, "cut")`) + **Paste** performs
  a move (clipboard consumed); **Copy** + **Paste** a copy.
- **Generic themed modals** ([web/app.js](web/app.js) `uiPrompt`/`uiConfirm`,
  built on demand reusing the `.user-cmd-modal-*` classes + `.ui-modal*` styles):
  promise-returning prompt (string|null) and confirm (bool) dialogs with
  Enter/Escape/backdrop handling, used by the rename/new/move/copy-to/delete flows.
- **Context-menu grouping** — `openFolderCtxMenu` builds items grouped by kind
  (open/download · create/paste · clipboard · mutating ops · chat/save) with a
  `SEP` sentinel rendered by `showFolderCtxMenu` as a `.folder-ctx-sep` rule;
  leading/trailing/duplicate separators are dropped so conditional groups never
  leave stray rules. A menu item may carry an `opts` third element
  (`[label, action, {disabled|hidden}]`): `disabled` renders a greyed,
  click-inert `<button disabled>` (`.folder-ctx-item:disabled`), `hidden` omits it.
- **".." (parent) row menu** — `openFolderUpCtxMenu` gives the ".." row its own
  context menu. ".." is a navigable directory, so the **filesystem container**
  actions apply to the parent dir (`parentDirAbs()`): *Download*, *New File…*,
  *New Folder…*, *Paste*, *Copy path*. **Exception:** *Open Chat here* /
  *Open Terminal here* root at the **currently displayed** folder (`foldersDir`),
  **not** the parent — users read "here" as "the folder I'm looking at", and the
  parent surprised them by landing on the app root when they'd navigated just one
  level down. The **entry-targeting** actions that make no sense for ".."
  (*Cut*, *Copy*, *Rename…*, *Move to…*, *Copy to…*, *Delete*) are shown
  **greyed/disabled** rather than active.
- **Client** ([web/app.js](web/app.js), styled in [web/css/styles.css](web/css/styles.css)):
  `loadFolder(path)` GETs (no `path`) or POSTs (with `path`) and `renderFolder`
  paints the path header plus a `..` entry (hidden at filesystem root), then a
  **lazy expand/collapse tree** built by `buildFolderEntry(entry, rel)` (each
  entry's clickable `.folder-entry-row` div plus, for dirs, a nested
  `ul.folder-children`). Files render a **VS Code / Seti-style type icon** via `fileIconSvg(name)`:
  a recognised extension (`fileTypeInfo` → `FILE_TYPES`/`FILE_NAMES` maps —
  go/js/ts/html/css/json/py/rs/yaml/… plus whole-name cases like `go.mod`,
  `Dockerfile`, `.gitignore`) becomes a `.file-glyph` — a language-brand-coloured
  document glyph + short label on a **transparent** background; unknown types
  fall back to the neutral `currentColor` document icon (all icons share a 15 px
  square slot so names stay aligned). The glyphs use explicit `stroke`/`fill`, so
  they keep their brand colour through `:hover` and the `.copied` selection
  state (only the neutral icons tint with the row). Click discrimination is
  via `wireClickDblClick(el,
  single, double)` (a ~220 ms timer the `dblclick` cancels):
  **directory** — single click `toggleFolderExpand`s it in place (lazy-fetches
  children via `GET …/folder?sub=<rel>`, cached with `li.dataset.loaded`),
  double click `loadFolder(rel)` navigates into it (mutates cwd);
  **file** — single click does nothing, double click `openFileInEditor(rel)`
  opens the file in a **Monaco editor tab** (see "Web UI file editor" below).
  (`insertFileRef(rel)` — insert `@<rel>` into the composer — is still available
  via Ctrl/Cmd+C↔V below.) Each entry row is `tabindex="-1"`
  (focusable on click); **Ctrl/Cmd+C** on a focused row (file *or* directory)
  `copyFileRef`s its `@<rel>` to the system clipboard (`navigator.clipboard`
  with an `execCommand` fallback) and remembers it in `lastCopiedRef`. The
  copied row keeps a **persistent `.copied` highlight** (accent left-bar + soft
  bg; a one-shot `.flash` pulse layers on at the moment of copy) so the user can
  see which item is armed for pasting — only one row carries it at a time, and
  `markCopiedRow` re-applies it from `lastCopiedRef` when entries are rebuilt by
  a render or lazy expand. Pressing **Escape while the pointer is over the
  Folders panel** (`foldersHover` gate) `clearCopiedRef`s the selection. A
  **Ctrl/Cmd+V** in any pane's composer pastes
  natively, except when the clipboard exactly matches `lastCopiedRef` — then the
  composer's second `paste` listener inserts it space-padded via
  `insertRefIntoComposer` (shared with `insertFileRef`). `refreshFoldersPanel`
  reloads when the panel is open; called from `setFocusedPanel` (active-session
  change) and after a `!cd` mutates the cwd. The reload **preserves the expanded
  subtree** (it snapshots the expanded dir `rel`s, reloads, then re-expands the
  survivors shallowest-first) so an automatic refresh never collapses what the
  user opened.
- **Auto-sync on agent / shell file changes.** The panel reflects filesystem
  changes made *during* a turn without a manual reload. `scheduleFoldersRefresh`
  (debounced 250 ms, no-op when collapsed) drives it from three triggers in
  [web/app.js](web/app.js): (1) the **`file_changed`** SSE event — when its path
  is `pathUnderFoldersDir`, the panel live-refreshes (this is what makes a
  `/init`-created `AGENT.md`, or any agent `Write`/`Edit`/`revert`, appear); (2)
  the end of every turn (the SSE `finally`, gated to `activeSessionId`) as a
  catch-all for changes that surface no `file_changed` — notably folders
  created/removed via the **Bash** tool (`mkdir`/`rm`/`mv`); (3) any **`!` shell
  command** (`runBangCommand` now refreshes after every command, not just `!cd`)
  and the **`#` memory** append (`runHashMemory`). So agent writes, agent Bash fs
  ops, and user `!`/`#` actions all keep the panel current.
- **Right-click context menu** (`openFolderCtxMenu` → `#folder-ctx-menu`,
  body-appended `position:fixed` so it escapes panel overflow; dismissed on any
  click / right-click / scroll / Escape / blur / resize — the click+contextmenu+
  scroll listeners are **capture-phase** so an app handler that `stopPropagation`s
  its own click can't keep the menu open; a clicked item's action still runs
  because the click event is already in flight to the button). The render +
  positioning + `SEP`-separator grouping are shared via `showFolderCtxMenu(ev, items)`.
  Items adapt to the entry, grouped by kind (see "Standard filesystem ops" above
  for the op functions):
  **folder** → *Open Chat here* · *Download* (zip) │ *New File…* · *New Folder…* ·
  *Paste* (when clipboard set) │ *Cut* · *Copy* · *Copy path* │ *Rename…* ·
  *Move to…* · *Copy to…* · *Delete* │ *Add to chat editor* (session only).
  **file** → *Open* · *Download* │ *Cut* · *Copy* · *Copy path* │ *Rename…* ·
  *Move to…* · *Copy to…* · *Delete* │ *Add to chat editor* (session only) ·
  *Save* (when `editorDirty.get(abs)`). The **path header** and **empty list
  area** carry a `contextmenu` handler (`openFolderDirCtxMenu`) offering *New
  File…* · *New Folder…* · *Paste here* (when clipboard set) │ *Download folder* ·
  *Copy path*, all targeting the current `foldersDir`. The **".." row** has its
  own handler (`openFolderUpCtxMenu`, see "..(parent) row menu" above). *Copy path*
  (`writeClipboard(abs)`) copies the path *string* and is distinct from both the
  Ctrl/Cmd+C `@ref` copy and the filesystem *Cut*/*Copy*. Absolute paths come from
  `absForRel(rel)`; `writeClipboard` is the shared clipboard helper
  (`navigator.clipboard` + `execCommand` fallback).

### Per-session tool working directory

The agent's file-system tools (`Bash`, `Read`, `Write`, `Edit`, `Grep`, `Glob`,
`revert`, `mime`) run in the **session's working directory** — the same
per-session `bashCwd` the `!cd` shell-escape and the Folders panel mutate — not
the process working directory. The mechanism lives in
[core/tools/cwd.go](core/tools/cwd.go):

- A per-session resolver (`SetCwdResolver`, read via `sessionCwd`) maps a tool
  call's `ctx.SessionID()` to a directory; the server installs one backed by
  `bashCwd.get` in [server/main.go](server/main.go). Plus a context-carried cwd
  (`WithCwd` / `cwdFromContext`) that **takes precedence** and, unlike the
  resolver, **propagates into sub-agent runners** — agenttool creates a fresh
  session per sub-agent call (new id, parent `UserID`), so `ctx.SessionID()`
  there is *not* the web-UI session; the context value reaches it because
  `tool.Context` embeds `context.Context`. `handleMessages` plants it with
  `fstools.WithCwd(ctx, bashCwd.get(meta.ID))` before `Runner.Run`
  ([server/sse.go](server/sse.go)), so both the leader's direct file ops and any
  it delegates to the investigator share the chosen directory.
- The tool handlers in [core/tools/tools.go](core/tools/tools.go) apply it:
  `Bash`/`Grep` set `cmd.Dir` (via the schema-hidden `Cwd string `json:"-"``
  field on `BashIn`/`GrepIn`), `Glob` matches against it and reports matches
  **relative** to it, and the file tools resolve a relative `file_path` against
  it with `resolveAgainst`. Absolute paths are always honoured unchanged.
- **Default-preserving**: with no resolver/value (CLI/TUI one-shot) or a session
  that never navigated, `bashCwd.get` returns the process cwd, so resolution is a
  no-op and behaviour is byte-identical to before. The `Cwd` fields carry
  `json:"-"` so they never appear in the LLM-facing tool schema.
- **Permission scoping follows the session cwd.** The permissions plugin's
  `CWDFunc` now takes the tool context and resolves the cwd via the exported
  `fstools.CwdForContext(tc)` (same resolution as the tools), falling back to the
  process cwd ([agent/build_plugins.go](agent/build_plugins.go)). So an "Allow in
  this project" grant is scoped to the folder the session is *in* when granted,
  and `cwdMatches` (in [core/permissions/permissions.go](core/permissions/permissions.go))
  makes it apply to that directory **and its descendants but never its parents** —
  navigate deeper and the grant holds; navigate up out of the granted tree and it
  no longer applies. (Sub-agents run their tools in a separate runner without the
  permissions plugin, so this scoping is a leader-side concern.)

### `@file` references in the composer

A composer prompt may reference files with `@path` — an `@` at the **start of
the line or after whitespace** (so emails like `a@b.com` are not matched),
followed by a non-whitespace path token. The grammar and resolution live in
[internal/fileref/](internal/fileref/) (`Spans`/`Tokens`/`Classify`/`Resolve`/`Context`),
mirrored in JS for the web UI.

- **Context inlining**: at send time each surface resolves `@` references
  against the session's working dir and appends the content of every referenced
  **regular file** as an extra `genai.Part` on the user turn (capped 64 KB/file,
  20 refs). The raw prompt (with the `@token` intact) is what gets persisted to
  history — the inlined block is turn-only. Wired in [server/sse.go](server/sse.go)
  `handleMessages` (cwd from `bashCwd`), [internal/tui/tui.go](internal/tui/tui.go)
  `send`, and [internal/cli/cli.go](internal/cli/cli.go) `runTurn` (process cwd).
  Directories and missing paths are **not** inlined.
- **Completion**: path-only, via `shellcomplete.CompletePath`. TUI adds an `@`
  branch to `SetAutocompleteFunc` (completes the last token's path, splices onto
  the `@` prefix). Web reuses `#slash-menu` with `menuMode === "at"`
  (`atTokenAtCaret`/`renderAtMenu`/`applyAtCompletion`, served by
  `GET /api/complete-file?path=…&session=…` → `{candidates}`).
- **Rendering**: in the web user bubble and the floating pinned-prompt header
  (`renderUserText`), valid file refs render as `.file-ref` links (distinct
  colour) that open the file in a new tab via `GET /api/file?path=…&session=…`
  (auth'd blob fetch); valid dirs render as `.file-ref-dir` links (dir listing);
  invalid refs downgrade to plain text. Validity comes from the batch
  `POST /api/fileref/resolve` `{paths,session}` → `{kinds}`. The **composer**
  highlights refs live as you type via a backdrop overlay: the `<textarea>` text
  is transparent (`color: transparent`, visible caret) and a `.prompt-highlight`
  div behind it (`renderPromptHighlight`/`highlightRefsHTML`, per-panel kind
  cache + debounced `scheduleRefResolve`) re-renders the same text with coloured
  `.file-ref` spans; an `ime-composing` class shows the raw textarea text during
  IME pre-edit. The TUI colourises valid refs in the echoed turn (`colorizeFileRefs`).
  `GET /api/file` is read-only but trusts the authenticated user with host file
  access (same trust model as the `!` shell-escape and the Read tool).

### Background mailbox delivery

In **server mode** the leader's mailbox is drained in the background, not
polled by the model. [server/mailbox_push.go](server/mailbox_push.go)
`pushManager` runs one goroutine per session (via
[agent/infrastructure.go](agent/infrastructure.go) `WatchMailbox`); when a
cross-session message arrives it `inject`s a synthetic `"[mailbox] …"` turn
(serialised against user turns by `sessionRunGuard`) and fires the
`sessionPushBroadcaster` so open web UI tabs refresh.

Because the JSONL backend's `Receive` **consumes** the message (single
reader), the model must not also poll the same inbox. The server therefore
sets `Options.BackgroundMailboxDelivery = true`, which sets
`teammates.Agent.SuppressInboxPolling` on the leader and **omits the
`teammate_check` tool** from the leader's toolset. The leader instruction no
longer mandates a per-turn mailbox poll — incoming messages arrive as
injected turns instead. CLI/TUI leave the flag false (no background drainer),
so `teammate_check` stays as the leader's only delivery path there.
`teammate_ask/tell/list` are unaffected in both modes. (Note: `teammate_ask`
still reads replies from the leader's own inbox, so under background delivery
its reply can race the drainer — a known limitation, separate from the
per-turn `teammate_check` tax this removed.)

### Background task notifications (`bash_background` / `monitor`)

Completed background tasks deliver their result back into the conversation,
reusing the **same injection rail** as mailbox delivery (above). Two sources
feed one per-session [`bg.Queue`](internal/bg/bg.go) (`Infrastructure.BgQueues`):
`bash_background` (one-shot command, terminal `Notification`) and `monitor`
([internal/bg/monitor.go](internal/bg/monitor.go) — a long-lived command whose
stdout lines are matched against a `filter` regexp and emitted as streamed
`event` notifications, coalesced within ~200 ms). Every launch registers a
`Task` in the queue's registry ([tasks.go](internal/bg/tasks.go)) so
`bg_list`/`bg_cancel`/`bg_output` can see and control it (the `bg_` prefix
avoids colliding with the `planning` group's task-graph `task_list`). All five tools
mount via the **`bg` tool group** ([agent/squad.go](agent/squad.go)
`infra.BgQueues.Tools()`) on a squad root; `monitor` is governed by the same
`Bash(…)` permission rules + safety floor as `bash_background` (the alias in
[core/permissions/permissions.go](core/permissions/permissions.go) `CheckArgs`).

- **Server (active wake).** [agent/infrastructure.go](agent/infrastructure.go)
  `WatchBackground` mirrors `WatchMailbox`: a per-session goroutine `Wait`s on the
  queue, `Drain`s any burst, and hands the coalesced batch to
  [server/mailbox_push.go](server/mailbox_push.go) `pushManager.injectNotification`.
  `inject` was generalised to `injectTurn(…, sseEvent)` shared by both paths;
  the background path injects a guarded, persisted synthetic turn (so the model
  reacts) and fires a **`task_notification`** SSE via `broadcast`. The bg watcher
  starts inside `pushManager.Watch` alongside the mailbox watcher, so every
  watcher start site (main.go persisted-session loop, session create, unarchive,
  a2a auto-create) covers it with no extra wiring; `Stop` cancels both.
- **Passive mode.** `OMNIS_TASK_NOTIFY=false` demotes active wake: the watcher
  still drains (so the buffer never wedges — a latent bug in the old orphaned
  queue) but only fires the `task_notification` toast; the result stays readable
  via `bg_output`. The toggle is read once in [server/main.go](server/main.go)
  and passed to `newPushManager` as `activeWake`.
- **Web UI.** [web/app.js](web/app.js) `subscribeGlobalEvents` handles
  `task_notification`: it appends any injected turn (active mode) and calls
  `notifyTaskEvent` → an in-app toast (`#task-toast-layer`,
  [web/css/features/notifications.css](web/css/features/notifications.css)) plus an
  optional **OS notification** when backgrounded, gated by the unified
  desktop-notification preference (see "Desktop notifications" below).
- **CLI/TUI (between-turn drain).** No push goroutine there; instead `runTurn`
  ([internal/cli/cli.go](internal/cli/cli.go)) and the TUI send path
  ([internal/tui/tui.go](internal/tui/tui.go)) `Drain` the per-session queue
  before each turn and prepend `bg.FormatBatch(...)` as an extra user-turn part
  (mirroring `@file` inlining; off the router's prompt-only view). Both configs
  gained a nil-safe `BgQueues` field set from `infra.BgQueues`.

**No-op contract:** with no `bash_background`/`monitor` calls the queue stays
empty and behaviour is byte-identical to before; the recall/glob fallbacks are
untouched.

### Mid-turn steering (type while computing)

A user can send additional information, remarks, or insights **while a turn is
still being computed** — the Claude Code "steer the running turn" affordance.
Delivery is **hybrid**: a note is injected into the *running* turn at its next
model boundary when one is reached, and otherwise run as a follow-up turn — so a
note is never lost. Works on all three surfaces (web, TUI, CLI).

The whole mechanism hangs off one process-wide store and one plugin:

- **Steering store** ([internal/steer/](internal/steer/) `Store`, on
  `Infrastructure.SteerStore`, survives hot-reload like `BgQueues`). Per session:
  `pending` (queued, not yet shown to the model) and `consumed` (shown this turn,
  kept only to fold into the persisted prompt). `Enqueue` adds a note; **`Drain`**
  atomically moves pending→consumed and returns them (so a note is delivered to
  the model **exactly once**); `TakeConsumed` / `TakePending` are the turn-end
  drains; `Forget` clears a deleted session.
- **`BeforeModelCallback` steering callback** ([agent/steer_plugin.go](agent/steer_plugin.go)
  `injectSteeringCallback`). It fires before **every** model call, `Drain`s the
  session's pending notes, and **`injectSteering`** merges them as a user message —
  appended to the trailing `req.Contents` entry when that is already a `user` turn
  (the common case mid-tool-loop, where the last content carries the tool results),
  else a fresh user message. The merge avoids two consecutive user-role messages
  (strict providers like a native Anthropic adapter reject them — omnis reaches
  Anthropic via LiteLLM, which coalesces, but other adapters may not). It is
  mounted **two ways**:
  - On the **answering squad root** as a runner plugin via `steerPlugin`
    ([agent/build_plugins.go](agent/build_plugins.go) `buildPlugins`, gated
    `!isRouterSquad` — the router never answers, so it never steers).
  - On **every sub-agent** as an agent-level `BeforeModelCallback`
    (`subAgentSteerYield`, [agent/build_subagents.go](agent/build_subagents.go)) —
    runner plugins do **not** reach sub-agents (agenttool runs them in its own
    plugin-less runner), so the callback is attached to the sub-agent agent
    directly. **It does NOT consume the note** (see below).
- **Session-id propagation for sub-agents.** A sub-agent runs under a fresh,
  ephemeral agenttool session id, so it can't key the store by `ctx.SessionID()`.
  Each surface plants the **real** session id on the run context with
  `agent.WithSteerSession(ctx, id)` before `Runner.Run`; `steerSessionID(ctx)`
  reads it (the callback falls back to `ctx.SessionID()` when absent, which for the
  leader IS the real session). The value reaches sub-agents because agenttool
  passes the leader's tool context to its inner `runner.Run` — the same
  propagation path `WithCwd` uses.
- **The leader is the dispatcher (sub-agents yield, never consume).** While a
  sub-agent runs the **leader is parked** (blocked in the synchronous agenttool
  call), so the leader can't see a note until the sub-agent returns. To keep the
  *leader* in charge of every note, `subAgentSteerYield` **peeks** (PendingLen,
  no drain) and, when a note is pending, **short-circuits the sub-agent's next
  model call with a final text response** — a no-function-call response ends the
  agenttool run (confirmed against `Flow.Run`/`IsFinalResponse`), so the leader's
  tool call returns a `[Interrupted: … returning control to you …]` notice plus
  the sub-agent's partial work (`lastAssistantText`). The note stays **pending**.
  The leader's own callback then drains+injects it at its next model step, so the
  leader sees the interrupt notice **and** the note together and decides what to
  do: stop/redo the sub-agent, re-invoke it with the note folded in, handle it
  itself, or re-run it to finish (note irrelevant). `Drain` is atomic, so the note
  is delivered **exactly once** (to the leader), then folds into the turn's
  persisted prompt via `TakeConsumed`. Trade-off: interrupting aborts the
  sub-agent's **in-flight model call**; its *completed* steps survive when the
  sub-agent opts into `resumable_sessions` (the leader resumes via the returned
  `session` handle — see "Durable / re-attachable sub-agent sessions"), otherwise
  only its last assistant text is handed back for salvage and a re-task restarts
  from scratch.
- **Leader awareness (instruction).** `steeringAwarenessBlock()`
  ([agent/steer_plugin.go](agent/steer_plugin.go)) is appended to every non-router
  root instruction when steering is enabled: it tells the leader that IT (not the
  sub-agents) decides on each note, that a running sub-agent will hand control back
  with an interrupt notice, and enumerates the four choices above.
- **Surface loop (the fallback).** After each turn the surface folds
  `TakeConsumed` into that turn's persisted prompt (a `[Sent while working]`
  block, so a reload shows what the user added) and runs `TakePending` leftovers
  (notes no model call reached) as a follow-up turn, looping (cap
  `maxSteerFollowups`, 16).

Surfaces:

- **Web** — `POST /api/sessions/:id/steer {text}` ([server/sse.go](server/sse.go)
  `handleSteer`): enqueues and returns `{queued:true}` when a turn is live
  (`LiveTurns.get(id) != nil`), else `{queued:false}` so the client sends it as a
  normal turn. The turn producer in `handleMessages` is a **loop** (not a single
  `RunWithRouting`): fold consumed → persist → drain pending → emit a `steer_turn`
  SSE (the follow-up's user bubble, rendered in every attached stream) → run it →
  repeat; one terminal `done` at the very end. The run-guard is held across the
  whole loop so no other turn interleaves. Stop (`handleCancel`) also drops
  pending steering so a cancel doesn't leak notes into the next turn. Client
  ([web/app.js](web/app.js)): the composer stays enabled while streaming; an Enter
  submit during `sessionSending` routes to `steerMessage` (POST `/steer`), shows a
  pending **chip** in the per-pane `#steer-tray` (styled in
  [web/css/features/composer.css](web/css/features/composer.css)), and on
  `{queued:false}` falls back to `sendMessage`. The `steer_turn` SSE clears the
  tray + renders the note as a user bubble; the send-path `finally` clears any
  remaining chips. i18n key `app.steer.queued`.
- **TUI** ([internal/tui/tui.go](internal/tui/tui.go)) — while `busy`, a plain
  Enter `Enqueue`s on `cfg.SteerStore` and echoes "steering queued"; the send
  goroutine loops the same fold/drain/follow-up after `RunWithRouting`.
- **CLI** ([internal/cli/cli.go](internal/cli/cli.go)) — the REPL now reads stdin
  on a **persistent background goroutine** (a `lines` channel) instead of blocking
  per line, so the user can type *during* a turn (the turn runs in its own
  goroutine with a `turnDone` channel); a line typed while a turn is in flight is
  enqueued as steering, otherwise it starts a turn. `runTurnSteering` wraps the
  fold/drain/follow-up loop. Ctrl-C still cancels just the running turn; Ctrl-D
  mid-turn exits once it finishes.

All three configs gained a **nil-safe** `SteerStore` field set from
`infra.SteerStore`. **No-op contract:** with nothing enqueued the store is empty,
`Drain`/`TakePending` return nil, every surface loops exactly once, and behaviour
is byte-identical to before.

### Desktop notifications (unified preference)

A **single** desktop-notification preference gates two web-UI signals: a finished
**background task / monitor** (`notifyTaskEvent`, above) **and** a finished **chat
reply** while the user is away (`notifyChatReply`). Both live in
[web/app.js](web/app.js) and only fire when the user is **away** —
`document.hidden || !document.hasFocus()` for tasks; for chat replies that **plus**
`panelsForSession(sessionId).length === 0` (the finished session isn't the active
tab in any visible pane, i.e. the user switched to another session). `notifyChatReply`
is called from **two** places: the send-path `finally` on `outcome === "done" ||
"reload"` (the initiating tab, immediate, rich live preview), **and** the
persistent `/api/events` global stream's **`chat_reply`** event (robust to a
backgrounded tab whose per-turn POST/SSE stream the browser suspended — the same
reliable channel `task_notification` uses, and the reason chat notifications fire
even when the sending tab is in the background). The notification's **title is the
session/chat name** (`paneTabTitle`) and its **body is the first few lines of the
reply** as a **multi-line** snippet — `notificationPreview` strips markdown and
keeps ≤4 non-empty lines joined with `\n` (length-capped), so the OS renders a
multi-line notification rather than one collapsed line. (The browser still appends
its own immutable origin source line below — web code cannot rename or suppress
it.) The two calls are **de-duplicated** so only **one** notification fires per
completed turn: the send-path fires first (the `done` SSE is emitted *before* the
server persists + broadcasts `chat_reply`) and carries the **final-answer** text
(`lastReplyText` = the last finalized segment, since a `tool_call` finalizes the
current segment), so it wins; the global `chat_reply` — which previews the **full**
turn text, whose first lines may be the leader's "handing off to `<sub-agent>`"
narration — is suppressed for that turn. The guard is an 8 s per-session window in
`sessionNotifiedAt` (sessionId → last-fired ms; cleared in `forgetSession`); the
shared `tag` (`omnis-chat-<sessionId>`) is a second-line coalesce. The global path
remains the fallback when the initiating tab was backgrounded and its send-path
`finally` didn't fire promptly. The away-check makes both no-ops when the user is
present. `stopped`/`error`/`exhausted` don't ping. `notifyTaskEvent` mirrors the
title (session name) convention.

The `chat_reply` event is broadcast server-side from the turn producer in
[server/sse.go](server/sse.go) `handleMessages` after the reply is persisted
(`d.PushEvents.broadcastWithText("chat_reply", id, replyNotificationPreview(text))`);
`replyNotificationPreview` is the server-side markdown→snippet reducer mirroring
the client's (also multi-line: ≤4 lines joined with `\n`). `pushMsg` carries an optional `Text` payload
([server/mailbox_push.go](server/mailbox_push.go) `broadcastWithText`), serialised
as the SSE data field's `text` ([server/server.go](server/server.go) `/api/events`).

- **Source of truth.** The durable choice is the server-side **`preferences.json`**
  ([server/preferences.go](server/preferences.go), `preferences.Notifications *bool`,
  under `paths.ConfigWriteDir()` = the user's home), exposed via `GET/PUT
  /api/preferences`. The PUT **merges** (load → `ShouldBindJSON` onto the loaded
  struct → save) so a theme-only PUT no longer wipes notifications and vice-versa.
  `localStorage["agent_toolkit_os_notify"]` is a **synchronous read-cache** the fire
  path reads; [web/settings.js](web/settings.js) `syncThemeFromServer` seeds it from
  the server prefs on boot and `saveNotifications(enabled)` writes both.
- **First-run opt-in.** When `preferences.json` has **no** `notifications` value
  (pointer is nil ⇒ omitted from JSON), `maybePromptNotifications` (run once at the
  end of `init()`, awaiting `window.Settings.prefsReady`) shows a `uiConfirm` opt-in
  (via the shared `offerNotificationGrant` helper — the confirm click is also the
  user gesture the native permission prompt needs), requests browser permission, and
  persists the answer — so it's asked **once per home directory**, never again. The
  Settings → Appearance toggle changes it later.
- **Boot-time intent⇄grant reconciliation.** The server-side intent and the
  per-browser `Notification.permission` are independent: clearing a site's data /
  permissions resets the browser grant to `"default"` (or it may be `"denied"`)
  while `preferences.json` still says **enabled** — leaving omnis "on" while the
  browser silently drops every notification, with no message explaining why. So when
  a recorded intent is `true` but the permission is **not** `granted`,
  `maybePromptNotifications` reconciles at startup: `"default"` ⇒ re-offer the grant
  (`offerNotificationGrant`), `"denied"` ⇒ show `showNotificationBlockedHelp`. Guarded
  by a once-per-tab-session `sessionStorage["agent_toolkit_notify_resynced"]` flag so
  a dismissed re-offer or a hard block never re-prompts on every navigation.
- **Permission is per-browser; intent is per-user.** The file records intent; the
  browser still owns the grant, so a second browser may need permission re-granted
  (via the Settings toggle). A website **cannot** grant its own notification
  permission — `Notification.requestPermission()` only shows the native prompt
  while the state is `"default"`; once `"denied"` it's a silent no-op. So opting in
  while the browser is blocking **keeps intent on** (it fires the moment the user
  unblocks it — the fire path re-checks `Notification.permission === "granted"`)
  and shows browser-specific guidance (`requestDesktopNotifications` +
  `showNotificationBlockedHelp`/`notificationUnblockHint` in [web/app.js](web/app.js)).
  An active "Block" in the just-shown native prompt (`default → denied`) is
  respected (intent saved off). **No-op contract:** with notifications
  off/unsupported every path returns early, byte-identical to before.

### Cross-browser session sync (`/api/events`)

The single multiplexed SSE stream `GET /api/events` ([server/server.go](server/server.go))
that every browser opens once via `subscribeGlobalEvents` ([web/app.js](web/app.js))
carries, besides `mailbox_push` / `ask_user` / `ask_user_cancel`, three
**session-list** events so two browsers viewing the same server stay in sync:
`session_created`, `session_deleted`, and `session_renamed`. Each is emitted by
`sessionPushBroadcaster.broadcast(event, sid)` ([server/mailbox_push.go](server/mailbox_push.go))
from the `POST /sessions`, `DELETE /sessions/:id`, and `PATCH /sessions/:id`
handlers respectively. The multiplexed `all` channel carries a
`pushMsg{Event,SID,Text}` (Text is an optional payload, e.g. the `chat_reply`
reply preview — see "Desktop notifications"; the legacy per-session `subs` channel
still only understands `mailbox_push`, so `notify` fans out to both while
`broadcast`/`broadcastWithText` touch only `all`). The client
handles `session_created`/`session_renamed` with a sidebar-only `loadSessions()`
and `session_deleted` with `forgetSession(sid)` (drops per-session maps + ask
widgets + `closeTabEverywhere`) then `loadSessions()`. All three handlers are
idempotent, so the originating browser harmlessly processes its own echoed
broadcast. New sessions are **never auto-opened** on other browsers — only listed.
A fourth event, **`session_rewound`**, is emitted by the rewind handler (see
"Conversation fork & rewind" below); the client re-renders the truncated
transcript from history for any pane showing that session. A fifth event,
**`chat_reply`** (carrying the session id + a reply-preview `text`), fires on every
completed user turn and drives away-from-session OS notifications (see "Desktop
notifications"); it does not change the transcript. A sixth event,
**`update_available`** (no session id), is fired once by the self-update poller
when a newer stable release is found; the client re-runs `checkForUpdate()` to
reveal the sidebar button (see "Self-update").

### Self-update (new-release detection + in-app install)

The running **omnis-server** periodically checks GitHub for a newer **stable**
release and surfaces a blue **Update** button next to the "Omnis" title in the
sidebar header; clicking it installs the new package for the channel omnis was
installed through, with a manual-steps fallback. Server-only (CLI/TUI never poll
or install). The logic lives in [internal/selfupdate/](internal/selfupdate/) and
the server wiring + routes in [server/selfupdate.go](server/selfupdate.go).

- **Version awareness.** [server/main.go](server/main.go) now declares
  `version`/`commit`/`date` ldflags vars (the Makefile/goreleaser already passed
  `-X main.version` to `./server` — it was silently dropped before). A `dev`
  version disables the check entirely (developers are never nagged).
- **Detection.** `selfupdate.DetectMethod(os.Executable())` classifies the
  install channel at runtime: pip (exe under `…/omnis/_dist/` or `site-packages/`),
  brew (darwin, under `brew --prefix`/`/Cellar/omnis/`), deb (`dpkg-query -S`
  succeeds), rpm (`rpm -qf` succeeds), msi (windows, under Program Files), else
  `raw`/`unknown`.
- **Check.** `CheckLatest(ctx, Repo, version)` GETs
  `https://api.github.com/repos/blouargant/omnis/releases/latest` (the `/latest`
  endpoint already excludes drafts/prereleases ⇒ stable only), `semverNewer`
  gates availability (non-semver/`dev` current ⇒ never available), and `assetFor`
  picks the matching `.deb`/`.rpm`/`.msi` asset by GOOS/GOARCH. brew/pip need no
  asset (package-manager install). The result is cached in `updateState` on
  `serverDeps`.
- **Poller.** `startUpdatePoller` runs a goroutine (15 s after boot, then every
  `OMNIS_UPDATE_INTERVAL`, default `6h`, gated by `OMNIS_UPDATE_CHECK`) that
  re-checks and, the first time an update appears, fires the `update_available`
  SSE via `PushEvents.broadcast` so open tabs light the button without polling.
- **Routes** (auth group, registered in [server/server.go](server/server.go)):
  `GET /api/update/status` (cached `{current, latest, available, method,
  asset_name, manual_steps, release_url}`, never the password),
  `POST /api/update/check` (force a re-check), and `POST /api/update/install`
  `{password?}` → runs `selfupdate.Install` (deb: `sudo -S apt-get install`,
  rpm: `sudo -S dnf install`, brew: `brew upgrade omnis`, pip: `pip install -U
  omnis-agent`, msi: `msiexec /i`). On success `{ok:true, restart_required:true}`;
  on failure `{ok:false, error, manual_steps}`. The password (deb/rpm only) is
  read from the body, piped to `sudo -S`, and never logged/persisted — same
  token-gated host-trust model as the terminal / Monaco-save / `!` routes.
- **Web UI** ([web/app.js](web/app.js)): `checkForUpdate` reads the cached status
  on boot (and on the `update_available` SSE) and reveals `#update-btn`;
  `openUpdateDialog` shows `current → latest`, the detected method, a sudo
  password field for deb/rpm, an **Install** button, and a manual-steps fallback.
  After a successful install it offers **Restart now** → `restartServerAndReload`
  (the existing `POST /api/server/restart` re-exec picks up the new binary).
  Styles in [web/css/features/sidebar.css](web/css/features/sidebar.css)
  (`.update-btn`) and [web/css/features/dialogs.css](web/css/features/dialogs.css)
  (`.update-*`).
- **No-op contract:** a `dev` build or `OMNIS_UPDATE_CHECK=false` runs no poller,
  shows no button, and is byte-identical to before.

### Conversation fork & rewind (Web UI)

Each user turn in the transcript carries a hover **↺ control** (top-right of the
bubble) opening a 2-item menu: **Fork conversation from here** / **Rewind
conversation to here**. Both branch the conversation at a turn; there is **no**
code/action revert. Semantics: the control names a turn index `K`; the cut is
**"to before turn K"** — keep turns `[0, K)`, drop `K…end`.

A conversation lives in **two layers**, both handled:
- **Display / persistence** — `conversation_<id>.json`
  ([internal/sessions/history.go](internal/sessions/history.go)): `TruncateConversationTurns`
  (rewind, in-place atomic truncate) and `ForkConversation` (copy first `K` turns
  into a new file, inheriting the source `Squad`). `Registry.SetTurns` fixes the
  sidebar counter (Touch only increments).
- **Model context** — the ADK runner's in-memory `session.InMemoryService` (what
  the model "remembers"). `Manager.ReseedSessionContext` ([agent/session_reseed.go](agent/session_reseed.go))
  rebuilds it from the kept turns: `Delete`→`Create`→`AppendEvent` one `user` +
  one `model` `genai.Content` event per turn, on the session's **active squad**
  (always) plus any other squad already holding an in-memory session for the id
  (routing-visited). The reconstruction is **text-only** — tool calls /
  attachments from old turns are not replayed (same fidelity as a restart or
  compression). `agent` can't import `internal/sessions` (cycle), so callers map
  `ConversationTurn` → the local `agent.Exchange` pair type.

**Routes** ([server/fork_rewind.go](server/fork_rewind.go), registered in
[server/server.go](server/server.go)): `POST /api/sessions/:id/rewind`
`{turn_index}` → `{turns}` + a `session_rewound` broadcast; `POST
/api/sessions/:id/fork` `{turn_index, title?}` → `{session_id, squad,
dropped_user_text}` and mirrors the `POST /sessions` wiring (RegisterSession,
Pin, PushMgr.Watch, `session_created` broadcast, SessionStart hook, inherits the
source `bashCwd`). Both reject **archived** (rewind only; forking an archived
source is allowed since it never mutates the source) and refuse to run while a
turn is in flight (`RunGuard.tryAcquire` → 409). A reseed failure is logged but
never corrupts the already-truncated display history.

**Web UI** ([web/app.js](web/app.js)): `appendUserBubble` takes a `turnIndex` and
stamps it on the row (`addTurnActions` adds the control); each render site
(`rerenderSessionFromHistory`, `appendNewPushTurns`, the live send path, the
initial history load) passes the true turn index, so the cut point stays exact
even with non-rendered mailbox/background turns. `forkConversation` /
`rewindConversation` POST then re-render and **pre-fill the composer** with the
dropped user message (only when empty) for edit-and-resend; `rewindConversation`
gates on a `uiConfirm`. Styles: `.turn-action-btn` / `.turn-menu` in
[web/css/features/messages.css](web/css/features/messages.css). CLI/TUI are
untouched (no routes, no reseed callers) — byte-identical no-op there.

### Background server (`omnis-server start` / `stop` / `status`)

`omnis-server` runs in the **foreground by default** (`omnis-server [flags]`).
Three subcommands, dispatched in `main()` before `run()`'s flag parsing
([server/main.go](server/main.go)) and orchestrated in
[server/daemon.go](server/daemon.go), add a background-daemon lifecycle:

- **`start [flags]`** re-execs the binary **detached** (`os/exec` +
  `SysProcAttr{Setsid: true}` on unix, so the child is its own session leader
  and survives the parent exiting + the terminal closing), redirects its
  stdout/stderr to `$OMNIS_HOME/logs/omnis-server.log`, writes the child PID to
  `$OMNIS_HOME/omnis-server.pid`, then returns — freeing the terminal handle. The
  child runs the **same foreground path**, so it **opens a browser exactly like
  plain `omnis-server`** does (per `server.yaml`'s `open_browser`), pointing at
  the *actually bound* address after any port auto-increment — `openBrowser` is
  fire-and-forget and only needs `DISPLAY`/`WAYLAND_DISPLAY` (inherited), not a
  terminal; pass `omnis-server start --no-browser` to suppress it. `start`
  inherits the CWD (config search `.agents` and the default `web` dir are
  CWD-relative, so the child must start where `start` ran), sets
  `OMNIS_SERVER_DAEMONIZED=1` on the child, refuses to start when a live PID is
  already recorded, and does a ~400 ms liveness grace check to surface an
  immediate failure (e.g. port already in use) instead of falsely reporting
  success.
- **`stop`** sends `SIGTERM` to the recorded PID (the server already traps it
  via `signal.NotifyContext` for a graceful shutdown), waits up to 15 s for it
  to exit, then removes the PID file. Missing/stale PID files are handled
  gracefully (idempotent).
- **`status`** reports running/stopped based on the PID file + a liveness probe
  (`kill -0`).

The detached child runs the **same foreground `run()` path** — there is no
separate daemon code path. Platform support mirrors `restart_other.go`: the
primitives (`detachSysProcAttr`, `pidAlive`, `signalTerminate`, and the
`daemonSupported` const) live in [server/daemon_unix.go](server/daemon_unix.go)
(real) and [server/daemon_other.go](server/daemon_other.go) (`!unix` stub that
returns `errDaemonUnsupported` so cross-platform builds stay green). Stale PID
files are always reconciled via the liveness probe, so a crash/self-exit never
wedges `start`/`status`.

### Hot reload (server mode)

The HTTP server supports rebuilding the agent generation without
restarting the process. Edits to `agents.json`, `models.json`,
`permissions.json`, and `mcp_config.json` (from any layer of the search
chain) are picked up by `POST /api/config/reload` (or the "Reload" button
in the web UI).

The model is a two-layer build split across [agent/infrastructure.go](agent/infrastructure.go),
[agent/instance.go](agent/instance.go), and [agent/manager.go](agent/manager.go):

- **Infrastructure** is process-wide and survives every reload: mailbox
  backend, session registry, event bus, ask_user registry, MCP subprocess
  pool, and the session-scoped state holders (tasks, todo, bg queues).
- **Instance** is one agent generation: a map of **SquadInstance** entries
  (leader + sub-agents + plugins + runner per squad) derived from a
  snapshot of RuntimeSettings. Each reload bumps the generation number
  and builds a fresh Instance — with every squad rewired — on top of the
  unchanged Infrastructure. The default squad's leader/runner/plugins
  are mirrored at the top of Instance so legacy callers (CLI, TUI,
  examples) keep working unchanged.
- **Manager** owns the live generations. New sessions pin to the current
  generation and record their squad on the session; the server resolves
  `Manager.LookupSquad(sessionID, squadName).Runner` per turn. In-flight
  sessions stay pinned to their existing generation across reloads, so a
  streaming turn never observes a swap. An old generation is torn down
  once its pinned-session refcount drops to zero.

MCP subprocesses are deduplicated by `(command, args, env)` hash via
[internal/mcp/pool.go](internal/mcp/pool.go): two generations that mount
the same server share one child process. A reload that only changes one
server restarts just that server.

`GET /api/config/status` exposes the current generation and per-generation
refcounts so the web UI can render a "n sessions draining on previous
version" pill.

The Settings editor's post-save banner ([web/settings.js](web/settings.js))
picks **one** action by mode, decided at save time:
- **reload** (default) — offers only **Reload** (hot-reload, no downtime).
- **restart** — offers only **Restart server**. Entered when a save changes
  the **embedder identity** in `models.json` (the `embed_model_ref`, or the
  referenced model's id/dim/provider connection — see `embedderFingerprint`),
  because the embedder is process-wide on `Infrastructure` and survives
  hot-reload, so only a full restart applies it. The mode is a sticky
  `localStorage` flag (`agent_toolkit_restart_required`) so a later
  hot-reloadable save can't downgrade a still-pending embedder restart back to
  Reload; it clears only on an actual Reload/Restart. The "Restart server"
  option is therefore **proposed only when an embedder change is pending** —
  every other edit shows Reload. (Restart remains the conceptual escape hatch
  for env/binary updates, but those are applied out-of-band, not via this
  banner.)

### Adding a new sub-agent

1. Create `.agents/registry/agents/<name>/agent.json` with the `AgentEntry` fields
   (`name`, `description`, `tools`, optional `model_ref`, etc.). Omit the
   `builtin` flag for user-added agents. (Use `$HOME/.omnis/registry/agents/<name>/`
   for user-global agents that don't belong to a specific project.)
2. Optionally create `registry/agents/<name>/instruction.md` to provide a
   custom system instruction. If omitted, the agent falls back to
   `registry/agents/default.md`.
3. Add the agent's name to the `agents` list in `agents.json` (from the active search-chain layer).
4. Add the new agent's name to the `members` list of every squad that
   should expose it (omit the entry to keep an agent reserved for one
   squad). If a squad omits the agent, the squad's leader won't see it
   as a delegable tool.
5. `agent.NewAgent()` auto-discovers the agent via `runtime.Agents`; no
   Go code change needed unless you want custom tool wiring
   (`defaultToolKeys`).

### Adding a new squad

Squads compose existing agents. Add a `SquadEntry` to the top-level
`squads:` array in `agents.json`:

```json
{
  "squads": [
    {
      "name": "default",
      "leader": "leader",
      "members": ["investigator", "web_agent", "summariser"]
    },
    {
      "name": "research",
      "description": "Web research focus.",
      "leader": "leader",
      "members": ["web_agent", "summariser"]
    },
    {
      "name": "helper",
      "description": "Single specialist, no coordinator.",
      "leader": "none",
      "members": ["helper"]
    }
  ]
}
```

Rules enforced at resolution time:

- A squad named `default` is always present; the resolver synthesises
  one (from enabled agents) when missing or when the user adds only a
  non-default squad in the editor.
- `leader` and every `members[i]` must reference an enabled agent;
  `curator` cannot be a member (it is process-wide).
- A non-`"none"` `leader` must be an agent marked `leader: true`.
- A `leader` of `"none"` (or empty) makes the squad **leaderless** and
  requires **exactly one member** (it runs directly as the root — see
  "Leaderless squads" above); the member need not be `leader: true`.
- Duplicate squad names are rejected.

The web UI exposes a Squads sub-tab under Settings → Agent with a leader
dropdown (including a `(none — run single agent directly)` option that
switches the member picker to single-select), member checkboxes, and
add/delete. Hot-reload picks up squad edits without a process restart.

### Tool dependency enforcement (`requires`)

Skills and MCP servers can declare runtime **tool dependencies** that the host
**enforces in code** — checking the binary is on PATH, asking the user to
install it, running the install, and re-checking — rather than relying on the
model to follow a prompt. Backed by [internal/deps/](internal/deps/) (`Ensure`).

- **Declaration.** A `requires` list (each `{command, install, label}`); the
  `install` value is either a single string or a per-OS map keyed by `GOOS`
  (`{default, linux, darwin, windows}`).
  - **Skills** declare it in a **`requires.json` sidecar** next to SKILL.md
    (mirroring the per-skill `permissions.json`), read by
    [internal/skills/](internal/skills/) `RequiresFor`. It must **not** go in
    the SKILL.md frontmatter — ADK's skill loader parses that with
    `KnownFields(true)` and rejects any field outside `name, description,
    license, compatibility, metadata, allowed-tools`.
    ```json
    { "requires": [ { "command": "lit", "label": "LiteParse",
                      "install": "pipx install liteparse" } ] }
    ```
  - **MCP servers** put it on the server entry in `mcp_config.json`
    (`Server.Requires`, [internal/mcp/mcp.go](internal/mcp/mcp.go)).
- **Skill enforcement point** = `load_skill`. A process-wide gate
  (`skills.SetDepGate`, installed from the ask-user registry in
  [agent/infrastructure.go](agent/infrastructure.go) via `newSkillDepGate`,
  [agent/skill_deps_gate.go](agent/skill_deps_gate.go)) decorates the
  `load_skill` tool ([internal/skills/deps_gate.go](internal/skills/deps_gate.go)
  `gatedLoadTool`, mirroring softskills' `renamedTool` — it must pack **itself**
  in `ProcessRequest` or dispatch bypasses the gate). When a loaded skill's
  declared binary is missing it asks the user (`ask_user`), installs via the
  **Bash safety floor**, and rechecks. `skills.Toolset`'s signature is
  unchanged; with no gate set (CLI/TUI examples) behaviour is byte-identical.
- **MCP enforcement point** = first connect. The pool routes any server with
  `requires` (or `${input:id}` refs) through the **lazy transport**
  ([internal/mcp/inputs.go](internal/mcp/inputs.go) `lazyTransport.Connect` →
  `ensureServerDeps`), so the dependency gate fires at first tool use — where a
  session context exists to prompt — not at agent-build/boot time.
- **On decline / install failure** the behaviour is *report unavailable,
  fallback* (not hard-block): the skill gate attaches a `dependency_status`
  notice to the `load_skill` result so the model uses the skill's documented
  fallback; the MCP gate returns a sticky `Connect` error so the server reports
  as unreachable. Enforcement guarantees *"no silent skip of the install once a
  dependency-bearing skill/server is engaged"* — it does **not** override the
  model's choice of which skill to use in the first place (that stays prompt-led).

### Adding a skill

1. Create `registry/skills/<name>/SKILL.md` with YAML front matter:
   ```yaml
   ---
   name: my-skill
   description: One-line description shown in list_skills output
   commands:        # optional — slash commands this skill depends on
     - my-command
   permissions:     # optional — permission rule-sets this skill depends on
     - my-ruleset
   ---
   # Skill content as markdown instructions
   ```
   The directory name must equal the frontmatter `name` field. The optional
   `commands` / `permissions` lists are **dependency declarations**: when the
   skill is installed from a registry, each name is resolved from a configured
   `commands` / `permissions` registry and installed too (see "Dependency
   cascade on skill install"). They are inert for a hand-authored local skill.

2. Add the skill name to the `"skills"` list in each agent's
   `registry/agents/<name>/agent.json` that should have access to it:
   ```json
   { "skills": ["my-skill", "other-skill"] }
   ```
   An empty list means no skills; the field is absent for agents that
   don't expose the `"Skill"` tool at all.

Hot-reload picks up changes to `agent.json` without a process restart.
The skill files themselves are read on demand at `load_skill` call time.

### Connecting remote A2A agents (client side)

A2A peers are wired via `a2a_config.json` (resolved from the config search chain) — no Go code required.

1. Add an entry for each remote endpoint:
   ```json
   {
     "agents": {
       "peer-omnis": {
         "url": "http://peer-host:8091/",
         "description": "Secondary omnis server.",
         "headers": { "Authorization": "Bearer ${input:peer_token}" },
         "squad": "",
         "session_name": "",
         "create": false
       }
     },
     "inputs": [
       { "id": "peer_token", "type": "promptString", "description": "Peer token", "password": true }
     ]
   }
   ```

2. Add the peer name to `registry/agents/leader/agent.json`:
   ```json
   { "a2a_agents": ["peer-omnis"] }
   ```

3. Hot-reload picks up both files (`POST /api/config/reload`). The leader
   then sees an `a2a_peer-omnis` tool it can invoke with a `prompt`,
   optional `squad`, `session_name`, and `create` arguments.

**Session routing** — when `session_name` is set the remote server looks up
the session by its friendly petname (e.g. `teaching-kite`), runs the turn,
persists it to the session's conversation file, and fires a `mailbox_push`
SSE event to any open web UI tab on that session. When `create: true` the
session is materialised if it does not yet exist (uses the same
`NewWithName` path as `POST /api/sessions` with a name).

**Tool argument precedence**: per-call `squad`/`session_name`/`create` >
`a2a_config.json` defaults > remote server's own defaults.

### A2A server (inbound calls)

`server/a2a_server.go` handles inbound `tasks/send` and `tasks/sendSubscribe`
calls from other A2A agents. Key behaviours:

- **Squad routing**: `metadata.squad` selects which squad the task runs on
  (falls back to `default`).
- **Session routing**: `metadata.session_name` routes into an existing named
  session. `metadata.create: true` auto-creates it if missing.
- **Ephemeral sessions**: omitting `session_name` creates a throwaway session
  per task and discards it after the response.
- **SSE push**: after persisting a turn, `sessionPushBroadcaster.notify`
  fires a `mailbox_push` event so open web UI tabs refresh live.
- **RunGuard**: `sessionRunGuard` serialises concurrent turns on the same
  session (shared with the web UI path — no double-processing).
- **Session name validation**: names must match `[a-z0-9-]{1,80}` (`validSessionName`).

Enable the A2A server via `server.yaml`:
```yaml
a2a_enabled: true
a2a_port: 8091
```

### Remote registries (skills, agents, mcp, a2a, squads, commands, permissions)

The web UI can browse and install skills, agents, MCP servers, A2A peers,
squads, slash commands, and permission rule-sets from any GitHub, GitLab, or
Gitea repository. All share the same `remote_registries.json` file (resolved
from the config search chain; with the same fork-on-first-edit semantics as
other config), and the same set of provider adapters in
[internal/registries/](internal/registries/).

Each entry has a `kind` field: `skills` (default when missing — legacy),
`agents`, `both` (skills + agents), `mcp`, `a2a`, `squads`, `commands`, or
`permissions`. A **permissions** registry item is a directory holding a
`permissions.json` (same `permissions.{allow,ask,deny}` shape as the
local file; old `always_*` files auto-convert); installing **merges** its rules into the user's `permissions.json`
deduped by pattern (`registries.MergePermissionsFile`), rather than copying a
file. The Settings → Registries hub exposes a **Permissions** kind alongside
the others.
The Settings → Skills/Agents/MCP/A2A/Commands → Remotes tabs each list
only the registries whose `kind` matches; a `both` entry shows up in
both the skills and agents tabs. The "Hosts" selector on the add/edit
dialog sets the kind.

There is also a consolidated **Settings → Registries** section (top-level
sidebar entry, between Commands and Appearance) that concentrates every remote
registry grouped by kind in a left nav (Skills / Agents / Squads / MCP / A2A /
Commands), with the same Add / Edit / Remove / Browse / Install flows as the
per-kind Remotes tabs — it *reuses* those per-kind renderers
([web/settings.js](web/settings.js) `renderRegistriesHub`), it does not
duplicate them. Two nav-context indirections let the reused renderers re-render
into the hub's right panel: `registriesHubRefresh` for the form-based kinds
(skills/mcp/a2a/commands) and the pre-existing `refreshRemotesRightFn` for
agents/squads; both are cleared at the top of each per-kind form renderer so the
standalone tabs are unchanged. A single **Reindex** button rebuilds the semantic
registry index via `POST /api/registries/reindex`
([server/server.go](server/server.go)), which calls
`Infrastructure.RegistryIndex(...).Reindex(ctx)` and returns the indexed-item
count (or `400` with a clear message when no embedding model is configured, in
which case the index is absent and recall falls back to glob/browse).

Remote layout — agents:

```
repo/path/to/agents/
├── leader/
│   ├── agent.json        ← required; same shape as registry/agents/<name>/agent.json
│   └── instruction.md    ← optional
└── investigator/
    └── agent.json
```

Remote layout — skills: one `SKILL.md` per subdirectory.

```
repo/path/to/skills/
├── my-skill/
│   └── SKILL.md
└── other-skill/
    └── SKILL.md
```

Remote layout — commands: one Claude Code-style markdown file per command
(Anthropic's `~/.claude/commands/<name>.md` formalism). The filename
without `.md` is the command name; YAML frontmatter (optional) supplies
`description` and `argument-hint`; the body is the prompt template,
supporting `$1..$N` positional and `$*` rest placeholders.

```
repo/path/to/commands/
├── review.md             ← frontmatter + body
└── triage/
    └── repro.md
```

Remote layout — permissions: one directory per rule-set, each holding a
`permissions.json` (same `permissions.{allow,ask,deny}` shape as the local file;
old `always_*` files auto-convert on install). The directory leaf is the rule-set
name; install **merges** the rules into `permissions.json` rather than copying a file.

```
repo/path/to/permissions/
├── kubectl-readonly/
│   └── permissions.json
└── git-safe/
    └── permissions.json
```

The browse view discovers `agent.json`, `SKILL.md`, or command `.md`
files recursively under the registry URL's `tree` path. The install
button downloads every file in the matched directory into
`$OMNIS_HOME/registry/agents/<name>/` (agents) or
`$OMNIS_HOME/registry/skills/<name>/` (skills). Commands install into
the single per-user `$OMNIS_HOME/user_commands.json` file (same store
the local Slash Commands editor writes to). After installing a skill,
add its name to the target agent's `"skills"` list in `agent.json` —
either via the web UI Skills tab or by editing the file directly.

The agent install dialog also exposes an "Enable in agents.json"
checkbox — when checked the installed agent's name is appended to the
runtime config's `agents` list so the next hot-reload wires it in.

**Dependency cascade on agent install** — installing an agent also resolves
the `skills` and `mcp_servers` (alias `mcpServers`) it declares in its
`agent.json` so the agent is actually usable, not just present. This happens on
**both** install surfaces:

- **Web UI** ([server/remote_registry_agents.go](server/remote_registry_agents.go)
  install route → [server/install_helpers.go](server/install_helpers.go)): each
  missing skill via `tryAutoInstallSkills` and each missing MCP server via
  `tryAutoInstallMCP`.
- **Helper agent** (the `install_remote_item` tool, `KindAgents` case in
  [internal/registries/tools.go](internal/registries/tools.go)): after the
  install it fetches the remote `agent.json`, `parseAgentDeps` extracts the
  lists, and `Deps.resolveAgentDeps` ([internal/registries/agent_deps.go](internal/registries/agent_deps.go))
  installs the missing skills/MCP servers from the configured registries. The
  remote manifest is the dependency source of truth (no disk-layer guess).

MCP resolution is shared by every surface via `registries.ResolveMCPServer` +
`registries.MergeMCPServer` ([internal/registries/mcp_install.go](internal/registries/mcp_install.go)),
which handle both `mcp.md` (YAML frontmatter) and JSON manifests — so the helper
agent's `InstallMCP` ([agent/agent.go](agent/agent.go) `buildRegistriesDeps`) and
the web UI route stay in lock-step. Anything not found in any registry
comes back as a `warnings[]` entry, surfaced by `showInstallResult`
([web/settings.js](web/settings.js)) for the web UI or in the tool result for the
helper. Resolution is best-effort and never rolls back the agent install.

**Dependency resolution searches every registry, not just kind-matched ones.**
The cascade loops (`resolveAgentDeps`/`resolveSkillDeps` in
[internal/registries/agent_deps.go](internal/registries/agent_deps.go), and the
`tryAutoInstall*` helpers in [server/install_helpers.go](server/install_helpers.go))
deliberately do **not** filter registries by `Serves(kind)` when hunting a declared
dependency: a multi-purpose repo (an agent shipped alongside its skills + MCP
server) is usually registered under a single `kind`, so a kind filter would skip
the very skill/MCP the agent needs even though `search_registries` (which indexes
every kind) lists it. Each `Browse*` call is a best-effort tree walk that returns
nothing in a registry lacking that kind's files, so the broadened search is safe —
it just costs a few extra browse calls per install. The helper agent's instruction
([registry/agents/helper/instruction.md](registry/agents/helper/instruction.md))
additionally requires it to install (not merely report) any dependency that still
lands in `warnings`, by locating it via `search_registries`/`install_remote_item`,
before telling the caller a dependency is genuinely unavailable.

**Dependency cascade on skill install** — symmetrically, a **skill** declares
its dependencies via two SKILL.md frontmatter lists, `commands:` and
`permissions:` (parsed onto `registries.Frontmatter`). Installing the skill
cascades them from the configured `commands` / `permissions` registries on
**both** surfaces:

- **Web UI** ([server/remote_registry.go](server/remote_registry.go) skill
  install route → [server/install_helpers.go](server/install_helpers.go)):
  `parseSkillMDDeps` + `tryAutoInstallCommands` / `tryAutoInstallPermissions`.
- **Helper agent** (`install_remote_item` / `install_remote_skill`,
  `KindSkills`/`KindBoth`): `Deps.cascadeSkillDeps` fetches the SKILL.md and
  `Deps.resolveSkillDeps` installs the declared commands/permissions
  ([internal/registries/agent_deps.go](internal/registries/agent_deps.go)).

Commands install into `user_commands.json`; permission rule-sets **merge** into
`permissions.json` (deduped by pattern, idempotent). The helper triggers a
hot-reload after a skill install so newly-merged permission overlays apply live.
A skill's bundled `permissions.json` (a file inside the skill dir) is still
copied by `InstallSkill` and loaded as a per-skill runtime overlay — separate
from the registry-merge path above.

**Hot-reload on helper install** — the `install_remote_item` /
`link_skill_to_agent` tools call `Deps.RequestReload` after a config-affecting
install (agent / MCP / squad / A2A / skill-link) so the item is wired into the
running fleet without a manual "Reload" click. The server wires this hook to
`Manager.Reload` via the process-wide `agent.SetReloadHook`
([agent/reload_hook.go](agent/reload_hook.go), set in [server/main.go](server/main.go));
CLI/TUI leave it nil (config edits apply on next start), and the tool result's
`reloaded` flag honestly reflects whether a reload fired. The web UI keeps its
existing post-save Reload banner instead.

Use `OMNIS_AGENTS_REGISTRY_DIR` or `OMNIS_SKILLS_REGISTRY_DIR` to redirect
either install location independently of `OMNIS_HOME`.

### Web UI debug mode

The web UI ships with a built-in debug overlay for inspecting streaming
performance and other client-side metrics. Enable it by either:

- Appending `?debug=1` to the URL, or
- Setting `localStorage.agent_toolkit_debug = "1"` (persists across reloads).

A small monospace badge appears in the top-right corner showing live per-turn
metrics:

```
[client] ttfb=120ms  chunks=84  42.3/s  bytes=1980
         render=18ms across 1 parse(s)
[server] ttfb=95ms  chunks=84  44.1/s  total=2010ms
```

- **client** metrics are measured in the browser (TTFB from `fetch()` start,
  cumulative `marked.parse` cost, chunks-per-second based on token-event
  arrival).
- **server** metrics are emitted by the backend as a `debug_timing` SSE event
  right before `done` (see [server/sse.go](server/sse.go) `emitDone`). They
  reflect the rate at which the agent is producing tokens on the wire,
  independent of any browser-side cost.

The instrumentation API is exposed on `window.AgentDebug` for ad-hoc probing
from the browser console. Extend it by adding new fields to the object in
[web/app.js](web/app.js) and calling `_paint()` after mutating state — keeping
the badge as the single surface for new client-side measurements.

### Resilient turn streaming (disconnect-survival + reconnect)

A web-UI user turn used to be bound to its HTTP request: `handleMessages`
([server/sse.go](server/sse.go)) ran the agent on `c.Request.Context()`, so any
interruption of the streaming `fetch()` (a reverse-proxy idle timeout, a Wi-Fi
blip, a closed tab) cancelled the context and **aborted the run mid-work** — and
the browser surfaced a raw `TypeError: network error`. Request-context
cancellation also doubled as the Stop button. Both are now decoupled.

- **`liveTurn` buffer** ([server/live_turn.go](server/live_turn.go)): a
  process-wide `liveTurnRegistry` (one `*liveTurn` per session, on
  `serverDeps.LiveTurns`, built in [server/main.go](server/main.go)) buffers
  every SSE frame of the in-flight turn. The buffer is the single source of
  truth: producers append via `emit(event, data)` (monotonic `seq`, 1-based) and
  wake consumers via a **closed-channel broadcast** (`notify` closed + replaced
  per emit); consumers read frames by `seq` from the slice, so a slow or
  reconnecting consumer never loses a frame. A `maxBufferBytes` (8 MiB) cap trims
  the oldest frames on a runaway turn (advancing `firstSeq`); a reconnect asking
  for a trimmed range gets a `reload` control frame instead of a corrupt replay.
  The turn is retained ~60 s after `finish()` so a reconnect racing completion
  can still drain the tail, then GC'd.
- **Producer/consumer split** in `handleMessages`: the run executes in a
  **background goroutine** on `runCtx, cancel := context.WithCancel(d.rootCtx)`
  (rooted on the server root, so shutdown still cancels) — **not** the request
  context. It holds the run-guard for the whole run, emits all frames to
  `lt.emit` (via the `sink`/`emitFrame` closures — `streamEvents` now takes a
  `sink func(event string, data []byte)` instead of an `io.Writer`), persists the
  turn, then `lt.finish()`. The HTTP handler is just a **consumer**: it
  `lt.stream`s buffered+live frames to `c.Writer` until the turn finishes **or
  the client disconnects** — and returning never cancels the run. Each frame
  carries an `id: <seq>` line.
- **Reconnect endpoint** `GET /api/sessions/:id/messages/stream?from=<seq>`
  (`handleMessageStream`): re-attaches to the live turn and replays frames with
  `seq > from`, then streams live until `done`. Returns **204** when no turn is
  in flight (finished+GC'd or never existed) so the client reloads history.
- **Cancel endpoint** `POST /api/sessions/:id/cancel` (`handleCancel`): the Stop
  button. Calls `lt.cancel()` (aborts `runCtx`), so a real Stop truly aborts the
  server-side run — distinct from a disconnect, which now keeps running.
- **Client** ([web/app.js](web/app.js)): `parseSSE` parses the `id:` line; the
  send path's event switch is extracted into `processStreamEvent` and driven by a
  reusable `consume(res)` (returns `done`/`reload`/`ended`). On a network drop
  (a non-`AbortError` throw, or an `ended` stream) `reconnectStream` retries the
  reconnect GET with capped backoff (~60 s window), showing a subtle
  `reconnecting…` status and resuming into the same bubbles from `lastSeq`; a
  `204` → `reload` → `rerenderSessionFromHistory` (clean rebuild from persisted
  history, no duplicate bubbles, sets the turn count authoritatively so the
  `finally` skips its bump). Exhausting retries shows a friendly "Lost connection
  … reopen this chat to see its reply" message. The Stop button flags
  `sessionStopped` + POSTs cancel + aborts the fetch; an `AbortError` is read as
  an intentional stop (not a reconnect trigger). Closing the tab aborts the fetch
  **without** the flag/cancel, so the run finishes in the background and is
  persisted.

### Streaming liveness heartbeat

The model can spend a long stretch producing **no visible chat text** — most
notably while it streams a large **tool-call argument** (e.g. the AGENT.md body
written by `/init`), which the LLM adapter accumulates silently
([core/llm/openai.go](core/llm/openai.go) `streamSSE` buffers tool-call argument
fragments and only yields a completed `FunctionCall` at turn end). With nothing on
the wire the browser's composer status would sit on a frozen `streaming…`,
reading as a stuck turn. To keep the status honest, `streamEvents`
([server/sse.go](server/sse.go)) runs a 2 s **heartbeat** ticker: when no
content-bearing SSE event has been emitted for ≥ 2 s it emits a `heartbeat`
event `{elapsed_ms}` (content emits bump `lastContentAt`; the heartbeat itself
does not). The client ([web/app.js](web/app.js) SSE `heartbeat` case) turns a
frozen `streaming…`/`thinking…` into a ticking `working… (Ns)`; a resumed `token`
re-asserts `streaming…`, and an explicit `running <tool>…` (tool executing) is
left untouched. `streamEvents` is the web-UI SSE path only (the A2A server uses a
separate path), so no other consumer sees the event.

Streaming renders in two tiers ([web/app.js](web/app.js)
`streamMdAdvance`/`streamMdFinalize`). **Completed blocks** — anything before a
blank line outside a fence, or a closed code fence — are promoted to HTML by
the full `marked.parse` exactly once and never re-parsed. The **in-progress
trailing block** (the "tail") is a single live node: a raw `<pre><code>` Text
node for code fences (appended via `appendData` at wire speed, verbatim), and a
`<span class="md-stream-tail">` for prose. The prose tail is re-rendered every
token by `lightStreamMd` — a tiny regex renderer that handles the common block
constructs (ATX headings, ordered/unordered tight lists, `<hr>`, blockquotes)
plus inline emphasis/strike/code via `lightInline`, collapses accumulated blank
lines, and emits `<br>` for single newlines to mirror `breaks:true`. Its HTML is
shaped to match `marked`'s tight-list/heading output so the preview doesn't
reflow when the real parser flushes the block. `lightInline` protects inline
code with private-use sentinels (`<n>`) before escaping/emphasis so
`**\`x\`**` renders as bold-wrapping-code (and code-internal `*`/digits stay
literal). This is cheap because it only ever touches the current block
(everything before `s.blockStart` is already flushed), so cost stays O(block),
not O(message²). The bubble carries **no `white-space: pre-wrap`** — the tail
emits its own `<br>`s and code lives in `<pre>`, so dropping pre-wrap stops the
literal newlines `marked` puts between block tags (`</ul>\n<ul>`) from rendering
as blank gaps mid-stream. **Do not run the full `marked.parse` per chunk on the
whole message** — that quadratic re-parse is what makes the UI feel slow even
when the wire is fast; `lightStreamMd` is the bounded exception, and only the
heavy parser produces the authoritative final HTML at block flush / finalize.

### Web UI tooltips (`data-tip`, never native `title`)

All hover tooltips in the web UI go through the **in-app themed tooltip
popup**, never the browser's native `title` attribute. A single body-appended
`#tip-layer` element ([web/app.js](web/app.js) `initTooltips`) renders every
`[data-tip]` tooltip: because it is `position: fixed` it escapes the sidebar /
panel `overflow` clipping, sits above every panel, and is styled to match the
active theme. Placement is above the target by default, flipping below only
when there isn't room near the viewport top; the arrow tracks the target's
centre after horizontal clamping.

- **To add a tooltip**: set `data-tip="…"` in an HTML string, or
  `el.setAttribute("data-tip", …)` in JS. **Do not use the `title` attribute
  or `el.title = …`** — native tooltips are unstyled, can't escape `overflow`
  clipping, and look inconsistent. Hovering a child of a `[data-tip]` element
  still resolves to the nearest ancestor (the handler uses
  `closest("[data-tip]")`), so wrapping containers can carry the tip.
- **Exception**: `.model-status-dot` keeps its own dedicated CSS pseudo-element
  tooltip and is explicitly **excluded** from the `#tip-layer` handler — leave
  its `data-tip` in place but don't expect the JS layer to render it.
- The layer reads `data-tip` via `textContent` (no HTML injection); HTML-string
  call sites still `escHtml()` the value as usual.
- **Long tips wrap**: the layer is `white-space: normal` with `max-width: 18rem`,
  so short tips stay on one line (the box shrink-wraps) while a long description
  soft-wraps onto multiple lines instead of stretching into one unreadable
  strip. Just write the full description in `data-tip` — wrapping is automatic,
  no manual line breaks.

### Web UI todo plan widget

The `todo_write` / `todo_update` / `todo_read` tools do **not** render as the
generic collapsed tool block. Instead [web/app.js](web/app.js) keeps a
per-session plan view in `sessionTodos` (sessionId → `[{task, status}]`) and
renders an "Update Todos" checklist (`.todo-block`, styled in
[web/css/styles.css](web/css/styles.css)) on every todo tool call so users can
follow plan execution: pending = empty box, `in_progress` = spinning marker,
`done`/`failed` = filled box + struck-through text. `todo_write` rebuilds the
list from `args.tasks`; `todo_update` mutates the item at `args.index`. These
calls are routed in the `tool_call` SSE case (via `isTodoTool`) and are **not**
pushed to `pendingTools`, so their `tool_result` is ignored.

Only the latest snapshot per session stays expanded: `sessionTodoBlock`
(sessionId → latest `.todo-block`) lets `appendTodoBlock` add the `collapsed`
class to the prior block when a new one arrives. Any block's header is a
click-toggle, and its `done/total` progress count stays visible while
collapsed. State is live-only (history replay renders text turns, not tool
calls) and both maps are cleared on session delete.

### Web UI ask-user wizard

`ask_user` questions for a session render as a **single multi-step wizard
card** in the pane's `#ask-user-slot` (above the composer), not a stack of
separate cards. The model lives in [web/app.js](web/app.js): `askWizards`
(sessionId → wizard `{ row, card, steps, current, busy, _submit }`) plus
`pendingAskWidgets` (questionId → `{ sessionId }`, so a server `ask_user_cancel`
can find the owning wizard). Each **step** is either `{type:"single", q,
resolved, answer}` or `{type:"group", group, questions[], scopeIdx, resolved,
cancelled}` — an install-permission burst (questions sharing a `group` tag, see
[internal/askuser/askuser.go](internal/askuser/askuser.go) `Question.Group`)
folds in as **one** group step that applies a single shared Allow/Deny scope to
every member question.

`renderAskUserWidget` routes each arriving question into the session's wizard
(`ensureWizard` + `addQuestionToWizard`); `renderWizard` rebuilds the card —
a clickable **step rail** (`.ask-wizard-rail`, hidden when there's one step;
current chip highlighted, resolved chips show ✓/✗), the active step's body
(`renderSingleStepBody` reuses the per-kind `buildAskInput`; `renderGroupStepBody`
reuses the install list + shared scope choices), and a `← Back` / `Skip` /
`Next →`-or-`Submit` action row (`appendWizardNav`). The card element persists
across renders (only children are replaced), so the one `keydown` Enter handler
wired in `ensureWizard` survives — it fires `wiz._submit`, which each render
points at the current step's primary action. **Steps resolve server-side as
soon as answered** (`submitSingleStep` / `submitGroupStep` POST to
`/api/sessions/:id/ask-user/:qid`), so a long wizard never lets early questions
hit the 5-minute timeout; `afterStepResolved` auto-advances to the first
unanswered step or `finalizeWizard`s (collapse to a stacked per-step summary,
moved into the transcript). Revisiting a resolved step via the rail shows a
read-only summary. On tab-hide the wizard requeues only its **unanswered**
questions into `queuedAskWidgets` and is torn down (rebuilt fresh on reselect);
session delete clears `askWizards`.

### Web UI split panels (VS Code-style)

`#chat` is a horizontal flex **row** of one-or-more independent `.chat-pane`
columns separated by draggable `.pane-divider` handles ([web/index.html](web/index.html)
`<template id="chat-pane-tpl">` is cloned per pane; [web/css/styles.css](web/css/styles.css)
`.chat-pane`/`.pane-divider`/`.pane-tabbar`/`.pane-toolbar`/`.pane-picker`). Each
pane owns its own copy of the chat UI (transcript, composer, prompt, send/cancel,
status, context ring + popup, ask-user slot, attachments).

**Each pane is a tab group**: `panel.tabs[]` is an ordered list of **tab keys** —
each key is one of three kinds: a real sessionId, a synthetic **draft** key
(`"draft#N"`, a pending "New Chat" tab with no session), or an **editor** key
(`"file#<absPath>"`, a Monaco file editor — see "Web UI file editor" below).
`panel.activeTab` is the visible key;
`panel.sessionId` mirrors it but is **null while a draft or editor tab is active** (kept for the
many call sites that read the active session). A pane always has ≥1 tab. The tab
strip (`.pane-tabs` in the `.pane-tabbar`, one `.pane-tab` per key — drafts get
`.pane-tab-draft` — plus a `+` `.pane-newtab-btn`) is rebuilt by
`renderPaneTabs(panel)`; clicking a tab `activateTab`s it (a draft key shows the
start picker, a session key mounts its transcript), the `×`/middle-click
`closeTab`s it. `+` (`newDraftTab`) **always** appends a fresh draft tab and
activates it — several drafts can coexist; the session is created only when the
user clicks "Start a new chat" (`newChat`) or picks one from the picker
(`bindSessionToPanel`), which takes the active draft's slot in place rather than
appending. Closing the last tab closes the pane, except the sole pane gets a fresh
draft so it's never tab-less. A session's transcript is a single cached DOM node
(`getContainer(sessionId)`), so a session lives in **at most one tab across all
panes** — selecting a session open elsewhere focuses that pane and activates its
tab rather than duplicating. Background tabs (open but not active) keep their push
subscription and accrue streamed turns into their detached container; the per-tab
busy dot reflects `sessionSending`. Draft keys are ephemeral — `saveLayout` strips
them and persists only session tabs.

Two membership helpers: `panelsForSession(id)` = panes where `id` is the **active**
tab (drives visible-pane chrome — status, ctx ring, ask widget, scroll);
`panelsWithTab(id)` = panes holding `id` as **any** tab (drives "open anywhere"
logic — push subscriptions via `releaseSessionIfUnviewed`, sidebar `.active`
highlight, dedupe-on-open, and delete/archive cleanup via `closeTabEverywhere`).
The shared per-pane ask-user slot is tab-scoped: `activateTab` re-queues a hidden
tab's ask widgets (`row._askQ` → `queuedAskWidgets`) and flushes the active tab's.

Per-session state stays in the existing `sessionId`-keyed Maps; the rest is in the
view layer ([web/app.js](web/app.js)): a `panels` array of
`{id, sessionId, tabs, root, els, width, _stick}` objects, `focusedPanelId`, and
helpers `focusedPanel()`/`fp()`, `setFocusedPanel`, `activateTab`/`closeTab`/
`bindSessionToPanel` (add-tab + activate), `createPanel`/`splitPanel`/`closePanel`,
`renderPaneTabs`, `rebuildChatDOM`/`layoutWidths`, `paneOfNode`/`sessionIdOfNode`
(resolve a node's pane/session for scroll/media — the latter handles background
tabs whose container is detached). `activeSessionId` is a **compatibility shim** =
the focused pane's active tab, so global-action sites (sidebar, modals, ctx
browser) keep working. Display-write functions (`applySessionUI`,
`renderCtxRing/Popup`, `setStatus`, `scrollBottom`, pinned-prompt,
`renderAttachmentsUI`, `renderAskUserWidget`, streaming gates) take/loop a `panel`
via `panelsForSession` so **background panes update too**; `applySessionUI` also
re-renders tabs (busy dot) via `panelsWithTab`. Listeners are wired **per-pane** in
`attachPaneHandlers`.

A pane's toolbar has split (clones a new empty pane to the right) and close
(hidden when `#chat.solo`) buttons. Selecting a sidebar session opens it as a
**new tab** in the focused pane; closing the **last** tab closes the pane (or, for
the sole pane, falls back to the empty `.pane-picker`). An empty pane shows
`.pane-picker` (start a new chat → `newChat(panel)`, or open an existing session).
Layout (per-pane `tabs` + `activeId`/`activeKey` + widths + focus) persists to
`localStorage["agent_toolkit_layout"]` as a **v2** record (`saveLayout`/
`restoreLayout`; v1 single-`sessionId` records still load), restored on boot after
`loadSessions`, dropping dead session ids (editor `file#…` keys survive the
live-session filter and reopen via the editor path). The
Settings panel still appends to `#chat`; `#chat.chat--settings > .chat-pane`
hides panes while it's open, and `rebuildChatDOM` preserves `#settings-panel`.

### Web UI file editor (Monaco)

Double-clicking a file in the **Folders** panel opens it in an embedded
[Monaco editor](https://microsoft.github.io/monaco-editor/) as a third pane-tab
kind — an **editor tab** keyed `"file#<absPath>"` — living next to chat-session
and draft tabs in the same `.pane-tabs` strip (`openFileInEditor(rel)` in
[web/app.js](web/app.js); `abs = join(foldersDir, rel)`). Opening an
already-open file focuses its tab rather than duplicating (editor tabs, like
sessions, live in at most one pane).

- **Vendored offline.** Monaco's `min/vs` is committed under `web/monaco/vs/`
  (served at `assets/monaco/vs/…` since `base.Static("/assets", webDir)` maps
  `/assets` → `web/`), so it works air-gapped — no CDN at runtime, mirroring the
  vendored [web/marked.min.js](web/marked.min.js). Re-vendor / bump with
  `make vendor-monaco` (`MONACO_VERSION` overridable, currently 0.55.1).
  `ensureMonaco()` lazily injects the AMD `loader.js` on first file open
  (computing the `vs` base from `document.baseURI` so a `BasePath` deployment
  works) and `require.config({paths:{vs}})`s it. **It deliberately does NOT set a
  custom `MonacoEnvironment.getWorkerUrl`**: Monaco ≥ 0.54 resolves its language
  workers itself — the hashed `vs/assets/<label>.worker-<hash>.js` files, via the
  loader's `toUrl` relative to the `vs` base, already absolute + same-origin — so
  the pre-0.54 blob-worker indirection that `importScripts`'d `base/worker/workerMain.js`
  is gone (that entry no longer exists; overriding it would break every language
  worker). When bumping Monaco, re-verify worker loading (a TS/JSON/CSS edit must
  still get diagnostics) since the worker layout is version-specific.
- **Per-file model, per-pane editor.** `editorModels` (absPath → Monaco model,
  created once from `GET /api/file`, language from `langForPath`) holds content +
  undo history, so switching tabs preserves unsaved edits; one Monaco instance
  per pane (`panel._editor`) `setModel`s the active file. The pane gets the
  `.editing` class while an editor tab is active (CSS hides the chat surfaces and
  shows `.pane-editor`); `activateTab`/`newChat`/session-open paths clear it.
  Theme follows the app: `monacoTheme()` maps `<html>[data-theme]` → `vs`/`vs-dark`,
  kept live by a `MutationObserver` on the attribute.
- **Edit + save to disk.** `editorDirty` tracks unsaved changes (a `.pane-tab`
  `.is-dirty` dot that yields to the × on hover). **Ctrl/Cmd+S** (a Monaco
  command) or the **Save** button (`saveEditor`) `PUT /api/file`
  `{ path, content, session }` → `handleFileWrite` ([server/fileref.go](server/fileref.go)),
  which writes straight to the host file preserving its mode. Like `GET /api/file`
  and the `!` shell-escape it is gated only by the API token and **bypasses the
  agent permission layer** (the authenticated user already has host file access);
  it edits **existing files only** (path must classify as a regular file).
- **Live-refresh on agent edits.** When the agent's `Write`/`Edit`/`revert`
  tools mutate a file, the server emits a **`file_changed`** SSE event carrying
  the **absolute** path — resolved against the session's working directory in
  [server/sse.go](server/sse.go) (`streamEvents` now takes the session `cwd`;
  `noteFileTool` records the path per `call_id` at the tool-call, `emitFileChanged`
  fires it at the tool-result only when the result isn't an `Error …` string).
  Both the leader (`tool_call`/`tool_result`) and sub-agent
  (`agent_tool_call`/`agent_tool_result`) paths are wired. The client
  ([web/app.js](web/app.js) `onAgentFileChanged`) refreshes any open editor model
  for that abs path: when the tab has **no unsaved edits** it reloads in place
  (`reloadEditorFromDisk` — a full-range `pushEditOperations`, preserving
  cursor/scroll via `saveViewState`/`restoreViewState`, guarded by
  `editorApplyingExternal` so it doesn't mark the tab dirty); when the tab **is
  dirty** it instead flags it stale (`editorStale`) and shows the
  `.pane-editor-stale` banner with a **Reload from disk** button — so the agent's
  changes never silently clobber unsaved edits and vice-versa. Stale state clears
  on save or reload.
- **Lifecycle.** `closeTab` on a dirty editor tab confirms discard, then disposes
  the model; `closePanel` disposes the pane's Monaco instance and any editor-tab
  models it owned. Editor keys carry no push subscription, so the session-only
  `releaseSessionIfUnviewed` is skipped for them.

### Web UI interactive terminal (xterm.js + PTY)

A **fourth** pane-tab kind — a terminal tab keyed `"term#<n>"` — runs a **real
interactive shell** (vim/top/ssh all work, full ANSI/colour) next to chat,
draft, and editor tabs in the same `.pane-tabs` strip. Open one from the pane
toolbar's terminal button (`openTerminalTab`) or from the Folders panel context
menu's **"Open Terminal here"** (rooted at the right-clicked dir / path header).

- **Backend** ([server/terminal.go](server/terminal.go) + platform files,
  registered as `GET /api/terminal/ws` in [server/server.go](server/server.go)):
  a **WebSocket** upgraded via `gorilla/websocket` bridges to a **PTY-backed
  shell** (`creack/pty`). The PTY abstraction is `ptySession`
  (Read/Write/Resize/**Cwd**/Close); the real implementation is in
  [server/terminal_unix.go](server/terminal_unix.go) (spawns `$SHELL` → `/bin/bash`
  → `/bin/sh`, `TERM=xterm-256color`; `Cwd` reads `/proc/<pid>/cwd`), and
  [server/terminal_windows.go](server/terminal_windows.go) is an unsupported stub
  (no ConPTY) so cross-platform builds stay green.
- **Auth**: the route is registered on the **unauthenticated** `api` group
  because a browser can't set an `Authorization` header on a WebSocket handshake;
  `handleTerminal` validates the bearer token from the **`token` query param**
  itself (constant-time; empty server token = unauthenticated mode). `CheckOrigin`
  additionally restricts browser clients to same-origin.
- **Working directory**: explicit `?cwd=` (validated dir) wins, else `?session=`'s
  Folders/`!cd` cwd (`bashCwd`), else the global "no session" cwd.
- **Wire protocol** (`runTerminalSession`): client → server **BinaryMessage** =
  raw stdin bytes, **TextMessage** = `{"cols":N,"rows":N}` resize; server →
  client **BinaryMessage** = raw PTY output, **TextMessage** = `{"cwd":"…"}` cwd
  control (see cwd sync below). One PTY→WS goroutine + one cwd-watcher goroutine,
  both serialised on a write mutex; the WS read loop pumps stdin + resize. Shell
  exit closes the PTY which ends all three.
- **cwd sync with the Folders panel**: a watcher goroutine polls `pty.Cwd()`
  (every 400 ms, Linux `/proc/<pid>/cwd`) and emits a `{"cwd":…}` text frame on
  change (and once on connect). Client `onTerminalCwd` ([web/app.js](web/app.js))
  follows it: while the terminal is the **focused pane's active tab** and the
  Folders panel is open, it `loadFolder(dir)`s — and because a terminal tab has no
  active chat session, that targets the **global** `/api/folder` cwd (the same dir
  the panel shows with no session), so the panel + the global "no session"
  environment track the shell's `cd`. `mountTerminal` re-aligns the panel to a
  terminal's last-known cwd on (re)activation. Best-effort: where `Cwd()` is
  unsupported (non-Linux) the watcher is a no-op and the panel simply doesn't
  auto-sync. (One-directional: navigating the Folders panel does **not** move the
  live shell.)
- **Trust model**: like the `!` shell-escape and the Monaco save route, the
  terminal **bypasses the agent permission layer by design** and, unlike the Bash
  tool, has **no safety floor** — it is an explicit, token-gated, fully
  interactive host shell. Output is never added to conversation/LLM history.
- **Client** ([web/app.js](web/app.js) "Terminal tabs" section): **xterm.js** +
  the fit addon are **vendored offline** under `web/xterm/` (served at
  `assets/xterm/…`), re-vendored with `make vendor-xterm`; `ensureXterm()` lazily
  injects the scripts + stylesheet on first open (mirroring `ensureMonaco`). Each
  terminal tab owns its own `Terminal` + `FitAddon` + `WebSocket` + detached host
  element in `termTabs` (key → entry), kept alive while backgrounded (output keeps
  streaming into the scrollback). `mountTerminal` moves the host into the pane's
  `.pane-terminal-host` and `fit()`s; `refitVisibleTerminals` re-fits + pushes the
  new size on pane-divider drag and window resize. The pane gets the `.terminal`
  class while a terminal tab is active (CSS hides the chat/editor surfaces, shows
  the xterm host); theme follows the app theme via the existing `data-theme`
  `MutationObserver` (`xtermTheme`).
- **Ephemeral**: terminal tabs are **stripped from the persisted layout**
  (`saveLayout`) — a server PTY can't survive a page reload — and torn down
  (`disposeTerminal`: close WS + dispose term) by `closeTab`/`closePanel`. They
  carry no push subscription, so the session-only `releaseSessionIfUnviewed` is
  skipped, like editor tabs.
