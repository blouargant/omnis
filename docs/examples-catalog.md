# Single-component demo catalog

The `examples/sNN_*` binaries each isolate one component of the harness.
They are ordered from the simplest (a bare loop, single tool) to the
most complex (multi-agent / distributed / end-to-end). Run any of them
with:

```bash
go run ./examples/sNN_<name> "<your prompt>"
```

(Set `YOKE_PROVIDER` and the appropriate API key first — see
[providers.md](providers.md).)

> **Single-session by design.** These demos use the back-compat
> constructors (`tasks.New`, `todo.NewStore`, `bg.NewQueue`,
> `compress.Config.MemoryPath`, `teammates.NewAgent` with no
> `NameFunc`) and therefore share one file / queue / mailbox across
> runs. The root [main.go](../main.go) instead uses the
> `*SessionScoped` / `SessionQueues` / `NameFunc` variants so
> concurrent `(user, session)` pairs stay isolated — see
> [configuration.md#session-isolation](configuration.md#session-isolation).

## Tier 1 — Single-tool basics

| #   | Binary                       | What it shows                                                                       |
|-----|------------------------------|-------------------------------------------------------------------------------------|
| s01 | `examples/s01_loop`          | The bare model→tool→model loop. No tools attached.                                  |
| s02 | `examples/s02_calc`          | The `calculate` tool — offload arithmetic from the model.                           |
| s03 | `examples/s03_mime`          | The `mime` tool — magic-byte detection vs. filename extension mismatch.             |
| s04 | `examples/s04_stream`        | Streaming text output to stdout.                                                    |
| s05 | `examples/s05_tools`         | The full file/shell tool kit (`bash`, `read`, `write`, `grep`, `glob`, `revert`).   |
| s06 | `examples/s06_revert`        | The `revert` tool walking back a write.                                             |
| s07 | `examples/s07_web_search`    | `web_search` (DuckDuckGo or SerpAPI) + `web_fetch`.                                 |

## Tier 2 — Tool-ecosystem extensions

| #   | Binary                       | What it shows                                                                       |
|-----|------------------------------|-------------------------------------------------------------------------------------|
| s08 | `examples/s08_ask_user`      | The interactive `ask_user` tool wired to stdin.                                     |
| s09 | `examples/s09_output_filters`| Bash output filters (`config/filters/*.json`) condensing noisy commands.            |
| s10 | `examples/s10_mcp`           | MCP toolsets loaded from JSON.                                                      |

## Tier 3 — Session state

| #   | Binary                       | What it shows                                                                       |
|-----|------------------------------|-------------------------------------------------------------------------------------|
| s11 | `examples/s11_todo`          | TodoWrite planning tools.                                                           |
| s12 | `examples/s12_tasks`         | Durable task graph (`task_create` / `_update` / `_list`).                           |
| s13 | `examples/s13_bg`            | Background commands with notifications (`bash_background`).                         |
| s14 | `examples/s14_cache`         | Prompt-cache stats plugin.                                                          |
| s15 | `examples/s15_parallel`      | Several tool calls dispatched in one model turn.                                    |
| s16 | `examples/s16_resume`        | Resuming a session across two runs.                                                 |
| s17 | `examples/s17_interrupt`     | `Ctrl-C` cancels the run cleanly via `context.Cancel`.                              |

## Tier 4 — Cross-cutting plugins

| #   | Binary                       | What it shows                                                                       |
|-----|------------------------------|-------------------------------------------------------------------------------------|
| s18 | `examples/s18_events`        | Event bus + file logger plugin.                                                     |
| s19 | `examples/s19_permissions`   | JSON-driven permission gating (`config/permissions.json`).                          |
| s20 | `examples/s20_compress`      | Context-compression plugin with a tiny threshold for visibility.                    |

## Tier 5 — Specialisation & multi-agent

| #   | Binary                       | What it shows                                                                       |
|-----|------------------------------|-------------------------------------------------------------------------------------|
| s21 | `examples/s21_skills`        | Lazy skill loading from `./skills/`.                                                |
| s22 | `examples/s22_subagents`     | A sub-agent (`summariser`) wrapped as a tool via `agenttool`.                       |
| s23 | `examples/s23_softskills`    | Soft-skills curator distilling a synthetic session into reusable procedures.        |

## Tier 6 — Multi-process & distributed

| #   | Binary                       | What it shows                                                                       |
|-----|------------------------------|-------------------------------------------------------------------------------------|
| s24 | `examples/s24_worktree`      | Git worktree isolation tools.                                                       |
| s25 | `examples/s25_conflicts`     | Programmatic creation of conflicting worktrees → merge abort with conflict list.    |
| s26 | `examples/s26_mailbox`       | Persistent teammate mailbox (in-memory backend).                                    |
| s27 | `examples/s27_fsm`           | The teammate FSM communication protocol exposed end-to-end.                         |
| s28 | `examples/s28_self_assign`   | An autonomous worker goroutine claims and completes tasks.                          |
| s29 | `examples/s29_redis`         | Redis-backed teammate mailbox (`REDIS_URL` required).                               |

## Tier 7 — End-to-end

| #   | Binary                           | What it shows                                                                   |
|-----|----------------------------------|---------------------------------------------------------------------------------|
| s30 | `examples/s30_k8s_context_e2e`   | Real-world Kubernetes context-compression validation with pass/fail checks.     |

## Full launcher

| Binary       | What it does                                                                    |
|--------------|---------------------------------------------------------------------------------|
| `./` (root `main.go`) | Wires every component together. Run with `go run . console` (ADK REPL), `go run . web webui` (ADK web UI), or `go run . --tui` (built-in tview chat). Specialise it via `skills/` + `config/`. |

### Root-binary flags

| Flag                | Default  | Effect                                                                |
|---------------------|----------|-----------------------------------------------------------------------|
| `-s`, `--skills DIR`| `skills` | Directory scanned for skill playbooks (`<name>/SKILL.md`).            |
| `--tui`             | _off_    | Launch the built-in tview chat UI instead of an ADK launcher subcommand. |

Flags must precede any launcher subcommand, e.g.
`go run . --skills ./my-skills console`. See
[configuration.md](configuration.md#command-line-flags) for details.
