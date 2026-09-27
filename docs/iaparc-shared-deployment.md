# Shared omnis on IA Parc (cookie-identity, test tier)

This guide covers deploying **one shared `omnis-server`** into the IA Parc
`test-system` namespace so several IA Parc users can drive their own IA Parc
projects through the agent, each recognised from their existing IA Parc
browser login — no separate omnis login.

This is the **test tier** described in
[docs/superpowers/specs/2026-09-26-shared-cookie-identity-design.md](superpowers/specs/2026-09-26-shared-cookie-identity-design.md).
Read the "Shared deployment (cookie identity)" section of
[CLAUDE.md](../CLAUDE.md) first for how the mechanism actually works
(`internal/identity`, the ownership guard, per-user state, token delivery).
This document is about *operating* it, not how it's built.

## What this mode is

- **One process, many users.** Every browser request carries the platform's
  own session cookie; omnis validates it against IA Parc and acts as the
  named user for that request. There is no `OMNIS_SERVER_TOKEN` in this mode
  and no per-user container — that's milestone 1
  ("Multi-user deployment (per-user containers)" in CLAUDE.md), a different,
  mutually-exclusive mode.
- **The agent runs `iapcli` (and any other shell command) as the calling
  user.** IA Parc sees and audits every action under that user's own login,
  not a shared service account.
- **Full omnis, not a restricted one.** This tier keeps `Bash`, `!`, the
  terminal, file routes, and config editing available to every user — see
  "Security posture" below before deciding whether that's acceptable for a
  given audience.

## Security posture — trusted users only

This is **UI/API-level isolation, not OS-level isolation**. Every user's
requests are served by the same Unix account, on the same filesystem, in the
same process:

- A determined user's agent can read another user's files under
  `$OMNIS_HOME`, and — via `/proc` — another user's in-flight platform token
  while a turn of theirs is running.
- Settings edits are **global**: any user can change the shared agent/model
  config.
- The agent can print its own token (`env`) into the chat transcript.
- The Helper's `set_preference` / `get_settings(preferences)` still read and
  write the **shared** preferences file, not the caller's own (Settings is
  global in this tier).
- **MCP servers are shared, including their `${input:…}` credentials.** A
  prompt for an MCP input (or an MCP/skill dependency install) is shown to —
  and answerable by — the user whose chat triggered it, even when a sub-agent
  raised it, and a typed answer is cached per user. But the MCP server process
  itself is one per configuration for the whole server: the first user to
  connect an input-templated server supplies the credential it runs with, and
  every other user's calls to that server use it. Don't use `${input:…}` for
  per-user credentials in this tier.

What *is* scoped per user: sessions, schedules, collections, preferences set
from the UI, working directories (new, imported and scheduled sessions start
in the user's own `work/` directory), `/api/events` (including pending and
live `ask_user` questions — and answering one is refused unless you own its
session), the teammate mailbox (an agent can neither list nor message another
user's sessions, and a cross-user message is dropped rather than run), and the
agent's past-session search tools (`search_sessions`/`read_session`/
`list_sessions`). Logins are compared trimmed and lower-cased.

Deploy this only for a namespace of users who are already trusted with shell
access on the platform they're driving (IA Parc project operators), not for a
general public audience. The restricted "target tier" (no shell, an `iapcli`
tool that injects the token without exposing it, per-user LLM keys, Settings
gated to admin roles) is a separate, not-yet-built design — see CLAUDE.md's
"Shared deployment (cookie identity)" section and spec §9 for what it removes.

## Configuration (IA Parc values)

Set these as environment variables (or the matching `server.yaml` keys —
env wins). Full descriptions are in CLAUDE.md's environment-variable table.

| Variable | IA Parc value |
|---|---|
| `OMNIS_IDENTITY_MODE` | `cookie` |
| `OMNIS_AUTH_COOKIES` | `iaparc_token,auth._token.local` |
| `OMNIS_AUTH_VALIDATE_CMD` | `iapcli user get` |
| `OMNIS_AUTH_TOKEN_ENV` | `IAPCLI_TOKEN` |
| `OMNIS_AUTH_LOGIN_FIELD` | `login` (**unconfirmed** — see "Prerequisite" below) |
| `OMNIS_AUTH_ROLES_FIELD` | `roles` |
| `OMNIS_AUTH_ADMIN_ROLES` | `admin` (parsed today, not yet enforced by this tier) |
| `OMNIS_AUTH_LOGIN_URL` | `https://test.iaparc.atoutlinux.net/sso/login` (**unconfirmed**) |
| `OMNIS_AUTH_CACHE_TTL` | `15m` (the default — can be omitted) |

Also set for this deployment: `OMNIS_SERVER_BASE_PATH=/omnis`,
`OMNIS_HOME=/data`, `update_check: false`, `open_browser: false`,
`a2a_enabled: false` (inbound A2A carries no per-user identity and is a fatal
combination with `identity_mode: cookie`). `iapcli` itself needs its own
`/etc/iapcli/.iapcli.yaml` pointing at the in-cluster gRPC endpoint with an
**empty** token (the per-request token comes from `IAPCLI_TOKEN`, injected by
omnis — never a token baked into that file).

Starting the server with `identity_mode: cookie` set but `OMNIS_USER_ID` or
`OMNIS_IDENTITY_HEADER` also set, or with `a2a_enabled: true`, is a **fatal
startup error** — those combinations are mutually exclusive by design.

## Validated: the platform cookie works with `iapcli` (2026-09-27)

The spike (spec §8 step 0) was run against `test.iaparc.atoutlinux.net`:
`IAPCLI_TOKEN=<iaparc_token cookie> iapcli -C <read-only config> user get`
exits 0 and prints the profile as **JSON**, with the login at `login` and the
roles at `roles` (a list, e.g. `["admin","rd","prod"]`) — the values used in
this guide. The read-only config file is left untouched and no
`~/.iapcli.yaml` is created. This requires **`iapcli` ≥ 0.25.3**: 0.25.2 tried
to write the token back into the config file and failed on a read-only mount.
`OMNIS_AUTH_LOGIN_URL` is still unverified until the first deployment.

## Build, push, deploy

The Kubernetes manifests and image build for this deployment live under
`packaging/k8s/iaparc-test/` (Task 12 of the implementation plan). **That
directory and its README are the source of truth for the actual
build/push/deploy commands** — this guide intentionally does not restate or
invent them here, since they had not been written at the time this section
was documented. See that README for:

- building and pushing the `iaparc/omnis-server:dev-<git describe>` image
  (contents: `omnis-server`, `omnis`, `/etc/omnis`, web assets, `iapcli`,
  `kubectl`, `helm`, `python3`, `/etc/agentskills/skills/iaparc/`, running as
  a non-root dedicated user),
- the `omnis-config` ConfigMap and `omnis-llm` Secret,
- the `omnis-data` PVC,
- the `omnis` Deployment (1 replica — state is in-process plus one volume, no
  horizontal scaling; no privileged ServiceAccount token, since omnis drives
  IA Parc entirely through `iapcli` under the calling user's own token) and
  Service/Ingress (mounted at `/omnis/`, with the same external-auth
  annotations as `iaparc-portal-ingress` and long proxy timeouts for SSE and
  the terminal WebSocket).

## Verifying the deployment

These are the same checks as the implementation plan's Task 13
verification pass:

1. **Unauthenticated access redirects.** Without an IA Parc session cookie,
   opening `/omnis/` should redirect to the IA Parc login rather than
   showing the bearer-token prompt or a raw 401.
2. **Logged in, the agent acts as you.** Log in to IA Parc as a real user
   (e.g. `blouargant@chapsvision.com`), open `/omnis/`, and confirm the
   sidebar footer shows "Signed in as `<your login>`". Ask something like
   "list my IA Parc projects" — the agent should load the `iaparc` skill and
   `iapcli` should answer scoped to your own IA Parc identity, not a shared
   service account.
3. **Two users don't see each other's chats.** Log in as a second IA Parc
   account (a different browser/profile) and confirm sessions, the session
   list, and live events (`/api/events`) are fully separated — including that
   a pending agent question in one user's session is never visible to the
   other.
4. **Ownership holds under direct API probing**, not just the UI: a request
   for another user's session id anywhere under `/api/sessions/:id/...` (or
   via a `session=` query parameter) should come back **404**, not the other
   user's data.

If any of these fail, re-check the `OMNIS_AUTH_*` configuration above first
(especially `OMNIS_AUTH_LOGIN_FIELD` per the spike prerequisite) before
suspecting the ownership/scoping code itself — a validator that resolves the
wrong field silently produces no login, which `cookieIdentityMiddleware`
treats as "not authenticated" rather than falling back to a shared identity.
