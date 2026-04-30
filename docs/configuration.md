# Configuration reference

All runtime configuration lives in `config/`. Two files, both YAML.

## `config/permissions.yaml`

The harness's safety envelope. Patterns are Go [`regexp`] strings
matched against the **bash command string** that is about to run (and,
in the future, against tool names).

The file has three lists, evaluated **top to bottom**:

| List           | Meaning                                                       |
|----------------|---------------------------------------------------------------|
| `always_deny`  | Hard-deny. The tool call is never executed; the model sees an error. |
| `always_allow` | Auto-allow. No prompt to the user.                            |
| `ask_user`     | Prompt the user (`y/n`) before executing.                     |

Anything matched by **none** of the three falls through to **ask**
(safe default).

### Default rules shipped

```yaml
always_deny:
  - "rm -rf /"
  - "mkfs"
  - "dd if=.* of=/dev/"
  - ":(){.*};:"          # fork bomb

always_allow:
  - "^ls( |$)"
  - "^cat "
  - "^pwd$"
  - "^echo "
  - "^head "
  - "^tail "
  - "^grep "
  - "^find .* -name"
  - "^go (build|test|vet|fmt)"
  - "^npm (test|run build)"
  - "^kubectl (get|describe|logs|top|explain) "
  - "^kubectl config (current-context|get-contexts|view)"
  - "^docker (ps|images|logs|inspect) "

ask_user:
  - "^rm "
  - "^git push"
  - "^sudo "
  - "^kubectl (apply|delete|patch|edit|scale|rollout|drain|cordon)"
  - "^docker (run|rm|rmi|exec)"
  - "^terraform (apply|destroy)"
  - "^helm (install|upgrade|uninstall)"
```

### Adding a domain

When you specialise the agent, add a matching rule pair (read-only
auto-allow + mutating ask):

```yaml
always_allow:
  - "^psql -c \"select"             # read-only Postgres
  - "^aws s3 ls"
ask_user:
  - "^psql -c \"(insert|update|delete|alter|drop)"
  - "^aws s3 (rm|cp|mv|sync) "
```

### Asker

The root binary uses `permissions.StdinAsker{}` which prompts on the
terminal. Implement `permissions.Asker` to integrate with a different
UI (web modal, Slack DM, etc.).

---

## `config/mcp_config.yaml`

Wires external [Model Context Protocol] servers as ADK toolsets. Each
entry spawns a child process and exposes its tools to the agent.

```yaml
servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    env: {}

  - name: kubernetes
    command: npx
    args: ["-y", "mcp-server-kubernetes"]
    env:
      KUBECONFIG: /home/you/.kube/config

  - name: postgres
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-postgres"
      - "postgresql://reader:pw@localhost/app"

  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "ghp_…"
```

### Fields

| Field     | Required | Notes                                                |
|-----------|----------|------------------------------------------------------|
| `name`    | yes      | Used as the toolset prefix in the agent.             |
| `command` | yes      | Executable to spawn (must be on `PATH`).             |
| `args`    | no       | Arguments passed to the command.                     |
| `env`     | no       | Environment variables added to the child process.    |

### Lifecycle

- Servers spawn at startup. If a server fails to start, it is logged
  and skipped — the agent continues with the rest.
- Servers are killed when the root binary exits.
- Tool names are namespaced as `<server>/<tool>` to prevent collisions.

### Security

Treat MCP servers as **untrusted code paths**: they receive arguments
from the LLM. Always pair an MCP server with `permissions.yaml` rules
gating its mutating verbs. The OOTB defaults already gate `kubectl
apply/delete`, `helm install`, `terraform apply`, etc.

[`regexp`]: https://pkg.go.dev/regexp
[Model Context Protocol]: https://modelcontextprotocol.io/

---

## Other runtime files

| File                  | Owner            | Purpose                                          |
|-----------------------|------------------|--------------------------------------------------|
| `.agent_events.log`   | `core/events`    | Append-only JSONL of every Before/After event    |
| `.agent_memory.md`    | `internal/compress` | Persistent memory snapshot when context is compressed |

Both are created in the working directory of the root binary.

## Environment variables (full list)

| Variable             | Used by               | Purpose                                          |
|----------------------|-----------------------|--------------------------------------------------|
| `GOAGENT_PROVIDER`   | `core/llm`            | Pick the LLM provider                            |
| `GOAGENT_MODEL`      | `core/llm`            | Override the per-provider default model id       |
| `GOOGLE_API_KEY`     | gemini provider       | Auth                                             |
| `GEMINI_API_KEY`     | gemini provider       | Auth (alias for `GOOGLE_API_KEY`)                |
| `ANTHROPIC_API_KEY`  | anthropic provider    | Auth                                             |
| `OPENAI_API_KEY`     | openai / openai_compat| Auth                                             |
| `OPENAI_BASE_URL`    | openai_compat         | API endpoint                                     |
| `REDIS_URL`          | `internal/teammates`  | Switch the mailbox backend to Redis              |
