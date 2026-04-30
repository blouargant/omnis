# Single-component demo catalog

The `examples/sNN_*` binaries each isolate one component of the harness.
They mirror the phases of the original article so you can run, read or
modify components in isolation. Run any of them with:

```bash
go run ./examples/sNN_<name> "<your prompt>"
```

(Set `GOAGENT_PROVIDER` and the appropriate API key first — see
[providers.md](providers.md).)

## Phase 1 — Loop & basic tools

| #   | Binary                | What it shows                                                |
|-----|-----------------------|--------------------------------------------------------------|
| s01 | `examples/s01_loop`        | The bare model→tool→model loop. No tools attached.           |
| s02 | `examples/s02_tools`       | The full file/shell tool kit (`bash`, `read`, `write`, `grep`, `glob`, `revert`). |
| s03 | `examples/s03_todo`        | TodoWrite planning tools.                                    |
| s04 | `examples/s04_subagents`   | A sub-agent (`summariser`) wrapped as a tool via `agenttool`.|

## Phase 2 — Skills & memory

| #   | Binary                | What it shows                                                |
|-----|-----------------------|--------------------------------------------------------------|
| s05 | `examples/s05_skills`      | Lazy skill loading from `./skills/`.                         |
| s06 | `examples/s06_compress`    | Context-compression plugin with a tiny threshold for visibility. |
| s07 | `examples/s07_tasks`       | Durable task graph (`task_create` / `_update` / `_list`).    |

## Phase 3 — Long-running work & teamwork

| #   | Binary                | What it shows                                                |
|-----|-----------------------|--------------------------------------------------------------|
| s08 | `examples/s08_bg`          | Background commands with notifications (`bash_background`).  |
| s09 | `examples/s09_mailbox`     | Persistent teammate mailbox (in-memory backend).             |
| s10 | `examples/s10_fsm`         | The teammate FSM communication protocol exposed end-to-end.  |
| s11 | `examples/s11_self_assign` | An autonomous worker goroutine claims and completes tasks.   |
| s12 | `examples/s12_worktree`    | Git worktree isolation tools.                                |

## Phase 4 — Streaming, governance, observability

| #   | Binary                | What it shows                                                |
|-----|-----------------------|--------------------------------------------------------------|
| s13 | `examples/s13_stream`      | Streaming text output to stdout.                             |
| s14 | `examples/s14_revert`      | The `revert` tool walking back a write.                      |
| s15 | `examples/s15_permissions` | YAML-driven permission gating (`config/permissions.yaml`).   |
| s16 | `examples/s16_events`      | Event bus + file logger plugin.                              |
| s17 | `examples/s17_resume`      | Resuming a session across two runs.                          |

## Phase 5 — Performance & external tools

| #   | Binary                | What it shows                                                |
|-----|-----------------------|--------------------------------------------------------------|
| s18 | `examples/s18_parallel`    | Several tool calls dispatched in one model turn.             |
| s19 | `examples/s19_interrupt`   | `Ctrl-C` cancels the run cleanly via `context.Cancel`.       |
| s20 | `examples/s20_cache`       | Prompt-cache stats plugin.                                   |
| s21 | `examples/s21_mcp`         | MCP toolsets loaded from YAML.                               |

## Phase 6 — Production extras

| #   | Binary                | What it shows                                                |
|-----|-----------------------|--------------------------------------------------------------|
| s22 | `examples/s22_redis`       | Redis-backed teammate mailbox (`REDIS_URL` required).        |
| s23 | `examples/s23_conflicts`   | Programmatic creation of conflicting worktrees → merge abort with conflict list. |

## Full launcher

| Binary       | What it does                                                                    |
|--------------|---------------------------------------------------------------------------------|
| `./` (root `main.go`) | Wires every component together and hands control to ADK's `full` launcher (`console` REPL or `web` UI). This is the binary you specialise via `skills/` + `config/`. Run with `go run . console`. |
