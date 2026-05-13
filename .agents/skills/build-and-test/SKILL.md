---
name: build-and-test
description: How to build, vet, lint and run anything in the agent-toolkit repository. Use whenever you need to compile, run a demo binary, run go vet, set up Go on this machine, or pick an LLM provider via environment variables. Mention triggers - go build, go vet, go run, YOKE_PROVIDER, run root binary, run a demo.
compatibility: Requires Go 1.25 installed at $HOME/.local/go (no sudo). Network access only if calling a remote LLM provider.
---

# Build & test

## One-time environment

Go is installed at `$HOME/.local/go` (no sudo). Always prefix
commands with the local PATH:

```bash
export PATH=$HOME/.local/go/bin:$PATH
export GOPATH=$HOME/.local/gopath
```

You can also inline it for a single command:

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./...
```

## Canonical pre-commit check

After **every** code change, run:

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go vet ./... && echo OK
```

If the final line is `OK`, the project compiles and passes vet. **Do
not declare a task done until you've seen this output.**

## Pick an LLM provider

The harness uses `YOKE_PROVIDER` (default: `openai_compat`):

| Provider        | Auth env                                         | Default model        |
|-----------------|--------------------------------------------------|----------------------|
| `gemini`        | `GOOGLE_API_KEY` *or* `GEMINI_API_KEY`           | `gemini-2.5-flash`   |
| `anthropic`     | `ANTHROPIC_API_KEY`                              | `claude-sonnet-4-5`  |
| `openai`        | `OPENAI_API_KEY`                                 | `gpt-4o-mini`        |
| `openai_compat` | `OPENAI_API_KEY` (optional) + `OPENAI_BASE_URL`  | `gpt-4o-mini`        |

Override the model with `YOKE_MODEL`. Full reference:
[docs/providers.md](../../docs/providers.md).

## Run the all-in-one launcher

```bash
PATH=$HOME/.local/go/bin:$PATH go run . console   # REPL
PATH=$HOME/.local/go/bin:$PATH go run . web webui # web UI
```

## Run a single-component demo

The `cmd/sNN_*` binaries each isolate one component. Example:

```bash
PATH=$HOME/.local/go/bin:$PATH go run ./examples/s05_skills "load the review skill and apply it to README.md"
```

Catalog: [docs/examples-catalog.md](../../docs/examples-catalog.md).

## Common failure modes

| Symptom                                              | Fix                                                                 |
|------------------------------------------------------|---------------------------------------------------------------------|
| `go: command not found`                              | You forgot the `PATH=$HOME/.local/go/bin:$PATH` prefix.             |
| `agentkit.NewModel: llm: ANTHROPIC_API_KEY required` | Set the right env var for `YOKE_PROVIDER`.                       |
| `mailbox backend: …`                                 | `REDIS_URL` set but unreachable, or unset & expected redis backend. |
| MCP server fails at startup                          | Logged and skipped; agent continues. Check `npx`/`uvx` availability.|
| Permission prompt loops                              | Add an explicit `always_allow` rule in `config/permissions.yaml`.   |

## Generated files

These are created at runtime in the launcher's CWD. **Never commit
them** (already gitignored where applicable):

- `.agent_events.log` — JSONL of every plugin event.
- `.agent_memory.md` — context-compression memory snapshot.

## Don't do these

- ❌ Do **not** install Go via `sudo apt`. The local install is the
  source of truth.
- ❌ Do **not** add LLM SDK dependencies to `go.mod` (Anthropic / OpenAI
  are HTTP+SSE adapters by design).
- ❌ Do **not** edit a file by piping into it from the terminal — use
  the editor tools so the LSP picks up the change.
- ❌ Do **not** declare a task complete without `go build ./... && go
  vet ./...` returning OK.
