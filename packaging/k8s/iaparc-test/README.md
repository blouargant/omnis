# omnis-server on the IA Parc test platform (shared, cookie identity)

Deploys a single shared `omnis-server` into the `test-system` namespace,
authenticated through the IA Parc portal's SSO cookie (`OMNIS_IDENTITY_MODE=cookie`)
instead of a per-container bearer token. See `docs/multi-user-containers.md` and
the design spec under `.superpowers/sdd/2026-09-26-shared-cookie-identity/` for
the identity model this deployment exercises.

## Prerequisites

- `KUBECONFIG` pointing at the test cluster (`~/kubeconfig-milkyway-tests`).
- Docker logged in to Docker Hub for the `iaparc` org (`docker login`).
- `iapcli` installed locally (staged into the image by `build.sh`).
- The `iaparc` skill checked out at `/etc/agentskills/skills/iaparc` (staged
  into the image's shared Agent-Skills registry layer).
- `OPENAI_API_KEY` / `OPENAI_BASE_URL` for the LLM gateway this deployment
  should use.

## Deploy

```bash
export KUBECONFIG=~/kubeconfig-milkyway-tests
IMAGE=$(packaging/k8s/iaparc-test/build.sh | tail -1)
kubectl -n test-system create secret generic omnis-llm \
  --from-literal=api_key="$OPENAI_API_KEY" --from-literal=base_url="$OPENAI_BASE_URL" \
  --dry-run=client -o yaml | kubectl apply -f -
sed "s|IMAGE_PLACEHOLDER|$IMAGE|" packaging/k8s/iaparc-test/omnis.yaml | kubectl apply -f -
kubectl -n test-system rollout status deploy/omnis
```

The app is then reachable at `https://test.iaparc.atoutlinux.net/omnis`,
gated by the portal's `nginx.ingress.kubernetes.io/auth-url` SSO check (the
same annotation pattern as `iaparc-portal-ingress`).

## What `build.sh` does

`packaging/k8s/iaparc-test/build.sh` runs `make build` for a static
(`CGO_ENABLED=0`) `bin/omnis`/`bin/omnis-server`, stages `iapcli` and the
`iaparc` skill into `.build/` (git-ignored — never commit it), builds
`iaparc/omnis-server:dev-<git-describe>` from the repo root using
`packaging/k8s/iaparc-test/Dockerfile`, and (unless `PUSH=0`) pushes it. It
prints the built image tag as its last line of stdout, which the deploy
sequence above captures into `$IMAGE`.

To build locally without pushing:

```bash
PUSH=0 packaging/k8s/iaparc-test/build.sh
```

## Identity mode — what this deployment is NOT

This is a **shared** server behind the platform's SSO gateway, not a
per-user container (see "Multi-user deployment (per-user containers)" in
`CLAUDE.md` for that alternative). `OMNIS_IDENTITY_MODE=cookie` reads the
login/roles out of the portal's session cookie by shelling out to
`iapcli user get` (via `OMNIS_AUTH_VALIDATE_CMD`), rather than relying on a
per-container bearer token or an `X-Forwarded-User` header. There is
therefore:

- **no** `OMNIS_SERVER_TOKEN` / bearer token in this config — the cookie
  check on every request is the auth boundary instead;
- **no** `OMNIS_CONFIG_PATH` / `OMNIS_USER_ID` — sessions are keyed off the
  login resolved from the cookie, not a single fixed account;
- **no** ServiceAccount token mounted into the pod
  (`automountServiceAccountToken: false`) and **no** A2A server
  (`a2a_enabled: false`) — this deployment has no business talking to the
  Kubernetes API or accepting inbound A2A calls.

## Open item — Task 0 spike not yet run

`OMNIS_AUTH_LOGIN_FIELD` (`"login"`) and `OMNIS_AUTH_ROLES_FIELD` (`"roles"`)
in `omnis.yaml`'s ConfigMap are the plan's assumed `iapcli user get` output
field names. **The plan's Task 0 spike — confirming those field names against
a real `iapcli user get` call with a live cookie token — has not been run.**
Re-verify before relying on role-gated (`OMNIS_AUTH_ADMIN_ROLES`) behavior in
this deployment; the ConfigMap carries a comment flagging this.

## Cluster values used (read-only `kubectl get` against `test-system`)

- **Ingress class**: `nginx` (`kubectl get ingressclass` — the only class in
  the cluster; `iaparc-portal-ingress` omits `ingressClassName` and relies on
  it being the default, but this manifest sets it explicitly).
- **TLS**: copied verbatim from `iaparc-portal-ingress` —
  `secretName: iaparc-system-certificate` for host `test.iaparc.atoutlinux.net`.
- **SSO annotations**: `auth-url`/`auth-signin`/`auth-response-headers` copied
  from `iaparc-portal-ingress` (`kubectl -n test-system get ingress
  iaparc-portal-ingress -o yaml`); `proxy-read/send-timeout` and
  `proxy-body-size` added for this app's longer streaming turns and file
  uploads.
- **gRPC operator service**: `iaparc-go-operator.test-system.svc.cluster.local`
  exists with a named `grpc` port `50051` (`kubectl -n test-system get svc
  iaparc-go-operator -o yaml`), but whether that in-cluster port terminates
  TLS with the SNI `iapcli` expects is unconfirmed (no live cookie token to
  test with, and the plan's Task 0 spike is pending). `.iapcli.yaml` in this
  manifest therefore uses the **public** endpoint —
  `address: test.iaparc.atoutlinux.net`, `port: "443"`, `insecure: ""` —
  matching the pattern in the dev machine's own `~/.iapcli.yaml` context
  (verified working there, minus its token, which was never copied here).
  Switching to the in-cluster service address is a follow-up once Task 0
  confirms it works.
