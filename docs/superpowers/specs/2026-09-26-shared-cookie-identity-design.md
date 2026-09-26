# Shared omnis with delegated cookie identity — design

**Status:** design approved in conversation, not implemented.
**Scope:** the *test tier* of a shared, multi-user omnis-server deployed inside
the IA Parc platform (`test.iaparc.atoutlinux.net`, namespace `test-system`),
driving IA Parc through the `iaparc` skill (`iapcli`) with each user's own
platform identity. The *target tier* (restricted, shell-less operator) is named
in §9 and gets its own spec.

## 1. Goal

A user already logged in to IA Parc in the browser opens `/omnis/`, is recognised
without another login, and asks the agent to operate IA Parc ("list my
projects", "restart this production"). The agent runs `iapcli` **as that user**:
every action is authorised and audited by IA Parc under the user's own login.

IA Parc's browser session puts a JWT in a cookie (`iaparc_token`, or
`auth._token.local`). `iapcli` reads a token from `IAPCLI_TOKEN` (verified:
viper `AutomaticEnv`; a garbage value is rejected as "token is malformed", i.e.
consumed). The login is the JWT's `auth_user` claim.

## 2. Relationship to milestone 1 and security posture

Milestone 1 (`2026-09-14-multi-user-container-isolation-design.md`) chose one
omnis-server **per user, per container**, because omnis hands out a host shell
(`Bash`, `!`, the terminal, file routes, MCP stdio, hooks) under the process's
Unix account. That remains the only design with real isolation.

This spec deliberately adds a different, **opt-in** mode: **one omnis-server
shared by several users**, identified per request. Decided with the user:

| Tier | Capability | Isolation between users |
|---|---|---|
| **Test (this spec)** | full omnis (option 2) | UI/API-level only — sessions, events, preferences, collections are scoped per user. **No** OS-level isolation: same UID, shared `$OMNIS_HOME`, a determined user (or their agent) can read another user's files and, via `/proc`, another user's in-flight token. **Trusted users only.** |
| **Target (next spec)** | restricted IA Parc operator (option 1): no generic shell, an `iapcli` tool that injects the token without exposing it | real, because the agent no longer has a shell |

The test tier is built so the target tier only *removes and restricts*; it must
not require rework of identity, scoping or token plumbing.

Mode exclusivity: `identity_mode: cookie` is **incompatible** with milestone 1's
`OMNIS_USER_ID` / `OMNIS_IDENTITY_HEADER`. Setting both is a fatal startup error.
Without `identity_mode` every behaviour is byte-identical to today.

## 3. Identity (`internal/identity`)

Nothing IA Parc-specific enters Go code; IA Parc appears only in deployment
config.

### 3.1 Configuration

Each variable has a `server.yaml` equivalent (env wins, as elsewhere).

| Variable | `server.yaml` | Meaning | IA Parc value |
|---|---|---|---|
| `OMNIS_IDENTITY_MODE` | `identity_mode` | `cookie` enables this mode; empty = off | `cookie` |
| `OMNIS_AUTH_COOKIES` | `auth_cookies` | comma list of cookie names, tried in order | `iaparc_token,auth._token.local` |
| `OMNIS_AUTH_VALIDATE_CMD` | `auth_validate_cmd` | validator command, split into argv (shell-word rules, **no shell executed**); exit 0 = token accepted | `iapcli user get` |
| `OMNIS_AUTH_TOKEN_ENV` | `auth_token_env` | env var name that carries the token — to the validator and to tools (§5) | `IAPCLI_TOKEN` |
| `OMNIS_AUTH_LOGIN_FIELD` | `auth_login_field` | dotted path of the login in the validator's stdout (JSON, else YAML) | `login` (confirm in spike, §8) |
| `OMNIS_AUTH_ROLES_FIELD` | `auth_roles_field` | dotted path of the roles list (optional) | `roles` |
| `OMNIS_AUTH_ADMIN_ROLES` | `auth_admin_roles` | comma list of roles granting admin (used by the target tier) | `admin` |
| `OMNIS_AUTH_LOGIN_URL` | `auth_login_url` | returned with a 401; `{return}` is replaced by the URL-encoded omnis URL | `https://test.iaparc.atoutlinux.net/sso/login` (confirm) |
| `OMNIS_AUTH_CACHE_TTL` | `auth_cache_ttl` | max validation cache lifetime | `15m` (default) |

Fatal at startup when `identity_mode: cookie` and any of: `OMNIS_USER_ID` or
`OMNIS_IDENTITY_HEADER` set; `auth_cookies`, `auth_validate_cmd`,
`auth_token_env` or `auth_login_field` empty; the validator binary not found on
`PATH`.

### 3.2 Types

```go
type Identity struct {
    Login string
    Roles []string
    Token string // never logged, never persisted
}

type Validator interface {
    Validate(ctx context.Context, token string) (Identity, error)
}
```

`CommandValidator` is the only implementation. It runs the argv with a 10 s
timeout, environment = the process environment plus `<auth_token_env>=<token>`,
parses stdout as JSON (fallback YAML), extracts the login and roles by dotted
path. Distinct errors: `ErrRejected` (non-zero exit, or empty login) vs
`ErrUnavailable` (timeout, exec failure). A validator HTTP implementation may be
added later behind the same interface; not built now.

### 3.3 Cache

Keyed by SHA-256 of the token. A positive entry lives until
`min(JWT exp if the token decodes as a JWT, now + auth_cache_ttl)`; the JWT is
only *decoded* for `exp`, never trusted for identity. A rejection is cached 30 s
so a bad cookie cannot hammer IA Parc. `ErrUnavailable` is not cached.
Concurrent validations of the same token are single-flighted.

### 3.4 Middleware `cookieIdentity`

Replaces `authMiddleware` + `identityMiddleware` on the `/api/*` group in cookie
mode (the bearer token is not used in this mode):

1. Read the first present cookie of `auth_cookies`; strip a leading `Bearer `
   (case-insensitive) and URL-decode.
2. None → **401** `{"error":"not authenticated","login_url":…}`.
3. Validate (cache first). `ErrRejected` → **401** as above;
   `ErrUnavailable` → **503** `{"error":"identity provider unavailable"}`.
4. On success: record the token in the `TokenStore` (§5.1), store `Identity` in
   the gin context and in the request `context.Context`
   (`identity.WithIdentity` / `identity.From`).

The terminal WebSocket (`GET /api/terminal/ws`) applies the same check on the
handshake cookies; the short-lived terminal token mechanism is bypassed in this
mode (browsers do send cookies on a same-origin WS handshake). `CheckOrigin`
same-origin stays.

`GET /api/whoami` returns `{user_id: <login>, identity_mode: "cookie"}`. The web
UI shows it in the existing "Signed in as" footer. On a 401 carrying
`login_url`, the web UI navigates the top window there (so a cookie expiry mid-use
lands the user on the platform login and back).

Tokens never appear in logs, the event audit log, error messages or persisted
files. Only the hash is used as a cache key.

## 4. Per-user scoping

Rule: **a session belongs to one login**, stored in `ConversationFile.UserID`
(existing field). Everything derived from a session inherits its owner.

| Item | Scope | Mechanism |
|---|---|---|
| Sessions (active, archived, hidden) | per user | `user_id` = request login at creation (create, fork, import, spawn, schedule); `Registry` exposes the owner |
| `/api/sessions/:id/*` | per user | an ownership middleware on the `:id` group returns **404** (not 403) when `owner != login` |
| Lists: `GET /sessions` (paginated or not), `/session-ids`, `/search/sessions` (scan and semantic, filtered at query time), collection counts | per user | filter by owner |
| `/api/events` | per user | each subscriber carries its login; a session-bearing event is delivered only to that session's owner; session-less events only if global by nature (`update_available`, config reload) — `collections_changed` and `schedule_changed` carry the login and go to that user only |
| Pending `ask_user` replay on connect | per user | filtered by session owner |
| Collections (`collections.json`, `collections/<name>/`) | per user | rooted at `$OMNIS_HOME/users/<login>/` |
| Preferences (`preferences.json`) | per user | `$OMNIS_HOME/users/<login>/preferences.json` |
| `/loop`, `/schedule` jobs | per user | `Job.Owner`; list/edit/delete/run filtered; sessions a job creates belong to its owner |
| Default working dir, session-less Folders panel (`/api/folder*`) | per user | root `$OMNIS_HOME/users/<login>/work/` (created on demand) instead of the process cwd; the global browse cwd becomes per login |
| Uploads | per session | unchanged |
| Agent config (agents, models, permissions, hooks, MCP, A2A, registries), soft-skills, precedents, docs index | **shared** | unchanged |

`<login>` is sanitised into a path segment (reject separators, `.`/`..`;
lowercase; non-`[a-z0-9._@-]` → `_`).

The process-global `sessions.UserID()` is not consulted in cookie mode: request
paths use `identity.From(ctx)`; request-less paths (scheduler fires, durable
question resume, mailbox/background injection, spawned tasks) use the owner
recorded on the session or job. Inbound A2A is **disabled** in cookie mode (it
carries no user identity); `a2a_enabled: true` with `identity_mode: cookie` is a
fatal startup error.

Known and accepted in the test tier:
- **Settings edits are global.** Any user can change the shared agent config.
  The target tier restricts Settings to `auth_admin_roles`.
- **No OS-level isolation** (§2). The scoping above protects the UI, not against
  a user's own agent reading the shared filesystem.

## 5. Token delivery to tools

The token is never written to disk: not `~/.iapcli.yaml`, not a conversation,
not a log.

### 5.1 `identity.TokenStore`

Process memory, `login → {token, exp}`, updated by every validated request, so
a cookie renewed by IA Parc replaces the old token naturally. Used by turns with
no HTTP request behind them (scheduler, spawn, mailbox, background
notifications, durable-question resume), which look up the **session owner's**
latest non-expired token. Lost on restart by design.

### 5.2 Injection

- Every turn entry point (interactive `handleMessages`, `injectTurnRouted`)
  plants `identity.WithToken(ctx, token)` beside `WithCwd` / `WithSteerSession`;
  it propagates into sub-agents by the same path.
- The server turns the token into a generic shell-environment entry
  (`<auth_token_env>=<token>`) and plants it with `fstools.WithShellEnv(ctx,
  env)`. `core/tools` knows nothing about identity: the `Bash` handler copies
  `ShellEnvFrom(ctx)` into `BashIn.Env`, `RunBashInteractive` (the `!`
  shell-escape) reads it from its context, and `bash_background` / `monitor`
  pass it to the background queue. The rest of the environment is inherited as
  today. No planted env ⇒ nothing is injected (CLI/TUI/single-user unchanged).
- `run_tests` is deliberately **not** injected: it shares `RunShellCaptured`
  with the hooks engine, which must never receive the token.
- Terminal: injected into the PTY environment at spawn, with the handshake's
  token. A long-lived shell keeps that token; opening a new terminal tab picks
  up the current one.
- **Not injected** into hooks (admin-authored shared config), MCP stdio servers
  (one shared process pool), LSP servers.

### 5.3 Missing or expired token

A turn keeps the token it started with. With no token available (background
turn after a restart, expired token) the variable is **not** set: `iapcli`
fails with its own message and the agent reports it. There is **no fallback to
a service identity** — a turn must never act on IA Parc as anyone else.

### 5.4 Accepted exposure (test tier)

The agent can read its own token (`env`), so it can reach the transcript and the
LLM context. It is the user's own token. The target tier replaces `Bash` with an
`iapcli` tool that injects the token without exposing it.

## 6. LLM

Test tier: one shared key (a `Secret` referenced by env-var name from
`models.json`), shared `models.json`. Target tier: a per-user LiteLLM virtual
key on the IA Parc gateway (`iapcli gateway keys`), charged to the user's
budget — out of scope here.

## 7. Deployment (`test-system`)

Manifests under `packaging/k8s/iaparc-test/`.

- **Image** `iaparc/omnis-server:dev-<git describe>`, pushed to the Docker Hub
  `iaparc` org from the local `docker login` (verify the repo is private before
  the first push — the image embeds the IA Parc skills). Contents: `omnis-server`,
  `omnis`, `/etc/omnis` (config + registry + hooks), web assets, `iapcli`
  0.25.2, `kubectl`, `helm`, `python3`, `/etc/agentskills/skills/iaparc/`.
  Non-root dedicated user.
- **ConfigMap** `omnis-config`: `OMNIS_IDENTITY_MODE=cookie`, the `OMNIS_AUTH_*`
  values of §3.1, `OMNIS_SERVER_BASE_PATH=/omnis`, `OMNIS_HOME=/data`,
  `update_check: false`, `open_browser: false`, `a2a_enabled: false`; and
  `/etc/iapcli/.iapcli.yaml` (context pointing at the in-cluster gRPC endpoint,
  empty token).
- **Secret** `omnis-llm`: the shared LLM key.
- **PVC** `omnis-data` (`nfs-provisioner`) at `/data`.
- **Deployment** `omnis`, 1 replica (state is in-process + one volume; no
  horizontal scaling), `imagePullSecrets: iapregistrykey`, **no** privileged
  ServiceAccount token (omnis has no Kubernetes rights of its own; it drives IA
  Parc through `iapcli` with the user's token).
- **Service** + **Ingress** `/omnis/` with the same external-auth annotations as
  `iaparc-portal-ingress` (`auth-url: http://iaparc-go-api.test-system.svc.cluster.local:9080/sso/verify/`,
  `auth-signin: https://test.iaparc.atoutlinux.net/sso/login`) and 3600 s
  proxy timeouts (SSE + terminal WS). Two layers: nginx rejects/redirects
  upstream; omnis revalidates on its own, so a misconfigured ingress still
  leaves omnis closed.

## 8. Testing and validation

**Unit (in `make test`):**
- `internal/identity`: cookie extraction (order, `Bearer` prefix, URL-encoding,
  absent); `CommandValidator` against a fake binary (JSON and YAML output,
  dotted paths, non-zero exit, timeout, missing binary); cache (hit, TTL capped
  by `exp`, 30 s negative cache, single-flight); tokens absent from logs and
  errors.
- Server, real gin router, fake validator knowing `alice` and `bob`:
  - `bob` gets **404** on every `/api/sessions/:id/*` route of an `alice`
    session — the test **enumerates the registered routes**, so a future route
    added without the ownership check fails it;
  - lists (sessions, ids, search, collections, schedules) show only the caller's
    data;
  - `/api/events`: `bob` receives nothing of `alice`'s sessions, including the
    pending `ask_user` replay;
  - terminal WS refused without a valid cookie;
  - fatal startup combinations (§3.1, §4 A2A).
- `core/tools`: the planted env is set on `Bash`, the `!` escape and
  background tasks when the context carries it, absent otherwise; never set on
  hooks or `run_tests`.
- **No-op contract:** with no `identity_mode`, the whole existing suite passes
  unmodified, including milestone 1's tests.

**On the cluster, in order:**
0. **Spike, before any code:** the user logs in to IA Parc in the browser; check
   that `IAPCLI_TOKEN=<cookie value> iapcli user get` succeeds and record its
   output shape (login and roles fields). If the cookie token is not accepted
   by the gRPC API, §3 is revised before continuing.
1. Deploy; without a cookie `/omnis/` redirects to the IA Parc login.
2. Logged in as `blouargant@chapsvision.com`: "Signed in as" shows the login;
   ask "list my IA Parc projects" — the agent loads the `iaparc` skill and
   `iapcli` answers under that identity.
3. With a second IA Parc account: sessions and events are separated in the UI.

## 9. Out of scope (target tier, next spec)

- Restricted mode: no terminal, `!`, `Bash`, file routes or config editing for
  non-admins; an `iapcli` tool (argv, no shell) injecting the token without
  exposing it to the agent.
- Per-user LiteLLM virtual keys.
- Settings restricted to `auth_admin_roles`.
- Per-user soft-skills / precedents.
- An HTTP validator implementation.

## 10. Documentation

CLAUDE.md: new section "Shared deployment (cookie identity)", new env vars in
the table, `internal/identity` in Key packages. `docs/` operator guide for the
IA Parc test deployment. `internal/features/FEATURES.md`: one bullet under the
in-development minor.
