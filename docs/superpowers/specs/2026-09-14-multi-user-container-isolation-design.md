# Multi-user omnis — milestone 1: per-user container isolation — design

**Status:** design approved, not implemented. **Shape:** omnis-server becomes one
more supervisord-managed service inside the per-user container the platform already
runs, behind the platform's existing OIDC gateway. Kernel-level isolation comes from
the container boundary; omnis gains a real per-session user id and a request-identity
check. No change to the agent core, the tools, MCP, LSP, hooks, or the terminal.

This is the first of three milestones toward a secure multi-user omnis. Sections 1–3
frame the whole programme; sections 4 onward specify milestone 1 only. Milestones 2
and 3 get their own specs.

## 1. Problem

omnis-server is single-user, and its Bearer token grants the rights of the Unix
account the server runs as:

- **Identity.** One shared `OMNIS_SERVER_TOKEN`. Every session carries the hard-coded
  `UserID = "web-user"` (`internal/sessions/sessions.go:20`). No route knows who is
  calling.
- **Execution.** The `Bash` tool runs `/bin/sh -c` as the server process's user
  (`core/tools/bash_unix.go:17`), with only `Setpgid`. The "safety floor" is a
  `strings.Contains` over three literals, and the permission layer addresses the
  *model*, not the user. Neither is a security boundary.
- **Unguarded host access.** The terminal PTY spawns the server's `$SHELL`; the `!`
  shell-escape, the Monaco save route, `GET /api/file`, the Files-panel operations
  (upload/delete/move/download) and lifecycle hooks all bypass the permission layer
  *by design* (token-only trust model, see CLAUDE.md). The token is therefore a shell
  on the host.
- **State.** One `$OMNIS_HOME` holds every session, config overlay, index and the MCP
  pool.

The requirement is a multi-user deployment where each user's environment is truly
partitioned and **script execution is genuinely confined to that user's environment**.
"Genuinely" rules out any rule inside omnis: the boundary must be enforced by the
kernel (Unix identity, namespaces, cgroups), not by instruction or by a permission
rule the model is asked to respect.

## 2. Context and constraints (from the requirements dialogue)

| Fact | Consequence |
|---|---|
| Users already reach a **per-user container** (Kubernetes, one pod per user) through an **existing OIDC gateway** that authenticates with the corporate SSO and routes each person to their container. | omnis does not implement OIDC and does not build a gateway. It plugs in as a service behind the gateway. |
| The container runs **supervisord**; services are launched **as the user's UID**; the user's **home is NFS-mounted**, created by an init service that supervisord starts first; UIDs are already assigned. | omnis-server is a supervisord program running as the user, with `$OMNIS_HOME` inside the NFS home. The user's environment *is* their real home. |
| **More than 200 active users.** | Nothing central may run all users' LLM turns in one Go process. Per-user processes ride an already-paid container. |
| **Shared features from day one**: team collections, shared sessions, user-to-user chat "like Slack" with `@omnis`. | A separate shared service (the *hub*) in later milestones. Milestone 1 must lay the two prerequisites it depends on: a real `UserID` on every session, and a verified request identity. |

## 3. Approaches considered

**A — one omnis-server per user inside their container, plus a shared hub (chosen).**
The container boundary gives kernel isolation for free, the agent core is untouched,
and all *new* code (the hub) has no host access by construction. Cost: one Go process
per active container, no shared stdio-MCP pool (HTTP MCP servers can be shared).

**B — one multi-tenant omnis-server, delegating every host operation by RPC to a
per-user executor pod (rejected).** Requires rewriting every layer that touches the
host (`core/tools`, `internal/lsp`, `internal/mcp`, `internal/hooks`, the terminal,
`/api/file`, Files ops, uploads), adds RPC latency to every file read, makes one
process a single point of failure for 200+ users, and carries the permanent risk that
one forgotten route breaks the isolation. omnis is also built around one process-wide
`Infrastructure`/`Manager`; it is not designed to run hundreds of users' turns in one
process.

**C — A without a hub: shared state via a ReadWriteMany volume, chat delegated to an
existing tool (Mattermost/Matrix) with a bridge (rejected for now).** Less code, but
ACLs on shared files are fragile and the user gets two UIs. Kept as a fallback for
milestone 3 if building chat proves too costly.

## 4. Milestone 1 — goals and non-goals

**Goals**

1. Every omnis process runs **as its user**, in that user's container, with all host
   access (tools, terminal, MCP stdio, LSP, hooks, file routes) confined by the
   container and the user's Unix rights.
2. omnis knows **which user it serves** and stamps that identity on every session,
   persisted, so later milestones can attribute and share sessions.
3. omnis **verifies the identity the gateway asserts** on every API request, so a
   misrouted or forged request is refused rather than served.
4. Deployment is reproducible: a supervisord program template, a reference container
   recipe, operator documentation, and packaging guard tests.

**Non-goals (milestone 1)**

- No OIDC/JWT handling inside omnis. Authentication stays in the gateway.
- No multi-user handling inside one process. One process = one user.
- No hub, no shared collections, no chat, no cross-container A2A/mailbox.
- No change to agents, squads, tools, permissions, MCP, LSP, hooks, or the terminal.
- No hardening of the Bash safety floor (tracked separately; it is a UX guard here).

## 5. Boundaries and threat model

```
browser ──HTTPS──▶ OIDC gateway (existing) ──▶ user's container
                     │ authenticates (SSO)         │ supervisord
                     │ routes user → container     │  ├─ home-init (first)
                     │ injects:                    │  └─ omnis-server (runAs UID)
                     │   Authorization: Bearer <T> │       $OMNIS_HOME = $HOME/.omnis (NFS)
                     │   X-Forwarded-User: <login> │       all tools/terminal/MCP/LSP/hooks
                     └─────────────────────────────┘       run as the user
```

- **Security boundary = the container + the user's UID.** Everything omnis executes
  inherits the user's rights on NFS and the container's namespaces, cgroups, seccomp
  and NetworkPolicy. A compromised omnis (prompt injection, a malicious skill) reaches
  exactly what the user could reach by logging into their container.
- **The gateway is the only intended entry.** It terminates SSO and injects two
  headers on every proxied request: the container's internal Bearer token and the
  authenticated login.
- **Residual threat omnis must cover itself:** a user who, from *their* container,
  reaches the omnis port of *another* container and forges the identity header.
  Defence in depth, two independent layers:
  1. **Token:** each container's omnis requires a per-container secret only the
     gateway knows (`OMNIS_SERVER_TOKEN`, generated by the platform). Forging the
     login header is useless without it.
  2. **Network:** a NetworkPolicy denying container→container traffic on the omnis
     port (platform-side, recommended; an example manifest ships as reference).
- **Identity check is a belt, the token is the braces.** The identity header check
  (§6) protects against the *gateway itself* misrouting (a bug, a misconfigured
  route). It is not the authentication.
- **What omnis's own guards are, and are not.** The Bash safety floor, the permission
  layer, and hooks remain *usability* guards against the model doing something the
  user did not intend. They are not part of the security boundary and this design
  does not rely on them.

## 6. Internal authentication and identity

### 6.1 Token (unchanged mechanism, new role)

`authMiddleware` (`server/auth.go`) is kept as is. The token becomes an **internal,
per-container secret**: the platform generates it, stores it where the gateway can
read it, and passes it to the container as `OMNIS_SERVER_TOKEN`. The gateway sets
`Authorization: Bearer <token>` on every proxied request.

Consequence for the web UI: the browser never holds a token. `apiFetch` only prompts
(`promptForToken`) on a 401, and a correctly proxied request never 401s, so **no web
UI change is needed**. (If a browser has a stale token in `localStorage` from an
earlier direct deployment, it sends its own `Authorization`; the gateway must be
configured to *set*, not *append*, the header so its value wins.)

### 6.2 Configured user identity

New setting **`OMNIS_USER_ID`** (env; `server.yaml` key `user_id` as the lower-
precedence form, following the existing env > yaml pattern). Set by the supervisord
program to the user's login. Empty ⇒ falls back to today's `"web-user"`, so every
existing deployment is byte-identical — **except** when an identity header is
enforced (§6.3), where an explicit value is mandatory and the fallback never applies.

At startup the server logs the effective user id and identity mode **when either
is configured**; an unconfigured install logs nothing (no-op contract).

### 6.3 Identity header check

New setting **`OMNIS_IDENTITY_HEADER`** (env; `server.yaml` key `identity_header`),
e.g. `X-Forwarded-User`. When non-empty, a new middleware — composed with
`authMiddleware` on the same `/api/*` group — enforces on every request:

| Condition | Response |
|---|---|
| header missing | `401 {"error":"missing identity header"}` |
| header present, value ≠ `OMNIS_USER_ID` (exact, case-sensitive after trim) | `403 {"error":"identity mismatch"}` |
| header present, value = `OMNIS_USER_ID` | pass |

When `OMNIS_IDENTITY_HEADER` is empty the middleware is not installed (no-op
contract). Setting `OMNIS_IDENTITY_HEADER` without an **explicitly configured**
`OMNIS_USER_ID` (env or yaml — the `"web-user"` fallback does not count) is a
startup error: comparing against a default that every container shares would let a
misrouted request through, which is exactly what the check exists to refuse.

Order: token check first, identity second — a caller without the secret learns
nothing about the expected login.

**Scope.** The terminal WebSocket route lives on the unauthenticated group by design
(browsers cannot set headers on a WS handshake) and is protected by its own
short-lived single-use token, minted over the *authenticated* `POST
/api/terminal/token` — which the identity middleware now covers. So the terminal is
protected transitively; no change. Static assets are not identity-checked.

**Fallback mode.** If the platform's gateway cannot inject an `Authorization`
header, the deployment runs with `OMNIS_SERVER_TOKEN` empty, `OMNIS_IDENTITY_HEADER`
set, and a strict NetworkPolicy. This is weaker (one layer instead of two) and the
operator documentation says so explicitly. It is not the recommended configuration.

### 6.4 `GET /api/whoami`

Returns `{"user_id": "<login>", "identity_enforced": true|false}`. The web UI shows
the login in the sidebar footer (i18n key `app.whoami`, en/fr/es/de; bump the
`app.js` `?v=` in `index.html`). Small, but it makes the isolation *visible*: a user
seeing someone else's login knows the routing is wrong before doing anything.

## 7. omnis-server as a supervisord program

### 7.1 Image contents

The existing `.deb` provides everything: binaries in `/usr/bin`, system config in
`/etc/omnis` (the default `paths.SystemConfigDir`, so **no config-path env var is
needed or allowed** — see the `OMNIS_CONFIG_PATH` gotcha in CLAUDE.md), and the web
UI in `/usr/share/omnis/web`. The reference `Dockerfile` installs the `.deb` on top
of a base image the platform owns; `python3` comes with the `.deb`'s dependencies
(required by the shipped Kubernetes hook, see "Distribution / packaging").

The image's `/etc/omnis/server.yaml` (a variant of `packaging/etc-omnis/server.yaml`)
sets:

```yaml
# addr is NOT set here: the listen address comes from OMNIS_SERVER_ADDR in the
# supervisord program (§7.2), so the platform owns it in one place.
token: ""                  # comes from OMNIS_SERVER_TOKEN, never from the file
open_browser: false
update_check: false        # the image is the update channel
a2a_enabled: false         # cross-container A2A is out of scope for milestone 1
identity_header: X-Forwarded-User   # platform's actual header name
```

### 7.2 Program definition

`packaging/supervisord/omnis-server.conf` (template; `${VAR}` placeholders are
rendered by the platform's container entrypoint):

```ini
; omnis-server as a supervisord program inside a per-user container.
;
; TEMPLATE — rendered with envsubst by the platform's container entrypoint
; before supervisord starts (supervisord itself does not expand its `user=`
; option, so the login has to be substituted into the file):
;
;   envsubst < omnis-server.conf > /etc/supervisor/conf.d/omnis-server.conf
;
; Variables the platform provides per container:
;   OMNIS_LOGIN         the user's login (also their Unix account in the image)
;   HOME                the user's NFS-mounted home
;   OMNIS_SERVER_TOKEN  the per-container secret the SSO gateway injects as
;                       "Authorization: Bearer <token>" on every proxied request
;   OMNIS_LISTEN_ADDR   the listen address inside the container. Two topologies:
;                         "127.0.0.1:8080" — the gateway proxies from a sidecar
;                           inside THIS pod; the NetworkPolicy example is then
;                           moot (nothing outside the pod can reach the port).
;                         "0.0.0.0:8080"   — the gateway reaches this pod over
;                           the cluster network; the NetworkPolicy example
;                           (packaging/container/networkpolicy.example.yaml) is
;                           what keeps every OTHER pod out, and its port must
;                           match this value.
;   OMNIS_BASE_PATH     URL prefix when the gateway routes by path; empty when
;                       it routes by host
;   LITELLM_API_KEY     the user's LiteLLM virtual key (per-user cost attribution)
;
; NOTE: supervisord applies Python %(...)s interpolation to every value in this
; file, and envsubst passes " through unescaped — so a token, LiteLLM key, or
; base path containing % or " will make the rendered program unparseable.
; Constrain OMNIS_SERVER_TOKEN and LITELLM_API_KEY to [A-Za-z0-9._-] (hex or
; base64url), or have the entrypoint escape % as %% before envsubst.
;
; Deliberately NOT set: OMNIS_CONFIG_PATH (bypasses the 3-layer config merge)
; and OMNIS_SYSTEM_CONFIG_DIR (the .deb already installs the system layer at
; /etc/omnis, which is the default). See docs/multi-user-containers.md.

[program:omnis-server]
command=/usr/bin/omnis-server
user=${OMNIS_LOGIN}
directory=${HOME}
priority=900
autostart=true
autorestart=true
startsecs=3
stopsignal=TERM
stopwaitsecs=30
stdout_logfile=/dev/stdout
stdout_logfile_maxbytes=0
stderr_logfile=/dev/stderr
stderr_logfile_maxbytes=0
environment=HOME="${HOME}",OMNIS_HOME="${HOME}/.omnis",OMNIS_USER_ID="${OMNIS_LOGIN}",OMNIS_SERVER_TOKEN="${OMNIS_SERVER_TOKEN}",OMNIS_SERVER_ADDR="${OMNIS_LISTEN_ADDR}",OMNIS_SERVER_BASE_PATH="${OMNIS_BASE_PATH}",OMNIS_WEB_DIR="/usr/share/omnis/web",LITELLM_API_KEY="${LITELLM_API_KEY}"
```

`supervisord` does not expand `%(ENV_…)s` in its `user=` option, so the template
uses `${VAR}` placeholders rendered by `envsubst` in the container entrypoint.
`OMNIS_SERVER_ADDR` is itself one such placeholder (`${OMNIS_LISTEN_ADDR}`),
never a value hard-coded in the template: a loopback default would either make
omnis unreachable behind a cluster-network gateway or render the
NetworkPolicy example moot, so the platform must pick the bind for its own
topology (see the `OMNIS_LISTEN_ADDR` comment above and
`docs/multi-user-containers.md`).

Rules the template must satisfy (guard-tested, §9):

- runs as the user (`user=${OMNIS_LOGIN}`), never as root;
- `OMNIS_HOME` is under `${HOME}`;
- sets `OMNIS_USER_ID` to `${OMNIS_LOGIN}`;
- **never** sets `OMNIS_CONFIG_PATH` (it would bypass the 3-layer merge and freeze
  every per-user override — the exact defect `packaging/profile_test.go` guards);
- does not set `OMNIS_SYSTEM_CONFIG_DIR` (the `.deb` layout is already the default);
- `OMNIS_SERVER_ADDR` is the rendered `${OMNIS_LISTEN_ADDR}`, never a hard-coded
  bind (a loopback default would contradict the NetworkPolicy example and the
  §5 threat model of one container reaching another's omnis port).

The `omnis-server start|stop|status` daemon subcommands are **not** used: supervisord
is the process manager and runs the foreground form.

### 7.3 Credentials and cost attribution

`models.json` already resolves `api_key`/`base_url` values as **environment-variable
names** (`resolveAPIKeyReference`). The system `models.json` references
`LITELLM_API_KEY`; the platform injects a **per-user LiteLLM virtual key**. Result:
per-user cost attribution on the LLM gateway with zero omnis code. The same applies to
the embedder key. A shared key works too, without attribution.

### 7.4 State, NFS, resources

- Everything mutable lives under `$HOME/.omnis` on NFS: sessions, preferences, config
  overlays, indexes, softskills, uploads. It survives container restarts, which is the
  point. `configedit.AtomicWriteFile` and the conversation writer use temp+rename in
  the same directory; this is safe on NFS.
- First run: `paths.Home()` creates `~/.omnis` lazily; no seeding step is needed
  (the `.deb` layer provides the system config). The pip launcher's seeding is not
  involved.
- Memory: recommend an embedding `dim` ≤ 1536 in the system `models.json` (go-turbovec
  allocates `O(dim²)` per index) and keep the default `OMNIS_SESSION_INDEX_IDLE`
  (10 min) so the session index is dropped when idle. CPU/memory ceilings come from
  the container's cgroups; the shipped `turn_budget` defaults stay.
- Lifecycle is the platform's: omnis runs as long as the container does. Idle
  stop/wake is a platform concern, not omnis's. The turn producer already survives a
  client disconnect and persists on `SIGTERM`, so a container stop mid-turn loses at
  most the in-flight generation, never persisted history.

### 7.5 Gateway prerequisites (operator doc)

- **Topology assumption**: the platform picks `OMNIS_LISTEN_ADDR` (§7.2) to match
  where its gateway actually runs. A gateway that is an in-pod sidecar proxies over
  loopback (`127.0.0.1:8080`) and the NetworkPolicy example below is moot (nothing
  outside the pod can reach the port regardless); a gateway that reaches the pod
  over the cluster network needs a non-loopback bind (`0.0.0.0:8080`) — otherwise
  omnis is simply unreachable — and the NetworkPolicy example is then what keeps
  every *other* pod out.
- Set (not append) `Authorization: Bearer <container token>` and the identity header.
- WebSocket upgrade on `/api/terminal/ws`.
- No response buffering and long idle timeouts on `GET /api/events` and the turn
  stream (`POST …/messages`, `GET …/messages/stream`); the client reconnects, but a
  gateway that buffers SSE breaks streaming entirely.
- If routing by path, set `OMNIS_SERVER_BASE_PATH` to the exact prefix the gateway
  forwards (it must forward the prefix, not strip it — `serveIndex` injects
  `window.BASE_PATH`).
- Optional (recommended when the bind is non-loopback): NetworkPolicy example
  (`packaging/container/networkpolicy.example.yaml`) allowing ingress on the
  omnis port only from the gateway's namespace/labels.

## 8. Code changes

All in `internal/sessions`, `server/`, `web/`, `packaging/`. Nothing in `agent/`,
`core/`, `internal/{mcp,lsp,hooks,…}`.

### 8.1 Real per-session user id (`internal/sessions`)

- Keep `const DefaultUserID = "web-user"` as the fallback *name*. Add:
  ```go
  func UserID() string      // configured user id, DefaultUserID when unset
  func SetUserID(id string) // called once at server boot, before any session exists
  ```
  `Registry.New`, `Registry.NewWithName` and every `server/` call site that currently
  reads `sessions.DefaultUserID` (scheduler, spawn, fork/rewind, export/import,
  `POST /sessions`, a2a auto-create) switch to `sessions.UserID()`. Mechanical; the
  TUI keeps `DefaultUserID`.
- `ConversationFile` gains `UserID string `json:"user_id,omitempty"``, written on
  every save. `LoadPersistedSessions` uses the file's `user_id` when present and
  `sessions.UserID()` otherwise (a legacy file belongs to the container's user — one
  container, one user).
- **Migration note.** Auxiliary per-session files are keyed by
  `agent.SessionSuffix(userID, sessionID)` (`agent_tasks_*`, `agent_todo_*`,
  `agent_memory_*`, `agent_statelog_*`, mailboxes). A pre-existing install that
  switches from `web-user` to a real login keeps its **conversation history intact**
  (`conversation_<id>.json` is keyed by id only) but its old-suffix auxiliary files
  become orphans and are swept by the GC. Acceptable: they are scratch state, and a
  fresh per-user deployment has none. Documented in the operator doc.

### 8.2 Identity middleware (`server/auth.go`, `server/config.go`, `server/main.go`)

- `ServerConfig` gains `UserID string `yaml:"user_id"`` and `IdentityHeader string
  `yaml:"identity_header"``; env `OMNIS_USER_ID` / `OMNIS_IDENTITY_HEADER` override
  (same resolution style as `OMNIS_SERVER_TOKEN`).
- `identityMiddleware(header, expected string) gin.HandlerFunc` per the table in
  §6.3; installed on the `auth` group right after `authMiddleware` when `header != ""`.
- Startup: `sessions.SetUserID(cfg)`; fail fast if `IdentityHeader != "" && UserID
  == ""`; log the effective values (never the token).
- `GET /api/whoami` on the `auth` group.

### 8.3 Web UI

- Sidebar footer shows the login from `/api/whoami` when `user_id != "web-user"` (a
  single-user install shows nothing — no-op contract). i18n keys in all four
  catalogues, `make i18n`, `?v=` bumps.

### 8.4 Packaging and docs

- `packaging/supervisord/omnis-server.conf` (§7.2).
- `packaging/container/Dockerfile` (reference: base image arg + `.deb` install +
  `server.yaml` overlay), `packaging/container/server.yaml`,
  `packaging/container/networkpolicy.example.yaml`.
- `docs/multi-user-containers.md`: operator guide (topology, gateway prerequisites,
  variables, fallback mode and its weaker guarantee, migration note, what omnis does
  *not* protect against).
- CLAUDE.md: new env vars in the table (`OMNIS_USER_ID`, `OMNIS_IDENTITY_HEADER`),
  a "Multi-user deployment (per-user containers)" section, `server.yaml` keys.
- `internal/features/FEATURES.md`: one bullet under the in-development minor
  ("Per-user identity: omnis-server can run as one user behind an SSO gateway and
  shows who you are").

## 9. Testing

- **`server/auth_test.go`** (table-driven, real router via `newAuthRouter`-style
  helper): identity disabled ⇒ untouched; header required + missing ⇒ 401; mismatch ⇒
  403; match ⇒ 200; token wrong + identity ok ⇒ 401 (token checked first); trim/case
  behaviour; `/api/whoami` payload.
- **`server/main` config resolution**: env overrides yaml for both keys; identity
  header without user id ⇒ startup error.
- **`internal/sessions`**: `SetUserID` flows into `New`/`NewWithName`;
  `ConversationFile` round-trip persists `user_id`; `LoadPersistedSessions` prefers
  the file's id and falls back to the configured one for a legacy file.
- **`server/`** round-trip: `POST /api/sessions` → registry `UserID` = configured →
  conversation file carries it → `LoadPersistedSessions` restores it.
- **`packaging/container_test.go`** (same precedent as `profile_test.go` and
  `hooks_assets_test.go`): the supervisord template runs as `${OMNIS_LOGIN}`,
  sets `OMNIS_HOME` under `HOME` and `OMNIS_USER_ID`, never mentions
  `OMNIS_CONFIG_PATH` or `OMNIS_SYSTEM_CONFIG_DIR`, and binds
  `OMNIS_SERVER_ADDR` to the rendered `${OMNIS_LISTEN_ADDR}` rather than a
  hard-coded loopback address; the container `server.yaml`
  has `update_check: false`, `open_browser: false`, `a2a_enabled: false`, empty
  `token`.
- **`server/identity_test.go`**: `GET /api/terminal/ws` is covered by the same
  identity middleware as the rest of `/api/*` (401 with no login, 403 with the
  wrong one) even though it sits outside the token-checking `auth` group; a
  request carrying the identity header twice is rejected as a mismatch even
  when the first value is correct.
- **Manual smoke** (documented in the operator guide): run two omnis-server processes
  as two Unix users on one host with different `OMNIS_USER_ID`, front them with a
  header-injecting proxy, verify `/api/whoami`, verify a forged header is refused,
  verify `!id` and the terminal report the right UID, verify a `Write` outside the
  user's home fails with a permission error.

## 10. Milestones 2 and 3 (out of scope here; what milestone 1 reserves)

- **Milestone 2 — hub: directory, team collections, shared sessions.** A separate
  service with its own database, no host access. omnis-server gains a *hub client*
  that publishes the sessions of a shared collection and reads teammates' sessions;
  `@omnis` in a shared context is answered by the **author's** container (the hub
  calls it through the gateway with the identity header), so a shared answer is
  always computed with the asker's rights. Milestone 1 provides its two prerequisites:
  a real `user_id` on every persisted session and a verified request identity.
- **Milestone 3 — user-to-user messaging.** Channels and DMs in the hub, real-time
  over SSE/WebSocket, UI inside the omnis web app. Build-vs-integrate (Matrix,
  Mattermost) is decided then, with approach C as the fallback.
- **Scale check.** Milestone 1 adds no central component; its cost is one Go process
  per active container, whose RSS is measured during milestone 1 and recorded here.
  The hub will be the only shared component, and it does neither LLM calls nor
  execution.

## 11. Open points to confirm with the platform team

1. The gateway can **set** an `Authorization` header per upstream (else: fallback
   mode, §6.3).
2. The exact identity header name it injects.
3. Routing mode: per-user host vs path prefix (decides `OMNIS_SERVER_BASE_PATH`).
4. Whether a NetworkPolicy denying container→container traffic on the omnis port can
   be applied.
5. Whether per-user LiteLLM virtual keys are available (cost attribution).
