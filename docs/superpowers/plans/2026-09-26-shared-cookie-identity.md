# Shared omnis with delegated cookie identity — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one omnis-server be shared by several IA Parc users: each request is identified by the platform's session cookie (validated by running `iapcli user get`), each user sees only their own sessions/events/preferences/collections, and the agent's shell tools run `iapcli` with that user's token.

**Architecture:** A new opt-in server mode `identity_mode: cookie`. A new package `internal/identity` (cookie extraction, command validator, validation cache, in-memory token store, context helpers) is consumed by a `cookieIdentity` gin middleware that replaces the bearer-token check. Session ownership reuses the existing `user_id` field; a route-wide ownership guard returns 404 across users; list/event paths filter by owner. The token reaches shell tools through a generic context value (`fstools.WithShellEnv`) planted at turn start. Without `identity_mode` every path is byte-identical to today.

**Tech Stack:** Go 1.26.6, gin, `gopkg.in/yaml.v3`, `golang.org/x/sync/singleflight`, vanilla JS web UI, Docker, Kubernetes (ingress-nginx external auth).

**Spec:** `docs/superpowers/specs/2026-09-26-shared-cookie-identity-design.md`

## Global Constraints

- `identity_mode` values: `""` (off, default) or `cookie`. Anything else is a fatal startup error.
- Fatal at startup in cookie mode when: `OMNIS_USER_ID`/`user_id` or `OMNIS_IDENTITY_HEADER`/`identity_header` is set; `auth_cookies`, `auth_validate_cmd`, `auth_token_env` or `auth_login_field` is empty; the validator binary is not on `PATH`; `a2a_enabled` is true.
- Env vars (env wins over `server.yaml`): `OMNIS_IDENTITY_MODE`, `OMNIS_AUTH_COOKIES`, `OMNIS_AUTH_VALIDATE_CMD`, `OMNIS_AUTH_TOKEN_ENV`, `OMNIS_AUTH_LOGIN_FIELD`, `OMNIS_AUTH_ROLES_FIELD`, `OMNIS_AUTH_ADMIN_ROLES`, `OMNIS_AUTH_LOGIN_URL`, `OMNIS_AUTH_CACHE_TTL` (default `15m`).
- Validator: argv, **no shell**, 10 s timeout, env = process env + `<auth_token_env>=<token>`, stdout parsed JSON then YAML.
- Cache: key = SHA-256(token); positive TTL = `min(JWT exp, now + auth_cache_ttl)`; negative (rejected) = 30 s; unavailable = not cached; single-flight per key.
- HTTP: no cookie / rejected → 401 `{"error":"not authenticated","login_url":<auth_login_url>}`; validator unavailable → 503 `{"error":"identity provider unavailable"}`; cross-user session/schedule access → 404.
- The token is never logged, persisted, or put in an error message. Not injected into hooks, MCP, LSP or `run_tests`.
- Nothing IA Parc-specific in Go code (names, cookies, command live in deployment config only).
- No-op contract: with `identity_mode` unset, the full existing test suite passes unmodified.
- Kubernetes: namespace `test-system`, image `iaparc/omnis-server:dev-<git describe>`, pull secret `iapregistrykey`, ingress path `/omnis/`, host `test.iaparc.atoutlinux.net`.
- Every Go change is `gofmt`-clean; run `make vet` and `make test` before each commit that touches Go.

## Review Focus

1. **iapcli writing a token into a shared config file.** `iapcli` auto-renews tokens and may persist one into `~/.iapcli.yaml`; in a shared pod a later call *without* `IAPCLI_TOKEN` would then run as that user. Expected: the pod's iapcli config is read-only and `$HOME` has no writable `.iapcli.yaml`; a call with no token fails with "You need first to login". Pinned by Task 0 step 4 and Task 12's manifest (`readOnly: true` mount, `HOME` with no config) plus Task 13 step 5.
2. **A route added later without an owner check.** Expected: any `/api/sessions/:id/*` or `/api/schedules/:id*` route refuses another user's id with 404. Pinned by Task 5's route-enumeration test.
3. **A session-less push event leaking to everyone.** `collections_changed` / `schedule_changed` / `session_deleted` (registry entry already gone). Expected: delivered only to the owner. Pinned by Task 6 tests.
4. **Two users hitting the validator at once with the same stale cookie.** Expected: one validator run, both get the same answer; a rejected cookie re-validated at most every 30 s. Pinned by Task 1 cache tests.
5. **A background turn after a restart (no token in memory).** Expected: the shell env carries no token; nothing falls back to another identity. Pinned by Task 7 test `TestShellEnvForUnknownOwnerIsEmpty`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/identity/identity.go` (new) | `Identity`, `Config`, context helpers, `LoginSegment` |
| `internal/identity/cookie.go` (new) | `TokenFromRequest` |
| `internal/identity/validator.go` (new) | `Validator`, `CommandValidator`, `SplitArgs`, dotted-path lookup, errors |
| `internal/identity/cache.go` (new) | `CachingValidator`, `jwtExp` |
| `internal/identity/tokens.go` (new) | `TokenStore` |
| `core/tools/shellenv.go` (new) | `WithShellEnv` / `ShellEnvFrom` |
| `core/tools/bash.go`, `core/tools/tools.go` | apply env to `Bash` and `RunBashInteractive` |
| `internal/bg/bg.go`, `internal/bg/monitor.go`, bg tool handlers | env on background tasks |
| `internal/sessions/sessions.go`, `history.go` | owner-aware constructors, `ListFor`, `SetConversationOwner` |
| `internal/sessions/collections.go`, `internal/collectionctx/collectionctx.go` | root-scoped stores |
| `agent/collection_plugin.go` | optional collection-root resolver |
| `server/identity_cookie.go` (new) | config resolution, `cookieAuth`, middleware, `requestLogin`, `ownerFor`, `shellEnvFor`, owner guard |
| `server/config.go` | new `ServerConfig` fields |
| `server/server.go`, `main.go`, `session_list.go`, `session_search.go`, `scheduler.go`, `collections.go`, `collection_autoupdate.go`, `collection_memory.go`, `spawn.go`, `fork_rewind.go`, `export_import.go`, `mailbox_push.go`, `sse.go`, `bash.go`, `folder_ops.go`, `uploads.go`, `agentmd.go`, `terminal.go`, `terminal_unix.go`, `terminal_windows.go`, `preferences.go`, `prompt_suggest.go`, `whatsnew.go`, `identity.go` | wiring |
| `web/app.js`, `web/index.html` | 401→login redirect, whoami |
| `packaging/k8s/iaparc-test/*` (new) | Dockerfile, manifests, build script, README |
| `CLAUDE.md`, `docs/iaparc-shared-deployment.md` (new), `internal/features/FEATURES.md` | docs |

---

### Task 0: Spike — confirm the cookie token works with iapcli (manual, needs the user)

**Files:** none (findings recorded in the spec §3.1 table and in Task 12's configmap values).

- [ ] **Step 1: Ask the user to log in** at `https://test.iaparc.atoutlinux.net/` in their own browser and copy the `iaparc_token` cookie value (DevTools → Application → Cookies). Do not enter credentials yourself.

- [ ] **Step 2: Validate it with iapcli from a scratch config**

```bash
SCR=$(mktemp -d)
cat > $SCR/.iapcli.yaml <<'EOF'
contexts:
    default:
        address: test.iaparc.atoutlinux.net
        port: "443"
        insecure: ""
        auth: {token: ""}
current_context: default
EOF
chmod 0444 $SCR/.iapcli.yaml
cd $SCR && HOME=$SCR IAPCLI_TOKEN='<paste>' iapcli -C $SCR/.iapcli.yaml user get; echo "exit=$?"
```

Expected: exit 0 and the user's profile printed. Record whether the output is JSON or YAML, and the exact field names holding the login and the roles.

- [ ] **Step 3: If Step 2 fails** with an auth error, try the `auth._token.local` cookie (strip a leading `Bearer `). If neither works, STOP and report to the user: §3 of the spec must be revised before continuing.

- [ ] **Step 4: Check iapcli does not persist the env token**

```bash
cd $SCR && HOME=$SCR IAPCLI_TOKEN='<paste>' iapcli -C $SCR/.iapcli.yaml user get >/dev/null; stat -c '%a %s' $SCR/.iapcli.yaml; grep -c 'token: ""' $SCR/.iapcli.yaml
cd $SCR && HOME=$SCR iapcli -C $SCR/.iapcli.yaml user get; echo "exit=$?"
```

Expected: file still mode 444, token still empty, and the second call (no env) fails with "You need first to login" and a non-zero exit. If iapcli errors because the config is read-only, record the message — Task 12 must then point `HOME` at a writable scratch dir that is **per call** (report to the user before changing the design).

- [ ] **Step 5: Record findings** — edit the spec §3.1 table's "IA Parc value" column (login field, roles field, output format, cookie name that worked) and commit:

```bash
git add docs/superpowers/specs/2026-09-26-shared-cookie-identity-design.md
git commit -m "docs(spec): record iapcli cookie-token spike findings"
```

---

### Task 1: `internal/identity` package

**Files:**
- Create: `internal/identity/identity.go`, `cookie.go`, `validator.go`, `cache.go`, `tokens.go`
- Test: `internal/identity/identity_test.go`, `validator_test.go`, `cache_test.go`, `tokens_test.go`

**Interfaces:**
- Produces:
  - `type Identity struct { Login string; Roles []string; Token string }`
  - `type Config struct { Cookies []string; ValidateCmd []string; TokenEnv, LoginField, RolesField string; AdminRoles []string; LoginURL string; CacheTTL time.Duration }`
  - `func WithIdentity(ctx context.Context, id Identity) context.Context`, `func From(ctx context.Context) (Identity, bool)`
  - `func LoginSegment(login string) string`
  - `func TokenFromRequest(r *http.Request, names []string) string`
  - `type Validator interface { Validate(ctx context.Context, token string) (Identity, error) }`
  - `var ErrRejected, ErrUnavailable error`
  - `type CommandValidator struct { Argv []string; TokenEnv, LoginField, RolesField string; Timeout time.Duration }`
  - `func SplitArgs(s string) ([]string, error)`
  - `func NewCachingValidator(inner Validator, ttl time.Duration) *CachingValidator` (field `Now func() time.Time` for tests)
  - `type TokenStore struct`; `func NewTokenStore() *TokenStore`; `(*TokenStore).Put(login, token string, exp time.Time)`; `(*TokenStore).Get(login string) (string, bool)`; field `Now func() time.Time`
  - `func JWTExp(token string) (time.Time, bool)`

- [ ] **Step 1: Write failing tests** — `internal/identity/identity_test.go`:

```go
package identity

import (
	"context"
	"net/http"
	"testing"
)

func TestContextRoundTrip(t *testing.T) {
	if _, ok := From(context.Background()); ok {
		t.Fatal("empty context must not carry an identity")
	}
	ctx := WithIdentity(context.Background(), Identity{Login: "alice", Token: "t"})
	id, ok := From(ctx)
	if !ok || id.Login != "alice" || id.Token != "t" {
		t.Fatalf("got %+v %v", id, ok)
	}
}

func TestLoginSegment(t *testing.T) {
	cases := map[string]string{
		"Alice@Example.com": "alice@example.com",
		"a/b":               "a_b",
		"..":                "_",
		".":                 "_",
		"":                  "_",
		"x y":               "x_y",
		"ok.name-1_2":       "ok.name-1_2",
	}
	for in, want := range cases {
		if got := LoginSegment(in); got != want {
			t.Errorf("LoginSegment(%q)=%q want %q", in, got, want)
		}
	}
}

func TestTokenFromRequest(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	names := []string{"primary", "fallback"}
	if got := TokenFromRequest(r, names); got != "" {
		t.Fatalf("no cookie: got %q", got)
	}
	r.AddCookie(&http.Cookie{Name: "fallback", Value: "Bearer%20abc"})
	if got := TokenFromRequest(r, names); got != "abc" {
		t.Fatalf("fallback+Bearer+urlencoded: got %q", got)
	}
	r.AddCookie(&http.Cookie{Name: "primary", Value: "xyz"})
	if got := TokenFromRequest(r, names); got != "xyz" {
		t.Fatalf("primary wins: got %q", got)
	}
}
```

`internal/identity/validator_test.go` — uses a fake validator script written to a temp dir:

```go
package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeCmd(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	p := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`iapcli user get -C "/etc/my dir/x.yaml" 'a b'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"iapcli", "user", "get", "-C", "/etc/my dir/x.yaml", "a b"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q", got)
		}
	}
	if _, err := SplitArgs(`unterminated "quote`); err == nil {
		t.Fatal("want error on unterminated quote")
	}
}

func TestCommandValidatorJSON(t *testing.T) {
	cmd := fakeCmd(t, `[ "$TOK" = "good" ] || exit 1; echo '{"user":{"login":"alice","roles":["admin","rd"]}}'`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "user.login", RolesField: "user.roles", Timeout: 5 * time.Second}
	id, err := v.Validate(context.Background(), "good")
	if err != nil || id.Login != "alice" || len(id.Roles) != 2 || id.Token != "good" {
		t.Fatalf("got %+v %v", id, err)
	}
	if _, err := v.Validate(context.Background(), "bad"); !errors.Is(err, ErrRejected) {
		t.Fatalf("bad token: want ErrRejected, got %v", err)
	}
}

func TestCommandValidatorYAML(t *testing.T) {
	cmd := fakeCmd(t, `printf 'login: bob\nroles: rd\n'`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "login", RolesField: "roles", Timeout: 5 * time.Second}
	id, err := v.Validate(context.Background(), "x")
	if err != nil || id.Login != "bob" || len(id.Roles) != 1 || id.Roles[0] != "rd" {
		t.Fatalf("got %+v %v", id, err)
	}
}

func TestCommandValidatorEmptyLoginRejected(t *testing.T) {
	cmd := fakeCmd(t, `echo '{"other":1}'`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "login", Timeout: 5 * time.Second}
	if _, err := v.Validate(context.Background(), "x"); !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestCommandValidatorUnavailable(t *testing.T) {
	v := CommandValidator{Argv: []string{"/nonexistent/validator"}, TokenEnv: "TOK", LoginField: "login", Timeout: time.Second}
	if _, err := v.Validate(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing binary: want ErrUnavailable, got %v", err)
	}
	slow := fakeCmd(t, `sleep 5`)
	v = CommandValidator{Argv: []string{slow}, TokenEnv: "TOK", LoginField: "login", Timeout: 200 * time.Millisecond}
	if _, err := v.Validate(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("timeout: want ErrUnavailable, got %v", err)
	}
}

func TestCommandValidatorErrorNeverContainsToken(t *testing.T) {
	cmd := fakeCmd(t, `echo "bad token $TOK" >&2; exit 1`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "login", Timeout: 5 * time.Second}
	_, err := v.Validate(context.Background(), "s3cr3t-token")
	if err == nil || strings.Contains(err.Error(), "s3cr3t-token") {
		t.Fatalf("error must not leak the token: %v", err)
	}
}
```

`internal/identity/cache_test.go`:

```go
package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingValidator struct {
	calls atomic.Int32
	delay time.Duration
	fn    func(token string) (Identity, error)
}

func (c *countingValidator) Validate(_ context.Context, token string) (Identity, error) {
	c.calls.Add(1)
	time.Sleep(c.delay)
	return c.fn(token)
}

func jwtWithExp(exp time.Time) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"auth_user":"x"}`, exp.Unix())))
	return "h." + p + ".s"
}

func TestJWTExp(t *testing.T) {
	want := time.Unix(1893456000, 0)
	got, ok := JWTExp(jwtWithExp(want))
	if !ok || !got.Equal(want) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := JWTExp("not-a-jwt"); ok {
		t.Fatal("non-JWT must report !ok")
	}
}

func TestCachingValidatorHitAndTTL(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	inner := &countingValidator{fn: func(tok string) (Identity, error) { return Identity{Login: "alice", Token: tok}, nil }}
	c := NewCachingValidator(inner, 15*time.Minute)
	c.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if id, err := c.Validate(context.Background(), "tok"); err != nil || id.Login != "alice" {
			t.Fatalf("got %+v %v", id, err)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("want 1 inner call, got %d", n)
	}
	now = now.Add(16 * time.Minute)
	_, _ = c.Validate(context.Background(), "tok")
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("after TTL want 2 calls, got %d", n)
	}
}

func TestCachingValidatorTTLCappedByJWTExp(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tok := jwtWithExp(now.Add(time.Minute))
	inner := &countingValidator{fn: func(tok string) (Identity, error) { return Identity{Login: "alice", Token: tok}, nil }}
	c := NewCachingValidator(inner, 15*time.Minute)
	c.Now = func() time.Time { return now }
	_, _ = c.Validate(context.Background(), tok)
	now = now.Add(2 * time.Minute)
	_, _ = c.Validate(context.Background(), tok)
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("JWT exp must cap the cache: want 2 calls, got %d", n)
	}
}

func TestCachingValidatorNegativeCache(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	inner := &countingValidator{fn: func(string) (Identity, error) { return Identity{}, ErrRejected }}
	c := NewCachingValidator(inner, 15*time.Minute)
	c.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if _, err := c.Validate(context.Background(), "bad"); !errors.Is(err, ErrRejected) {
			t.Fatalf("want ErrRejected, got %v", err)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("rejection must be cached: got %d calls", n)
	}
	now = now.Add(31 * time.Second)
	_, _ = c.Validate(context.Background(), "bad")
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("negative cache must expire after 30s: got %d calls", n)
	}
}

func TestCachingValidatorUnavailableNotCached(t *testing.T) {
	inner := &countingValidator{fn: func(string) (Identity, error) { return Identity{}, ErrUnavailable }}
	c := NewCachingValidator(inner, 15*time.Minute)
	_, _ = c.Validate(context.Background(), "t")
	_, _ = c.Validate(context.Background(), "t")
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("unavailable must not be cached: got %d calls", n)
	}
}

func TestCachingValidatorSingleFlight(t *testing.T) {
	inner := &countingValidator{delay: 100 * time.Millisecond, fn: func(tok string) (Identity, error) { return Identity{Login: "alice", Token: tok}, nil }}
	c := NewCachingValidator(inner, 15*time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Validate(context.Background(), "same") }()
	}
	wg.Wait()
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("concurrent validations must single-flight: got %d calls", n)
	}
}
```

`internal/identity/tokens_test.go`:

```go
package identity

import (
	"testing"
	"time"
)

func TestTokenStore(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := NewTokenStore()
	s.Now = func() time.Time { return now }
	if _, ok := s.Get("alice"); ok {
		t.Fatal("empty store")
	}
	s.Put("alice", "t1", now.Add(time.Minute))
	if tok, ok := s.Get("alice"); !ok || tok != "t1" {
		t.Fatalf("got %q %v", tok, ok)
	}
	s.Put("alice", "t2", time.Time{}) // zero exp = no expiry known
	if tok, _ := s.Get("alice"); tok != "t2" {
		t.Fatalf("newer token must replace: %q", tok)
	}
	s.Put("bob", "b", now.Add(time.Second))
	now = now.Add(2 * time.Second)
	if _, ok := s.Get("bob"); ok {
		t.Fatal("expired token must not be returned")
	}
	if _, ok := s.Get("alice"); !ok {
		t.Fatal("zero-exp token must survive")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/identity/...`
Expected: FAIL — package does not compile (undefined: `WithIdentity`, …).

- [ ] **Step 3: Implement** — `internal/identity/identity.go`:

```go
// Package identity resolves the user behind a web request in omnis-server's
// shared ("cookie") mode: it reads a platform session cookie, validates the
// token by running an operator-configured command, caches the verdict, and
// keeps each user's latest token in memory so shell tools can act as that
// user. It knows nothing about any particular platform — cookie names, the
// validator command and the output field paths are configuration.
// See docs/superpowers/specs/2026-09-26-shared-cookie-identity-design.md.
package identity

import (
	"context"
	"strings"
	"time"
)

// Identity is a validated caller. Token is the raw platform token: never log
// it, never persist it.
type Identity struct {
	Login string
	Roles []string
	Token string
}

// Config is the resolved cookie-mode configuration (server.yaml + env).
type Config struct {
	Cookies     []string
	ValidateCmd []string
	TokenEnv    string
	LoginField  string
	RolesField  string
	AdminRoles  []string
	LoginURL    string
	CacheTTL    time.Duration
}

type ctxKey struct{}

// WithIdentity returns ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the identity carried by ctx, if any.
func From(ctx context.Context) (Identity, bool) {
	if ctx == nil {
		return Identity{}, false
	}
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok && id.Login != ""
}

// LoginSegment turns a login into a safe single path segment: lowercased,
// every rune outside [a-z0-9._@-] replaced by '_', and "", "." and ".."
// mapped to "_" so it can never escape its parent directory.
func LoginSegment(login string) string {
	s := strings.ToLower(strings.TrimSpace(login))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '@', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}
```

`internal/identity/cookie.go`:

```go
package identity

import (
	"net/http"
	"net/url"
	"strings"
)

// TokenFromRequest returns the token carried by the first present cookie of
// names (in order), URL-decoded and with a leading "Bearer " stripped. "" when
// none is present.
func TokenFromRequest(r *http.Request, names []string) string {
	for _, n := range names {
		ck, err := r.Cookie(n)
		if err != nil || ck.Value == "" {
			continue
		}
		v := ck.Value
		if dec, err := url.QueryUnescape(v); err == nil {
			v = dec
		}
		v = strings.TrimSpace(v)
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			v = strings.TrimSpace(v[7:])
		}
		if v != "" {
			return v
		}
	}
	return ""
}
```

`internal/identity/validator.go`:

```go
package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	// ErrRejected: the validator ran and refused the token (or named no login).
	ErrRejected = errors.New("identity: token rejected")
	// ErrUnavailable: the validator could not give an answer (missing binary,
	// timeout, exec failure). Distinct from a rejection so the server answers
	// 503 rather than "not logged in".
	ErrUnavailable = errors.New("identity: validator unavailable")
)

// Validator turns a token into an Identity.
type Validator interface {
	Validate(ctx context.Context, token string) (Identity, error)
}

// CommandValidator runs Argv (no shell) with <TokenEnv>=<token> added to the
// process environment. Exit 0 = accepted; stdout is parsed as JSON, else YAML,
// and LoginField / RolesField are dotted paths into it.
type CommandValidator struct {
	Argv       []string
	TokenEnv   string
	LoginField string
	RolesField string
	Timeout    time.Duration
}

func (v CommandValidator) Validate(ctx context.Context, token string) (Identity, error) {
	if len(v.Argv) == 0 {
		return Identity{}, fmt.Errorf("%w: no command", ErrUnavailable)
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, v.Argv[0], v.Argv[1:]...)
	cmd.Env = append(os.Environ(), v.TokenEnv+"="+token)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil // stderr may echo the token; never capture it into an error
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if cctx.Err() != nil {
		return Identity{}, fmt.Errorf("%w: timed out after %s", ErrUnavailable, timeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Identity{}, fmt.Errorf("%w: validator exited %d", ErrRejected, exitErr.ExitCode())
		}
		return Identity{}, fmt.Errorf("%w: cannot run validator", ErrUnavailable)
	}
	doc, ok := parseDoc(out.Bytes())
	if !ok {
		return Identity{}, fmt.Errorf("%w: unparseable validator output", ErrRejected)
	}
	login, _ := lookup(doc, v.LoginField).(string)
	login = strings.TrimSpace(login)
	if login == "" {
		return Identity{}, fmt.Errorf("%w: no login at %q", ErrRejected, v.LoginField)
	}
	return Identity{Login: login, Roles: toStrings(lookup(doc, v.RolesField)), Token: token}, nil
}

func parseDoc(b []byte) (any, bool) {
	var doc any
	if err := json.Unmarshal(b, &doc); err == nil {
		return doc, true
	}
	if err := yaml.Unmarshal(b, &doc); err == nil && doc != nil {
		return doc, true
	}
	return nil, false
}

// lookup walks a dotted path through nested maps. "" or a miss yields nil.
func lookup(doc any, path string) any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	cur := doc
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func toStrings(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		var out []string
		for _, s := range strings.Split(t, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// SplitArgs splits a command line into argv with single/double-quote support
// (no expansion, no escapes beyond quoting). It never invokes a shell.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inArg = true
		case r == ' ' || r == '\t' || r == '\n':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}
```

`internal/identity/cache.go`:

```go
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const negativeTTL = 30 * time.Second

type cacheEntry struct {
	id      Identity
	err     error
	expires time.Time
}

// CachingValidator memoises an inner Validator by token hash: accepted tokens
// until min(JWT exp, now+ttl), rejections for 30 s, unavailability never.
// Concurrent validations of one token share a single inner call.
type CachingValidator struct {
	inner Validator
	ttl   time.Duration
	Now   func() time.Time

	mu sync.Mutex
	m  map[string]cacheEntry
	sf singleflight.Group
}

func NewCachingValidator(inner Validator, ttl time.Duration) *CachingValidator {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &CachingValidator{inner: inner, ttl: ttl, Now: time.Now, m: map[string]cacheEntry{}}
}

func (c *CachingValidator) Validate(ctx context.Context, token string) (Identity, error) {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	now := c.Now()
	c.mu.Lock()
	if e, ok := c.m[key]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		if e.err != nil {
			return Identity{}, e.err
		}
		id := e.id
		id.Token = token
		return id, nil
	}
	c.mu.Unlock()

	v, err, _ := c.sf.Do(key, func() (any, error) {
		id, err := c.inner.Validate(ctx, token)
		now := c.Now()
		switch {
		case err == nil:
			exp := now.Add(c.ttl)
			if jexp, ok := JWTExp(token); ok && jexp.Before(exp) {
				exp = jexp
			}
			c.store(key, cacheEntry{id: Identity{Login: id.Login, Roles: id.Roles}, expires: exp})
		case errors.Is(err, ErrRejected):
			c.store(key, cacheEntry{err: err, expires: now.Add(negativeTTL)})
		}
		return id, err
	})
	if err != nil {
		return Identity{}, err
	}
	id := v.(Identity)
	id.Token = token
	return id, nil
}

func (c *CachingValidator) store(key string, e cacheEntry) {
	c.mu.Lock()
	c.m[key] = e
	now := c.Now()
	if len(c.m) > 4096 { // bound memory: drop expired entries
		for k, v := range c.m {
			if !now.Before(v.expires) {
				delete(c.m, k)
			}
		}
	}
	c.mu.Unlock()
}

// JWTExp decodes (does NOT verify) a JWT's exp claim. Used only to bound the
// cache lifetime — identity always comes from the validator.
func JWTExp(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}
```

`internal/identity/tokens.go`:

```go
package identity

import (
	"sync"
	"time"
)

// TokenStore keeps each login's most recent validated token in process memory
// so turns with no HTTP request behind them (schedules, spawns, mailbox) can
// act as the session owner. Lost on restart by design.
type TokenStore struct {
	Now func() time.Time
	mu  sync.RWMutex
	m   map[string]storedToken
}

type storedToken struct {
	token string
	exp   time.Time // zero = unknown
}

func NewTokenStore() *TokenStore {
	return &TokenStore{Now: time.Now, m: map[string]storedToken{}}
}

// Put records login's latest token. exp may be zero (unknown expiry).
func (s *TokenStore) Put(login, token string, exp time.Time) {
	if login == "" || token == "" {
		return
	}
	s.mu.Lock()
	s.m[login] = storedToken{token: token, exp: exp}
	s.mu.Unlock()
}

// Get returns login's token unless it is known to have expired.
func (s *TokenStore) Get(login string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	t, ok := s.m[login]
	s.mu.RUnlock()
	if !ok || (!t.exp.IsZero() && !s.Now().Before(t.exp)) {
		return "", false
	}
	return t.token, true
}
```

- [ ] **Step 4: Make singleflight a direct dependency and run tests**

Run: `go mod tidy && go test ./internal/identity/... -race`
Expected: PASS; `go.mod` now lists `golang.org/x/sync` in the direct `require` block.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/identity
git add internal/identity go.mod go.sum
git commit -m "feat(identity): cookie token extraction, command validator, cache, token store"
```

---

### Task 2: Generic shell environment for shell tools (`core/tools`, `internal/bg`)

**Files:**
- Create: `core/tools/shellenv.go`, `core/tools/shellenv_test.go`
- Modify: `core/tools/bash.go` (`BashIn`, `RunBash`, `RunBashInteractive`), `core/tools/tools.go` (Bash handler), `internal/bg/bg.go` (`Start`), `internal/bg/monitor.go` (`StartMonitor`, `runMonitor`), the bg tool handlers in `internal/bg` (the `bash_background` and `monitor` tool funcs — locate with `grep -n "q.Start(\|q.StartMonitor(" internal/bg/*.go`)
- Test: `core/tools/shellenv_test.go`, `internal/bg/env_test.go`

**Interfaces:**
- Produces: `func WithShellEnv(ctx context.Context, env []string) context.Context`, `func ShellEnvFrom(ctx context.Context) []string` (package `core/tools`, imported elsewhere as `fstools`); `BashIn.Env []string` (`json:"-"`); `(*bg.Queue).Start(label, command string, timeout time.Duration, env ...string) string`; `(*bg.Queue).StartMonitor(label, command, filter string, timeout time.Duration, persistent bool, env ...string) (string, error)`.

- [ ] **Step 1: Write failing tests** — `core/tools/shellenv_test.go`:

```go
package tools

import (
	"context"
	"strings"
	"testing"
)

func TestShellEnvContext(t *testing.T) {
	if ShellEnvFrom(context.Background()) != nil {
		t.Fatal("empty ctx must carry no env")
	}
	ctx := WithShellEnv(context.Background(), []string{"OMNIS_T=abc"})
	if got := ShellEnvFrom(ctx); len(got) != 1 || got[0] != "OMNIS_T=abc" {
		t.Fatalf("got %q", got)
	}
	if WithShellEnv(ctx, nil) != ctx {
		t.Fatal("nil env must be a no-op")
	}
}

func TestRunBashAppliesEnv(t *testing.T) {
	out, _ := RunBash(context.Background(), BashIn{Command: `echo "v=$OMNIS_T"`, Env: []string{"OMNIS_T=abc"}})
	if !strings.Contains(out, "v=abc") {
		t.Fatalf("got %q", out)
	}
	out, _ = RunBash(context.Background(), BashIn{Command: `echo "v=$OMNIS_T"`})
	if !strings.Contains(out, "v=") || strings.Contains(out, "abc") {
		t.Fatalf("no env must leave the var unset: %q", out)
	}
}

func TestRunBashInteractiveReadsEnvFromContext(t *testing.T) {
	ctx := WithShellEnv(context.Background(), []string{"OMNIS_T=xyz"})
	out, _, _ := RunBashInteractive(ctx, `echo "v=$OMNIS_T"`, "", 0)
	if !strings.Contains(out, "v=xyz") {
		t.Fatalf("got %q", out)
	}
}

func TestRunShellCapturedNeverReadsContextEnv(t *testing.T) {
	ctx := WithShellEnv(context.Background(), []string{"OMNIS_T=leak"})
	res := RunShellCaptured(ctx, `echo "v=$OMNIS_T"`, "", nil, 0)
	if strings.Contains(res.Stdout, "leak") {
		t.Fatalf("hooks/run_tests path must not receive the planted env: %q", res.Stdout)
	}
}
```

`internal/bg/env_test.go`:

```go
package bg

import (
	"strings"
	"testing"
	"time"
)

func TestStartPassesEnv(t *testing.T) {
	q := NewQueue()
	id := q.Start("t", `echo "v=$OMNIS_T"`, 10*time.Second, "OMNIS_T=abc")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out, ok := q.Output(id); ok && strings.Contains(out, "v=abc") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background task did not see the env")
}
```

Before writing `env_test.go`, check the queue constructor and output accessor names with `grep -n "^func New\|^func (q \*Queue) \(Output\|Get\|Task\)" internal/bg/*.go` and use the real names (adjust `NewQueue` / `Output` accordingly — if no output accessor exists, read the task's result via the registry accessor that `bg_output` uses).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/tools/ -run 'ShellEnv|RunBashApplies|RunBashInteractiveReads|RunShellCapturedNever' ; go test ./internal/bg/ -run TestStartPassesEnv`
Expected: compile failure (`undefined: WithShellEnv`, `unknown field Env`, too many arguments to `Start`).

- [ ] **Step 3: Implement** — `core/tools/shellenv.go`:

```go
package tools

import "context"

type shellEnvKey struct{}

// WithShellEnv returns a context carrying extra "NAME=value" entries for the
// agent's shell tools (Bash, the "!" escape, background tasks). Like WithCwd it
// propagates into sub-agent runners. The server uses it to hand a user's
// platform token to their own commands; core/tools knows nothing about what
// the entries mean. A nil/empty env is a no-op. Deliberately NOT read by
// RunShellCaptured (hooks, run_tests).
func WithShellEnv(ctx context.Context, env []string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	return context.WithValue(ctx, shellEnvKey{}, append([]string(nil), env...))
}

// ShellEnvFrom returns the entries planted by WithShellEnv, or nil.
func ShellEnvFrom(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	env, _ := ctx.Value(shellEnvKey{}).([]string)
	return env
}
```

In `core/tools/bash.go`, add to `BashIn` (after `Cwd`):

```go
	// Env holds extra "NAME=value" entries appended to the process environment.
	// Set internally from ShellEnvFrom(ctx); excluded from the LLM schema.
	Env []string `json:"-"`
```

In `RunBash`, after `if in.Cwd != "" { cmd.Dir = in.Cwd }` add:

```go
	if len(in.Env) > 0 {
		cmd.Env = append(os.Environ(), in.Env...)
	}
```

In `RunBashInteractive`, after `if cwd != "" { cmd.Dir = cwd }` add:

```go
	if env := ShellEnvFrom(ctx); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
```

Add `"os"` to `bash.go` imports if missing. Do **not** touch `RunShellCaptured`.

In `core/tools/tools.go`, the Bash handler becomes:

```go
			func(ctx adk.ToolContext, in BashIn) (BashOut, error) {
				in.Cwd = sessionCwd(ctx)
				in.Env = ShellEnvFrom(ctx)
				out, _ := RunBash(context.Background(), in)
				return BashOut{Output: out}, nil
			}),
```

In `internal/bg/bg.go`, change the signature to `func (q *Queue) Start(label, command string, timeout time.Duration, env ...string) string` and after `cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)` add:

```go
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
```

In `internal/bg/monitor.go`, change to `func (q *Queue) StartMonitor(label, command, filter string, timeout time.Duration, persistent bool, env ...string) (string, error)`, store `env` on the `Task` (add field `env []string` to `Task` in `tasks.go`, unexported, not serialised), and in `runMonitor` after `cmd := exec.CommandContext(...)`:

```go
	if len(t.env) > 0 {
		cmd.Env = append(os.Environ(), t.env...)
	}
```

In the `bash_background` / `monitor` tool handlers, pass `fstools.ShellEnvFrom(ctx)...` as the trailing argument (`ctx` is the tool's `adk.ToolContext`; the bg package already imports `core/tools` as `tools` — use that alias).

- [ ] **Step 4: Run tests**

Run: `go test ./core/tools/ ./internal/bg/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make vet
git add core/tools internal/bg
git commit -m "feat(tools): carry extra shell env on the context for Bash, ! and background tasks"
```

---

### Task 3: Owner-aware sessions

**Files:**
- Modify: `internal/sessions/sessions.go`, `internal/sessions/history.go`
- Modify: `server/server.go` (create), `server/scheduler.go`, `server/fork_rewind.go`, `server/export_import.go`, `server/spawn.go`, `server/a2a_server.go` (only if it calls `Registry.New`)
- Test: `internal/sessions/owner_test.go`

**Interfaces:**
- Produces: `func (r *Registry) NewFor(owner, squad string) *SessionMeta`; `func (r *Registry) ListFor(owner string) []*SessionMeta` (owner "" ⇒ all, same order as `List`); `func SetConversationOwner(sessionID, owner string) error` (force-sets `user_id`).
- Consumes (later tasks call): `ownerFor(c *gin.Context) string` is defined in Task 4; in this task, callers keep passing `sessions.UserID()` where no request is at hand.

- [ ] **Step 1: Failing test** — `internal/sessions/owner_test.go`:

```go
package sessions

import "testing"

func TestNewForAndListFor(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	r := NewEmptyRegistry()
	a := r.NewFor("alice", "system")
	b := r.NewFor("bob", "system")
	if a.UserID != "alice" || b.UserID != "bob" {
		t.Fatalf("owners: %q %q", a.UserID, b.UserID)
	}
	if got := r.ListFor("alice"); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("ListFor(alice) = %v", got)
	}
	if got := r.ListFor(""); len(got) != 2 {
		t.Fatalf("ListFor(\"\") must list everything, got %d", len(got))
	}
	if m := r.NewFor("", "system"); m.UserID != UserID() {
		t.Fatalf("empty owner must fall back to UserID(), got %q", m.UserID)
	}
}

func TestSetConversationOwnerOverridesStamp(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_ = SetConversationSquad("s1", "system") // stamps the process default
	if err := SetConversationOwner("s1", "alice"); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConversationFile("s1")
	if err != nil || f.UserID != "alice" {
		t.Fatalf("user_id=%q err=%v", f.UserID, err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/sessions/ -run 'NewFor|SetConversationOwner'` — Expected: FAIL (undefined).

- [ ] **Step 3: Implement** — in `sessions.go`, refactor `New` so both share one body:

```go
// New creates a session with an auto-generated petname ID, owned by UserID().
func (r *Registry) New(squad string) *SessionMeta { return r.NewFor("", squad) }

// NewFor creates a session owned by owner ("" ⇒ UserID()).
func (r *Registry) NewFor(owner, squad string) *SessionMeta {
	if owner == "" {
		owner = UserID()
	}
	now := time.Now()
	r.mu.Lock()
	m := &SessionMeta{
		ID:         r.uniqueName(),
		UserID:     owner,
		CreatedAt:  now,
		LastUsedAt: now,
		Squad:      squad,
	}
	r.items[m.ID] = m
	r.mu.Unlock()
	return m
}

// ListFor returns List() restricted to sessions owned by owner; "" ⇒ all.
func (r *Registry) ListFor(owner string) []*SessionMeta {
	all := r.List()
	if owner == "" {
		return all
	}
	out := all[:0:0]
	for _, m := range all {
		if m.UserID == owner {
			out = append(out, m)
		}
	}
	return out
}
```

In `history.go`, next to `SetConversationSquad`:

```go
// SetConversationOwner force-sets the session's owning login. Unlike the
// stamp in SaveConversationFile (only when empty), this overwrites — the
// shared server calls it right after creating a session for a request user.
func SetConversationOwner(sessionID, owner string) error {
	return mutateConversation(sessionID, func(f *ConversationFile) { f.UserID = owner })
}
```

- [ ] **Step 4: Make every server-side creation owner-aware.** At each site below, replace `d.Registry.New(squad)` with `d.Registry.NewFor(owner, squad)` and every `sessions.UserID()` used as that session's owner (in `RegisterSession`, `PushMgr.Watch`, `injectTurn`) with `owner`, then persist with `_ = sessions.SetConversationOwner(meta.ID, meta.UserID)` right after `SetConversationSquad`:
  - `server/server.go` POST `/sessions`: `owner := sessions.UserID()` for now (Task 4 replaces it with `ownerFor(c)`).
  - `server/scheduler.go`: `createScheduledSession(d, squad, prompt)` gains an `owner string` param; the fire callback passes `userOrDefault(job.UserID)`; the `injectTurn` after it uses the same owner.
  - `server/fork_rewind.go` `handleFork`: owner = the **source** session's `sessionUserID(srcMeta)`.
  - `server/export_import.go` `handleImportSession`: owner = `sessions.UserID()` for now (Task 4 → `ownerFor(c)`).
  - `server/spawn.go` `materializeSession`: `d.Registry.NewFor(userOrDefault(o.UserID), squad)`; `drainSpawns` / `handleSpawn` set `o.UserID` to the parent session's owner.

- [ ] **Step 5: Run the whole suite** — `make test` — Expected: PASS (single-user mode: every owner equals `UserID()`).

- [ ] **Step 6: Commit**

```bash
git add internal/sessions server
git commit -m "feat(sessions): owner-aware session creation and listing"
```

---

### Task 4: Cookie identity mode — config, middleware, wiring

**Files:**
- Create: `server/identity_cookie.go`, `server/identity_cookie_test.go`
- Modify: `server/config.go` (`ServerConfig`), `server/server.go` (`serverDeps`, `newEngine`), `server/main.go` (resolve + fatal checks + deps), `server/identity.go` (`handleWhoami`), `server/terminal.go` (WS token bypass), POST `/sessions` + import (owner)

**Interfaces:**
- Consumes: `identity.*` (Task 1), `Registry.NewFor` (Task 3).
- Produces (server package):
  - `type cookieAuth struct { cfg identity.Config; validator identity.Validator; tokens *identity.TokenStore }`
  - `func resolveCookieIdentity(cfg ServerConfig, lookPath func(string) (string, error)) (*cookieAuth, error)` — nil, nil when mode off
  - `func cookieIdentityMiddleware(a *cookieAuth) gin.HandlerFunc`
  - `func requestLogin(c *gin.Context) string` ("" when not in cookie mode)
  - `func ownerFor(c *gin.Context) string` (login, else `sessions.UserID()`)
  - `serverDeps.Cookie *cookieAuth`

- [ ] **Step 1: Failing tests** — `server/identity_cookie_test.go`:

```go
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/identity"
)

// fakeValidator knows a fixed token→login table.
type fakeValidator map[string]string

func (f fakeValidator) Validate(_ context.Context, tok string) (identity.Identity, error) {
	if tok == "down" {
		return identity.Identity{}, identity.ErrUnavailable
	}
	if l, ok := f[tok]; ok {
		return identity.Identity{Login: l, Token: tok}, nil
	}
	return identity.Identity{}, identity.ErrRejected
}

func testCookieAuth() *cookieAuth {
	return &cookieAuth{
		cfg:       identity.Config{Cookies: []string{"plat_token"}, TokenEnv: "PLAT_TOKEN", LoginURL: "https://plat/login?r={return}"},
		validator: fakeValidator{"ta": "alice", "tb": "bob"},
		tokens:    identity.NewTokenStore(),
	}
}

func TestCookieMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := testCookieAuth()
	r := gin.New()
	r.GET("/x", cookieIdentityMiddleware(a), func(c *gin.Context) { c.String(200, requestLogin(c)) })

	do := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/x", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "plat_token", Value: cookie})
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := do(""); w.Code != 401 || !strings.Contains(w.Body.String(), "https://plat/login") {
		t.Fatalf("no cookie: %d %s", w.Code, w.Body)
	}
	if w := do("nope"); w.Code != 401 {
		t.Fatalf("rejected: %d", w.Code)
	}
	if w := do("down"); w.Code != 503 {
		t.Fatalf("unavailable: %d", w.Code)
	}
	if w := do("ta"); w.Code != 200 || w.Body.String() != "alice" {
		t.Fatalf("valid: %d %q", w.Code, w.Body)
	}
	if tok, ok := a.tokens.Get("alice"); !ok || tok != "ta" {
		t.Fatalf("token store not updated: %q %v", tok, ok)
	}
}

func TestResolveCookieIdentity(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/x", nil }
	missing := func(string) (string, error) { return "", errors.New("nope") }
	base := ServerConfig{IdentityMode: "cookie", AuthCookies: "a,b", AuthValidateCmd: "x user get", AuthTokenEnv: "T", AuthLoginField: "login"}

	if a, err := resolveCookieIdentity(ServerConfig{}, found); a != nil || err != nil {
		t.Fatalf("mode off must be nil,nil: %v %v", a, err)
	}
	a, err := resolveCookieIdentity(base, found)
	if err != nil || a == nil || len(a.cfg.Cookies) != 2 || a.cfg.ValidateCmd[0] != "x" {
		t.Fatalf("valid config: %+v %v", a, err)
	}
	bad := []ServerConfig{
		func() ServerConfig { c := base; c.IdentityMode = "oidc"; return c }(),
		func() ServerConfig { c := base; c.UserID = "u"; return c }(),
		func() ServerConfig { c := base; c.IdentityHeader = "X-User"; return c }(),
		func() ServerConfig { c := base; c.A2AEnabled = true; return c }(),
		func() ServerConfig { c := base; c.AuthCookies = ""; return c }(),
		func() ServerConfig { c := base; c.AuthValidateCmd = ""; return c }(),
		func() ServerConfig { c := base; c.AuthTokenEnv = ""; return c }(),
		func() ServerConfig { c := base; c.AuthLoginField = ""; return c }(),
	}
	for i, c := range bad {
		if _, err := resolveCookieIdentity(c, found); err == nil {
			t.Errorf("bad[%d] must be fatal", i)
		}
	}
	if _, err := resolveCookieIdentity(base, missing); err == nil {
		t.Error("validator binary not on PATH must be fatal")
	}
}
```

Env-var precedence is covered by a third test using `t.Setenv("OMNIS_AUTH_COOKIES", "z")` on top of `base` and asserting `a.cfg.Cookies == ["z"]`; also `t.Setenv("OMNIS_AUTH_CACHE_TTL", "2m")` → `a.cfg.CacheTTL == 2*time.Minute`. Write it:

```go
func TestResolveCookieIdentityEnvWins(t *testing.T) {
	t.Setenv("OMNIS_AUTH_COOKIES", "z")
	t.Setenv("OMNIS_AUTH_CACHE_TTL", "2m")
	found := func(string) (string, error) { return "/usr/bin/x", nil }
	base := ServerConfig{IdentityMode: "cookie", AuthCookies: "a,b", AuthValidateCmd: "x", AuthTokenEnv: "T", AuthLoginField: "login"}
	a, err := resolveCookieIdentity(base, found)
	if err != nil || len(a.cfg.Cookies) != 1 || a.cfg.Cookies[0] != "z" || a.cfg.CacheTTL != 2*time.Minute {
		t.Fatalf("%+v %v", a, err)
	}
}
```

(add `"time"` to the imports).

- [ ] **Step 2: Run** `go test ./server/ -run 'CookieMiddleware|ResolveCookieIdentity'` — Expected: compile FAIL.

- [ ] **Step 3: Add `ServerConfig` fields** in `server/config.go` (after `IdentityHeader`):

```go
	// IdentityMode selects how requests are identified. "" = single-user /
	// milestone-1 behaviour; "cookie" = a SHARED server identifying each
	// request by a platform session cookie validated with AuthValidateCmd.
	// See docs/superpowers/specs/2026-09-26-shared-cookie-identity-design.md.
	// Overridden by OMNIS_IDENTITY_MODE.
	IdentityMode    string `yaml:"identity_mode,omitempty" json:"identity_mode,omitempty"`
	AuthCookies     string `yaml:"auth_cookies,omitempty" json:"auth_cookies,omitempty"`           // OMNIS_AUTH_COOKIES (comma list)
	AuthValidateCmd string `yaml:"auth_validate_cmd,omitempty" json:"auth_validate_cmd,omitempty"` // OMNIS_AUTH_VALIDATE_CMD
	AuthTokenEnv    string `yaml:"auth_token_env,omitempty" json:"auth_token_env,omitempty"`       // OMNIS_AUTH_TOKEN_ENV
	AuthLoginField  string `yaml:"auth_login_field,omitempty" json:"auth_login_field,omitempty"`   // OMNIS_AUTH_LOGIN_FIELD
	AuthRolesField  string `yaml:"auth_roles_field,omitempty" json:"auth_roles_field,omitempty"`   // OMNIS_AUTH_ROLES_FIELD
	AuthAdminRoles  string `yaml:"auth_admin_roles,omitempty" json:"auth_admin_roles,omitempty"`   // OMNIS_AUTH_ADMIN_ROLES (comma list)
	AuthLoginURL    string `yaml:"auth_login_url,omitempty" json:"auth_login_url,omitempty"`       // OMNIS_AUTH_LOGIN_URL
	AuthCacheTTL    string `yaml:"auth_cache_ttl,omitempty" json:"auth_cache_ttl,omitempty"`       // OMNIS_AUTH_CACHE_TTL (Go duration)
```

- [ ] **Step 4: Implement `server/identity_cookie.go`**:

```go
package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/identity"
	"github.com/blouargant/omnis/internal/sessions"
)

// cookieAuth is the shared-server ("cookie") identity mode. nil ⇒ mode off.
type cookieAuth struct {
	cfg       identity.Config
	validator identity.Validator
	tokens    *identity.TokenStore
}

func commaList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveCookieIdentity applies env > server.yaml and the fatal combinations
// of spec §3.1/§4. lookPath is exec.LookPath in production.
func resolveCookieIdentity(cfg ServerConfig, lookPath func(string) (string, error)) (*cookieAuth, error) {
	mode := strings.ToLower(strings.TrimSpace(envOr("OMNIS_IDENTITY_MODE", cfg.IdentityMode)))
	switch mode {
	case "":
		return nil, nil
	case "cookie":
	default:
		return nil, fmt.Errorf("server: unknown identity_mode %q (want \"\" or \"cookie\")", mode)
	}
	if strings.TrimSpace(envOr("OMNIS_USER_ID", cfg.UserID)) != "" || strings.TrimSpace(envOr("OMNIS_IDENTITY_HEADER", cfg.IdentityHeader)) != "" {
		return nil, errors.New("server: identity_mode cookie (one server, many users) cannot be combined with user_id/identity_header (one server per user)")
	}
	if cfg.A2AEnabled {
		return nil, errors.New("server: identity_mode cookie requires a2a_enabled: false — inbound A2A carries no user identity")
	}
	c := identity.Config{
		Cookies:    commaList(envOr("OMNIS_AUTH_COOKIES", cfg.AuthCookies)),
		TokenEnv:   strings.TrimSpace(envOr("OMNIS_AUTH_TOKEN_ENV", cfg.AuthTokenEnv)),
		LoginField: strings.TrimSpace(envOr("OMNIS_AUTH_LOGIN_FIELD", cfg.AuthLoginField)),
		RolesField: strings.TrimSpace(envOr("OMNIS_AUTH_ROLES_FIELD", cfg.AuthRolesField)),
		AdminRoles: commaList(envOr("OMNIS_AUTH_ADMIN_ROLES", cfg.AuthAdminRoles)),
		LoginURL:   strings.TrimSpace(envOr("OMNIS_AUTH_LOGIN_URL", cfg.AuthLoginURL)),
		CacheTTL:   15 * time.Minute,
	}
	argv, err := identity.SplitArgs(envOr("OMNIS_AUTH_VALIDATE_CMD", cfg.AuthValidateCmd))
	if err != nil {
		return nil, fmt.Errorf("server: auth_validate_cmd: %w", err)
	}
	c.ValidateCmd = argv
	if ttl := strings.TrimSpace(envOr("OMNIS_AUTH_CACHE_TTL", cfg.AuthCacheTTL)); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("server: auth_cache_ttl %q: want a positive Go duration", ttl)
		}
		c.CacheTTL = d
	}
	var missing []string
	if len(c.Cookies) == 0 {
		missing = append(missing, "auth_cookies")
	}
	if len(c.ValidateCmd) == 0 {
		missing = append(missing, "auth_validate_cmd")
	}
	if c.TokenEnv == "" {
		missing = append(missing, "auth_token_env")
	}
	if c.LoginField == "" {
		missing = append(missing, "auth_login_field")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("server: identity_mode cookie needs %s", strings.Join(missing, ", "))
	}
	if _, err := lookPath(c.ValidateCmd[0]); err != nil {
		return nil, fmt.Errorf("server: auth_validate_cmd %q not found on PATH", c.ValidateCmd[0])
	}
	inner := identity.CommandValidator{Argv: c.ValidateCmd, TokenEnv: c.TokenEnv, LoginField: c.LoginField, RolesField: c.RolesField, Timeout: 10 * time.Second}
	return &cookieAuth{cfg: c, validator: identity.NewCachingValidator(inner, c.CacheTTL), tokens: identity.NewTokenStore()}, nil
}

// cookieIdentityMiddleware authenticates every request from the platform
// cookie (spec §3.4). It replaces the bearer-token check in cookie mode.
func cookieIdentityMiddleware(a *cookieAuth) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := identity.TokenFromRequest(c.Request, a.cfg.Cookies)
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated", "login_url": a.cfg.LoginURL})
			return
		}
		id, err := a.validator.Validate(c.Request.Context(), tok)
		if err != nil {
			if errors.Is(err, identity.ErrUnavailable) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "identity provider unavailable"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated", "login_url": a.cfg.LoginURL})
			return
		}
		exp, _ := identity.JWTExp(tok)
		a.tokens.Put(id.Login, tok, exp)
		c.Request = c.Request.WithContext(identity.WithIdentity(c.Request.Context(), id))
		c.Next()
	}
}

// requestLogin is the cookie-mode caller's login, "" in any other mode.
func requestLogin(c *gin.Context) string {
	if id, ok := identity.From(c.Request.Context()); ok {
		return id.Login
	}
	return ""
}

// ownerFor is the owner to stamp on a session created by this request.
func ownerFor(c *gin.Context) string {
	if l := requestLogin(c); l != "" {
		return l
	}
	return sessions.UserID()
}
```

- [ ] **Step 5: Wire it.**
  - `serverDeps`: add `Cookie *cookieAuth` with a comment "non-nil ⇒ shared cookie-identity mode (spec 2026-09-26)".
  - `newEngine`: replace the `api.GET("/terminal/ws", …)` and `auth := api.Group(…)` lines with:

```go
	var authChain []gin.HandlerFunc
	if d.Cookie != nil {
		authChain = []gin.HandlerFunc{cookieIdentityMiddleware(d.Cookie)}
	} else {
		authChain = []gin.HandlerFunc{authMiddleware(d.Token), identityMiddleware(d.IdentityHeader, sessions.UserID())}
	}
	wsChain := authChain[len(authChain)-1:] // cookie mode: the cookie check; otherwise the identity header check
	api.GET("/terminal/ws", append(wsChain, handleTerminal(d))...)
	auth := api.Group("", authChain...)
```

  - `handleTerminal`: change the first check to `if d.Cookie == nil && d.Token != "" && !termTokens.consume(c.Query("token")) {`.
  - `handleWhoami(identityHeader string)` → `handleWhoami(d serverDeps)`; body:

```go
		if d.Cookie != nil {
			c.JSON(http.StatusOK, gin.H{"user_id": requestLogin(c), "identity_mode": "cookie", "identity_enforced": true})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user_id":           sessions.UserID(),
			"identity_enforced": strings.TrimSpace(d.IdentityHeader) != "",
		})
```

  and the route `auth.GET("/whoami", handleWhoami(d))`. Update `server/identity_test.go` call sites to the new signature (`handleWhoami(serverDeps{IdentityHeader: …})`).
  - POST `/sessions` and `handleImportSession`: `owner := ownerFor(c)` (replacing the Task 3 placeholder).
  - `server/main.go`, right after `resolveIdentity` succeeds:

```go
	cookie, err := resolveCookieIdentity(serverCfg, exec.LookPath)
	if err != nil {
		return err
	}
	if cookie != nil {
		log.Printf("server: SHARED mode — requests identified by cookie %v, validated by %q; trusted users only (no OS isolation between users)", cookie.cfg.Cookies, cookie.cfg.ValidateCmd[0])
	}
```

  and set `Cookie: cookie` in the `serverDeps` literal. Also skip starting the A2A server when `cookie != nil` (belt: config already refused `a2a_enabled`). Add `"os/exec"` to imports if absent.

- [ ] **Step 6: Run** `go test ./server/ -race` — Expected: PASS (new tests + all existing).

- [ ] **Step 7: Commit**

```bash
make vet
git add server
git commit -m "feat(server): cookie identity mode with command validation"
```

---

### Task 5: Ownership guard and per-user listings

**Files:**
- Modify: `server/identity_cookie.go` (guard), `server/server.go` (mount guard, events replay later), `server/session_list.go`, `server/session_search.go`, `server/scheduler.go`, `server/collections.go` (counts only; storage is Task 9)
- Test: `server/identity_scope_test.go`

**Interfaces:**
- Consumes: `requestLogin` (Task 4), `Registry.ListFor` (Task 3), `scheduler.Job.UserID`.
- Produces: `func ownerGuard(d serverDeps) gin.HandlerFunc`; `func visibleTo(login, owner string) bool`; test helper `newCookieTestEngine(t) (*gin.Engine, serverDeps)` reused by Tasks 6–9.

- [ ] **Step 1: Failing test** — `server/identity_scope_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// newCookieTestEngine builds the real router in cookie mode with a fake
// validator (tokens "ta"→alice, "tb"→bob) and one session per user.
func newCookieTestEngine(t *testing.T) (*gin.Engine, serverDeps, string, string) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	d := serverDeps{
		Registry:   sessions.NewEmptyRegistry(),
		WebDir:     t.TempDir(),
		Cookie:     testCookieAuth(),
		PushEvents: newSessionPushBroadcaster(),
		RunGuard:   newSessionRunGuard(),
		LiveTurns:  newLiveTurnRegistry(),
		rootCtx:    t.Context(),
	}
	a := d.Registry.NewFor("alice", "system")
	b := d.Registry.NewFor("bob", "system")
	return newEngine(d), d, a.ID, b.ID
}

func doAs(r http.Handler, tok, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Every route carrying a session or schedule id must refuse another user's id
// with 404 — enumerated from the router so a future route cannot slip past.
func TestOwnerGuardCoversEveryIDRoute(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	n := 0
	for _, rt := range r.Routes() {
		if !strings.Contains(rt.Path, "/sessions/:id") {
			continue
		}
		path := strings.NewReplacer(":id", aliceSID, ":qid", "q1", ":runID", "r1").Replace(rt.Path)
		w := doAs(r, "tb", rt.Method, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s as bob: got %d, want 404", rt.Method, rt.Path, w.Code)
		}
		n++
	}
	if n < 30 {
		t.Fatalf("expected to exercise the whole /sessions/:id family, only saw %d routes", n)
	}
}

func TestSessionListsAreScoped(t *testing.T) {
	r, _, aliceSID, bobSID := newCookieTestEngine(t)
	for _, path := range []string{"/api/sessions", "/api/sessions?limit=50", "/api/session-ids"} {
		body := doAs(r, "ta", "GET", path).Body.String()
		if !strings.Contains(body, aliceSID) || strings.Contains(body, bobSID) {
			t.Errorf("%s as alice leaked or missed: %s", path, body)
		}
	}
	var out struct{ Results []map[string]any }
	_ = json.Unmarshal(doAs(r, "ta", "GET", "/api/search/sessions?q=anything").Body.Bytes(), &out)
	for _, res := range out.Results {
		if res["session_id"] == bobSID {
			t.Errorf("search leaked bob's session to alice")
		}
	}
}

func TestSchedulesAreScoped(t *testing.T) {
	r, d, _, _ := newCookieTestEngine(t)
	d.Scheduler = newTestScheduler(t) // see Step 3
	r = newEngine(d)
	req := `{"kind":"schedule","spec":"every 2h","prompt":"hello"}`
	wa := doWithBody(r, "ta", "POST", "/api/schedules", req)
	if wa.Code != http.StatusCreated {
		t.Fatalf("create as alice: %d %s", wa.Code, wa.Body)
	}
	var job struct{ ID string }
	_ = json.Unmarshal(wa.Body.Bytes(), &job)
	if body := doAs(r, "tb", "GET", "/api/schedules").Body.String(); strings.Contains(body, job.ID) {
		t.Fatalf("bob sees alice's schedule: %s", body)
	}
	for _, p := range []string{"/api/schedules/" + job.ID, "/api/schedules/" + job.ID + "/run", "/api/schedules/" + job.ID + "/history"} {
		for _, m := range []string{"PATCH", "POST", "DELETE"} {
			w := doAs(r, "tb", m, p)
			if w.Code == http.StatusOK || w.Code == http.StatusCreated || w.Code == http.StatusNoContent {
				t.Errorf("%s %s as bob: %d", m, p, w.Code)
			}
		}
	}
}

func doWithBody(r http.Handler, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
```

Before running, confirm the constructor names used in `newCookieTestEngine` (`newSessionPushBroadcaster`, `newSessionRunGuard`, `newLiveTurnRegistry`) with `grep -n "^func new" server/*.go` and adjust. For `newTestScheduler`, reuse the helper already used in `server/scheduler_cascade_test.go` (grep `scheduler.New(` there) — if it has a different name, call that one. The search route returns results under the key used in `handleSearchSessions` (`results`); if the result objects key the id differently than `session_id`, adjust the test to that key.

- [ ] **Step 2: Run** `go test ./server/ -run 'OwnerGuard|ListsAreScoped|SchedulesAreScoped'` — Expected: FAIL (bob gets 200s; lists leak).

- [ ] **Step 3: Implement the guard** (append to `server/identity_cookie.go`):

```go
// visibleTo reports whether an item owned by owner is visible to login.
// login "" (not cookie mode) sees everything — the no-op contract.
func visibleTo(login, owner string) bool { return login == "" || owner == login }

// ownerGuard refuses, with 404, any route addressing a session or schedule
// the caller does not own — and a `session` query parameter naming one. It
// keys on the MATCHED route pattern (c.FullPath()), so every route under
// /sessions/:id and /schedules/:id is covered, including ones added later.
func ownerGuard(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		login := requestLogin(c)
		if login == "" {
			c.Next()
			return
		}
		notFound := func() { c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"}) }
		fp := c.FullPath()
		if strings.Contains(fp, "/sessions/:id") {
			if m, ok := d.Registry.Snapshot(c.Param("id")); ok && m.UserID != login {
				notFound()
				return
			}
		}
		if sid := c.Query("session"); sid != "" {
			if m, ok := d.Registry.Snapshot(sid); ok && m.UserID != login {
				notFound()
				return
			}
		}
		if strings.Contains(fp, "/schedules/:id") && d.Scheduler != nil {
			for _, j := range d.Scheduler.List() {
				if j.ID == c.Param("id") && j.UserID != login {
					notFound()
					return
				}
			}
		}
		c.Next()
	}
}
```

Mount it: `auth := api.Group("", append(authChain, ownerGuard(d))...)`.

- [ ] **Step 4: Scope the listings.**
  - `session_list.go`: in both handlers replace `all := d.Registry.List()` with `all := d.Registry.ListFor(requestLogin(c))`.
  - `session_search.go` `handleSearchSessions`: after `results, mode, stats, err := sessindex.SearchOrScan(...)`, filter:

```go
		if login := requestLogin(c); login != "" {
			kept := results[:0]
			for _, r := range results {
				if m, ok := d.Registry.Snapshot(r.SessionID); ok && m.UserID == login {
					kept = append(kept, r)
				}
			}
			results = kept
		}
```

  (use the real id field name of `sessindex.Result` — check with `grep -n "type Result struct" -A8 internal/sessindex/*.go`).
  - `scheduler.go`: `handleListSchedules` filters `d.Scheduler.List()` by `visibleTo(requestLogin(c), j.UserID)`; `handleCreateSchedule` sets `UserID: ownerFor(c)`; when `req.SessionID != ""` and cookie mode, reject with 404 unless the caller owns that session.
  - `collections.go`: the three `for _, m := range d.Registry.List()` session-count loops use `d.Registry.ListFor(requestLogin(c))` (the handler's `c` is in scope; for helpers taking `d` only, add a `login string` parameter).

- [ ] **Step 5: Run** `go test ./server/ -race` — Expected: PASS.

- [ ] **Step 6: Commit**

```bash
make vet
git add server
git commit -m "feat(server): per-user ownership guard and scoped session/schedule listings"
```

---

### Task 6: Per-user push events

**Files:**
- Modify: `server/mailbox_push.go` (`pushMsg.Owner`, broadcaster `ownerOf`, `broadcastOwned`), `server/server.go` (`/api/events` filter + ask_user replay filter, broadcaster wiring), `server/spawn.go` (`session_deleted` with owner), `server/collections.go`, `server/collection_autoupdate.go` (`collections_changed` with owner), `server/scheduler.go` (`broadcastScheduleChanged(d, owner)`), `server/main.go` (set `ownerOf`)
- Test: `server/identity_events_test.go`

**Interfaces:**
- Produces: `pushMsg.Owner string`; `(*sessionPushBroadcaster).ownerOf func(sid string) string` (field, set once at boot); `(*sessionPushBroadcaster).broadcastOwned(event, sid, owner string)`; `func pushVisible(login string, m pushMsg) bool`.

- [ ] **Step 1: Failing test** — `server/identity_events_test.go`:

```go
package main

import "testing"

func TestPushVisible(t *testing.T) {
	cases := []struct {
		login string
		m     pushMsg
		want  bool
	}{
		{"", pushMsg{Event: "x", SID: "s", Owner: "bob"}, true},            // single-user: everything
		{"alice", pushMsg{Event: "session_created", SID: "s", Owner: "alice"}, true},
		{"alice", pushMsg{Event: "session_created", SID: "s", Owner: "bob"}, false},
		{"alice", pushMsg{Event: "session_deleted", SID: "s"}, false},      // unknown owner ⇒ withheld
		{"alice", pushMsg{Event: "collections_changed", Owner: "bob"}, false},
		{"alice", pushMsg{Event: "collections_changed", Owner: "alice"}, true},
		{"alice", pushMsg{Event: "update_available"}, true},                // global by nature
	}
	for i, c := range cases {
		if got := pushVisible(c.login, c.m); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestBroadcastResolvesOwner(t *testing.T) {
	b := newSessionPushBroadcaster()
	b.ownerOf = func(sid string) string { return map[string]string{"s1": "alice"}[sid] }
	ch := b.subscribeAll()
	defer b.unsubscribeAll(ch)
	b.broadcast("session_created", "s1")
	if m := <-ch; m.Owner != "alice" {
		t.Fatalf("owner not resolved: %+v", m)
	}
	b.broadcastOwned("session_deleted", "gone", "bob")
	if m := <-ch; m.Owner != "bob" {
		t.Fatalf("explicit owner lost: %+v", m)
	}
}
```

- [ ] **Step 2: Run** `go test ./server/ -run 'PushVisible|BroadcastResolvesOwner'` — Expected: compile FAIL.

- [ ] **Step 3: Implement.** In `mailbox_push.go`: add `Owner string` to `pushMsg`; add field `ownerOf func(sid string) string` to `sessionPushBroadcaster`; in `broadcastFrom` and `broadcastData`, build the message with `Owner: b.resolveOwner(sessionID)` where:

```go
func (b *sessionPushBroadcaster) resolveOwner(sid string) string {
	if sid == "" || b.ownerOf == nil {
		return ""
	}
	return b.ownerOf(sid)
}

// broadcastOwned sends a session-less (or already-deleted-session) event to
// one owner's browsers only. owner "" ⇒ global.
func (b *sessionPushBroadcaster) broadcastOwned(event, sid, owner string) {
	b.mu.RLock()
	for ch := range b.all {
		select {
		case ch <- pushMsg{Event: event, SID: sid, Owner: owner}:
		default:
		}
	}
	b.mu.RUnlock()
}

// pushVisible decides whether a push event reaches a subscriber. login ""
// (not cookie mode) receives everything. In cookie mode an event naming a
// session goes only to its owner (and is withheld when the owner is unknown);
// a session-less event goes to its owner, or to everyone when it has none
// (update_available, config reload).
func pushVisible(login string, m pushMsg) bool {
	if login == "" {
		return true
	}
	if m.Owner != "" {
		return m.Owner == login
	}
	return m.SID == ""
}
```

  (Match the existing select/default pattern and field names in `broadcastFrom`.)

  In `server/server.go` `/api/events`: `login := requestLogin(c)` at the top; in the `case msg := <-pushCh:` branch, `if !pushVisible(login, msg) { continue }`; in the replay loop use `d.Registry.ListFor(login)`; in the `case be := <-busCh:` branch, before writing, resolve `sid, _ := be.Payload["session_id"].(string)` and `if login != "" { if m, ok := d.Registry.Snapshot(sid); !ok || m.UserID != login { continue } }`.

  In `main.go`, after the broadcaster is built: `pushEvents.ownerOf = func(sid string) string { if m, ok := registry.Snapshot(sid); ok { return m.UserID }; return "" }` (use the real variable names there).

  Replace the emitters:
  - `spawn.go` `deleteSession`: capture `owner := userID` (already captured from `meta.UserID`) and replace `d.PushEvents.broadcast("session_deleted", id)` with `d.PushEvents.broadcastOwned("session_deleted", id, owner)`.
  - `collections.go` (5 sites) and `collection_autoupdate.go` (1 site): `broadcast("collections_changed", "")` → `broadcastOwned("collections_changed", "", login)` where `login = requestLogin(c)` in handlers and the session owner in the auto-update worker. In single-user mode `login` is "" ⇒ global, unchanged.
  - `scheduler.go`: `broadcastScheduleChanged(d serverDeps)` → `broadcastScheduleChanged(d serverDeps, owner string)` using `broadcastOwned("schedule_changed", "", owner)`; callers pass `requestLogin(c)` (handlers) or `job.UserID` (fire path). In single-user mode job owners are `web-user` — so `pushVisible("", …)` is true anyway.

- [ ] **Step 4: Add an end-to-end SSE assertion** to `identity_events_test.go`: using `newCookieTestEngine`, open `/api/events` as bob via `httptest.NewServer` + a cookie, call `d.PushEvents.broadcast("session_renamed", aliceSID)` then `d.PushEvents.broadcast("session_renamed", bobSID)`, and read the stream until the first `event:` line — it must mention `bobSID`, never `aliceSID`. Close with a 2 s deadline.

```go
func TestEventsStreamIsScoped(t *testing.T) {
	r, d, aliceSID, bobSID := newCookieTestEngine(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: "tb"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	d.PushEvents.ownerOf = func(sid string) string {
		if m, ok := d.Registry.Snapshot(sid); ok {
			return m.UserID
		}
		return ""
	}
	time.Sleep(100 * time.Millisecond) // let the handler subscribe
	d.PushEvents.broadcast("session_renamed", aliceSID)
	d.PushEvents.broadcast("session_renamed", bobSID)
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			if strings.Contains(line, aliceSID) {
				t.Fatal("bob received alice's event")
			}
			if strings.Contains(line, bobSID) {
				return
			}
		}
	}
	t.Fatal("bob never received his own event")
}
```

(imports: `bufio`, `context`, `net/http`, `net/http/httptest`, `strings`, `time`.)

- [ ] **Step 5: Run** `go test ./server/ -race` — Expected: PASS.

- [ ] **Step 6: Commit**

```bash
make vet
git add server
git commit -m "feat(server): scope push events and ask_user replay to the session owner"
```

---

### Task 7: Token delivery to turns, the `!` escape and the terminal

**Files:**
- Modify: `server/identity_cookie.go` (`shellEnvFor`), `server/sse.go` (turn ctx), `server/mailbox_push.go` (`injectTurnRouted` ctx), `server/bash.go` (`handleBash`), `server/terminal.go`, `server/terminal_unix.go`, `server/terminal_windows.go`
- Test: `server/identity_token_test.go`

**Interfaces:**
- Consumes: `fstools.WithShellEnv` (Task 2), `cookieAuth.tokens` (Task 4).
- Produces: `func (d serverDeps) shellEnvFor(owner string) []string`; `startPTYSession(dir string, env []string) (ptySession, error)`.

- [ ] **Step 1: Failing test** — `server/identity_token_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"
)

func TestShellEnvFor(t *testing.T) {
	d := serverDeps{Cookie: testCookieAuth()}
	d.Cookie.tokens.Put("alice", "ta", time.Time{})
	if env := d.shellEnvFor("alice"); len(env) != 1 || env[0] != "PLAT_TOKEN=ta" {
		t.Fatalf("got %q", env)
	}
}

func TestShellEnvForUnknownOwnerIsEmpty(t *testing.T) {
	d := serverDeps{Cookie: testCookieAuth()}
	d.Cookie.tokens.Put("alice", "ta", time.Time{})
	if env := d.shellEnvFor("bob"); env != nil {
		t.Fatalf("no token for bob must yield no env (never another user's): %q", env)
	}
	if env := (serverDeps{}).shellEnvFor("alice"); env != nil {
		t.Fatalf("single-user mode must never inject: %q", env)
	}
}

func TestBangEscapeSeesOwnToken(t *testing.T) {
	r, _, aliceSID, _ := newCookieTestEngine(t)
	w := doWithBody(r, "ta", "POST", "/api/sessions/"+aliceSID+"/bash", `{"command":"echo tok=$PLAT_TOKEN"}`)
	if !strings.Contains(w.Body.String(), "tok=ta") {
		t.Fatalf("! escape did not get alice's token: %d %s", w.Code, w.Body)
	}
}
```

- [ ] **Step 2: Run** `go test ./server/ -run 'ShellEnvFor|BangEscape'` — Expected: FAIL.

- [ ] **Step 3: Implement** in `server/identity_cookie.go`:

```go
// shellEnvFor returns the shell env entry carrying owner's current platform
// token, or nil (single-user mode, or no live token for owner — never another
// user's).
func (d serverDeps) shellEnvFor(owner string) []string {
	if d.Cookie == nil || owner == "" {
		return nil
	}
	tok, ok := d.Cookie.tokens.Get(owner)
	if !ok {
		return nil
	}
	return []string{d.Cookie.cfg.TokenEnv + "=" + tok}
}
```

  - `server/sse.go` `handleMessages`, after `runCtx = fstools.WithCwd(runCtx, cwd)`: `runCtx = fstools.WithShellEnv(runCtx, d.shellEnvFor(meta.UserID))`.
  - `server/mailbox_push.go` `injectTurnRouted`, next to `ctx = events.WithRootSession(ctx, sessionID)`: `ctx = fstools.WithShellEnv(ctx, d.shellEnvFor(userID))` (`userID` is the function's owner parameter; add the `fstools "github.com/blouargant/omnis/core/tools"` import if absent).
  - `server/bash.go` `handleBash`: pass `fstools.WithShellEnv(c.Request.Context(), d.shellEnvFor(ownerOfSession(d, id)))` to `RunBashInteractive`, where `ownerOfSession` is:

```go
func ownerOfSession(d serverDeps, id string) string {
	if m, ok := d.Registry.Snapshot(id); ok {
		return m.UserID
	}
	return ""
}
```

  (put it in `identity_cookie.go`; check the import alias used in `bash.go` — it uses `tools.` — keep that alias.)
  - Terminal: change `startPTYSession(dir string)` to `startPTYSession(dir string, env []string)` in both platform files; unix: `cmd.Env = append(append(os.Environ(), "TERM=xterm-256color"), env...)`. `runTerminalSession(ws, dir)` → `runTerminalSession(ws, dir, env)`. In `handleTerminal`: `env := d.shellEnvFor(requestLogin(c))` (requestLogin is "" in single-user mode ⇒ nil).

- [ ] **Step 4: Run** `go test ./server/ ./core/tools/ -race` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make vet
git add server
git commit -m "feat(server): hand the session owner's token to shell tools, ! and the terminal"
```

---

### Task 8: Per-user preferences and working directories

**Files:**
- Modify: `server/identity_cookie.go` (`userRoot`), `server/preferences.go`, `server/prompt_suggest.go`, `server/whatsnew.go`, `server/server.go` (routes), `server/bash.go` (`bashCwdStore` per-login global), `server/folder_ops.go`, `server/uploads.go`, `server/agentmd.go`, `server/terminal.go`, POST `/sessions` (start dir)
- Test: `server/identity_user_state_test.go`

**Interfaces:**
- Produces: `func userRoot(login string) string` (`paths.ConfigWriteDir()` when login "", else `<ConfigWriteDir>/users/<LoginSegment(login)>`); `func userWorkDir(login string) string` (`userRoot(login)/work`, created on demand; "" when login ""); `type prefStores struct`, `func newPrefStores() *prefStores`, `(*prefStores).forLogin(login string) *preferencesStore`; `(*bashCwdStore).getGlobalFor(login string) string`, `(*bashCwdStore).setGlobalFor(login, dir string)`.

- [ ] **Step 1: Failing test** — `server/identity_user_state_test.go`:

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferencesArePerUser(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	if w := doWithBody(r, "ta", "PUT", "/api/preferences", `{"theme":"github-dark"}`); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if body := doAs(r, "tb", "GET", "/api/preferences").Body.String(); strings.Contains(body, "github-dark") {
		t.Fatalf("bob sees alice's theme: %s", body)
	}
	if body := doAs(r, "ta", "GET", "/api/preferences").Body.String(); !strings.Contains(body, "github-dark") {
		t.Fatalf("alice lost her theme: %s", body)
	}
}

func TestUserRoots(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if userRoot("") == userRoot("alice") {
		t.Fatal("per-user root must differ from the shared root")
	}
	if filepath.Base(userRoot("A/B")) != "a_b" {
		t.Fatalf("login must be sanitised: %s", userRoot("A/B"))
	}
	if userWorkDir("") != "" {
		t.Fatal("no work dir outside cookie mode")
	}
}

func TestNewSessionStartsInUserWorkDir(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	w := doWithBody(r, "ta", "POST", "/api/sessions", `{}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	body := doAs(r, "ta", "GET", "/api/folder").Body.String()
	if !strings.Contains(body, filepath.Join("users", "alice", "work")) {
		t.Fatalf("global folder for alice should be her work dir: %s", body)
	}
}
```

- [ ] **Step 2: Run** `go test ./server/ -run 'PerUser|UserRoots|UserWorkDir'` — Expected: FAIL.

- [ ] **Step 3: Implement.**

`server/identity_cookie.go`:

```go
func userRoot(login string) string {
	if login == "" {
		return paths.ConfigWriteDir()
	}
	return filepath.Join(paths.ConfigWriteDir(), "users", identity.LoginSegment(login))
}

func userWorkDir(login string) string {
	if login == "" {
		return ""
	}
	dir := filepath.Join(userRoot(login), "work")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
```

`server/preferences.go`:

```go
// prefStores hands out one preferencesStore per login (cookie mode) or the
// single shared one (login "").
type prefStores struct {
	mu     sync.Mutex
	shared *preferencesStore
	byUser map[string]*preferencesStore
}

func newPrefStores(cf configFiles) *prefStores {
	return &prefStores{shared: newPreferencesStore(cf), byUser: map[string]*preferencesStore{}}
}

func (p *prefStores) forLogin(login string) *preferencesStore {
	if login == "" {
		return p.shared
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.byUser[login]
	if !ok {
		s = &preferencesStore{path: filepath.Join(userRoot(login), "preferences.json")}
		p.byUser[login] = s
	}
	return s
}
```

`registerPreferencesRoutes(rg, store *preferencesStore)` → `registerPreferencesRoutes(rg, stores *prefStores)` with `store := stores.forLogin(requestLogin(c))` inside each handler; same change for `handleSuggestion(d, prefs)` / `suggestionsEnabled` (take the per-request store) and `registerWhatsNewRoutes`. In `server.go`: `prefStore := newPrefStores(d.ConfigFiles)`. Update any test that calls these with a `*preferencesStore` to wrap it: `&prefStores{shared: s, byUser: map[string]*preferencesStore{}}`.

`server/bash.go` `bashCwdStore`: add field `userDef map[string]string` (init in `newBashCwdStore`) and:

```go
// getGlobalFor is the session-less browse cwd for login; login "" ⇒ the
// shared one (single-user behaviour unchanged).
func (s *bashCwdStore) getGlobalFor(login string) string {
	if login == "" {
		return s.getGlobal()
	}
	s.mu.Lock()
	d, ok := s.userDef[login]
	s.mu.Unlock()
	if ok && d != "" {
		return d
	}
	return userWorkDir(login)
}

func (s *bashCwdStore) setGlobalFor(login, dir string) {
	if login == "" {
		s.setGlobal(dir)
		return
	}
	if dir == "" {
		return
	}
	s.mu.Lock()
	s.userDef[login] = dir
	s.mu.Unlock()
}
```

Replace every `bashCwd.getGlobal()` / `bashCwd.setGlobal(dir)` inside a gin handler (`folder_ops.go` ×5, `uploads.go` ×2, `agentmd.go` ×1, `bash.go` ×2, `terminal.go` ×1) with `bashCwd.getGlobalFor(requestLogin(c))` / `bashCwd.setGlobalFor(requestLogin(c), dir)`.

POST `/sessions`: `startDir := bashCwd.get(meta.ID)` → 

```go
		startDir := bashCwd.get(meta.ID)
		if wd := userWorkDir(requestLogin(c)); wd != "" {
			startDir = wd
		}
```

(the explicit `body.Dir` / collection cwd overrides below it stay).

- [ ] **Step 4: Run** `go test ./server/ -race` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make vet
git add server
git commit -m "feat(server): per-user preferences and working directories in cookie mode"
```

---

### Task 9: Per-user collections

**Files:**
- Modify: `internal/sessions/collections.go`, `internal/collectionctx/collectionctx.go`, `agent/collection_plugin.go`, `server/collections.go`, `server/collection_memory.go`, `server/collection_autoupdate.go`, `server/export_import.go`, `server/session_list.go`, `server/server.go`, `server/main.go`
- Test: `internal/sessions/collections_root_test.go`, `internal/collectionctx/root_test.go`, `server/identity_collections_test.go`

**Interfaces:**
- Produces:
  - `type Collections struct{ root string }`; `func CollectionsIn(root string) *Collections`; every exported package-level collections function gains a method of the same name and signature on `*Collections` (`ListCollections`, `AddCollection`, `RemoveCollection`, `RenameCollection`, `CollectionColors`, `SetCollectionColor`, `CollectionProfile`, `SetCollectionProfile`, `CollectionProfileFull`, `UpdateCollectionProfile`, `SetCollectionProfileData`, `SetCollectionMemoryUpdate`, `CollectionsPath`); the package-level functions become one-line wrappers over `CollectionsIn(paths.ConfigWriteDir())`.
  - `type Store struct{ base string }`; `func In(root string) *Store` (base = `root/collections`); methods mirroring every exported `collectionctx` function; package-level functions wrap `In(paths.ConfigWriteDir())`. The render cache key includes `base`.
  - `func SetCollectionRootResolver(f func(sessionID string) string)` in `agent` (optional; nil ⇒ shared root).
  - server: `func collectionsFor(c *gin.Context) *sessions.Collections` = `sessions.CollectionsIn(userRoot(requestLogin(c)))`; `func ctxStoreFor(login string) *collectionctx.Store` = `collectionctx.In(userRoot(login))`.

- [ ] **Step 1: Failing tests.**

`internal/sessions/collections_root_test.go`:

```go
package sessions

import (
	"path/filepath"
	"testing"
)

func TestCollectionsAreRootScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	a := CollectionsIn(filepath.Join(home, "users", "alice"))
	b := CollectionsIn(filepath.Join(home, "users", "bob"))
	if _, _, err := a.AddCollection("Infra"); err != nil {
		t.Fatal(err)
	}
	if names, _ := b.ListCollections(); len(names) != 0 {
		t.Fatalf("bob sees alice's collections: %v", names)
	}
	if names, _ := ListCollections(); len(names) != 0 {
		t.Fatalf("shared root must be untouched: %v", names)
	}
	if names, _ := a.ListCollections(); len(names) != 1 || names[0] != "Infra" {
		t.Fatalf("alice: %v", names)
	}
}
```

`internal/collectionctx/root_test.go`:

```go
package collectionctx

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreIsRootScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	a := In(filepath.Join(home, "users", "alice"))
	b := In(filepath.Join(home, "users", "bob"))
	if err := a.WriteInstructions("Infra", "alice rules"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.Resolve("Infra"), "alice rules") {
		t.Fatal("bob resolved alice's collection context")
	}
	if !strings.Contains(a.Resolve("Infra"), "alice rules") {
		t.Fatal("alice lost her context")
	}
	if strings.Contains(Resolve("Infra"), "alice rules") {
		t.Fatal("shared root must be untouched")
	}
}
```

`server/identity_collections_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestCollectionsArePerUser(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	if w := doWithBody(r, "ta", "POST", "/api/collections", `{"name":"Infra"}`); w.Code/100 != 2 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if body := doAs(r, "tb", "GET", "/api/collections").Body.String(); strings.Contains(body, "Infra") {
		t.Fatalf("bob sees alice's collection: %s", body)
	}
	if w := doAs(r, "tb", "GET", "/api/collections/Infra/context"); w.Code == 200 && strings.Contains(w.Body.String(), "Infra") {
		// bob addressing "Infra" must hit HIS (non-existent) collection, not alice's
		t.Fatalf("bob reached alice's collection context: %s", w.Body)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/sessions/ ./internal/collectionctx/ ./server/ -run 'RootScoped|PerUser'` — Expected: FAIL.

- [ ] **Step 3: Refactor `internal/collectionctx`.** Add:

```go
// Store is the collection-context store rooted at one state root (the shared
// $OMNIS_HOME, or a user's directory in the shared server). The package-level
// functions operate on the shared root.
type Store struct{ base string }

// In returns the store whose collections live under root/collections.
func In(root string) *Store { return &Store{base: filepath.Join(root, "collections")} }

func shared() *Store { return &Store{base: baseDir()} }
```

Turn each exported function into a method using `s.base` in place of `baseDir()` (`Dir`, `InstructionsPath`, `MemoryPath`, `PrevMemoryPath`, `ReadInstructions`, `ReadMemory`, `ReadPrevMemory`, `WriteInstructions`, `WriteMemory`, `WritePrevMemory`, `HasPrevMemory`, `RemovePrevMemory`, `RenameDir`, `RemoveDir`, `HasContext`, `Resolve`), and keep each package-level name as `func X(args) R { return shared().X(args) }`. In `Resolve`, key the cache by `s.base + "\x00" + name` instead of `name`.

- [ ] **Step 4: Refactor `internal/sessions/collections.go`** the same way: `type Collections struct{ root string }`, `func CollectionsIn(root string) *Collections`, `func sharedCollections() *Collections { return CollectionsIn(paths.ConfigWriteDir()) }`. Inside, `CollectionsPath()` → `c.path()` = `filepath.Join(c.root, "collections.json")`, `saveFileLocked`/`loadFileLocked` become methods using `c.root` (the temp file is created in `c.root`, `MkdirAll(c.root)`), and the cascade calls in `RenameCollection`/`RemoveCollection` use `collectionctx.In(c.root).RenameDir(...)` / `.RemoveDir(...)`. Keep the single package mutex `collectionsMu`. Every exported package-level function becomes a wrapper over `sharedCollections()`.

- [ ] **Step 5: Agent resolver.** In `agent/collection_plugin.go` add, mirroring the existing resolver:

```go
var (
	collectionRootMu       sync.RWMutex
	collectionRootResolver func(sessionID string) string
)

// SetCollectionRootResolver installs an optional session→state-root resolver
// (the shared server's per-user directory). nil or "" ⇒ the shared root.
func SetCollectionRootResolver(f func(sessionID string) string) {
	collectionRootMu.Lock()
	collectionRootResolver = f
	collectionRootMu.Unlock()
}
```

and where the plugin calls `collectionctx.Resolve(name)`, use:

```go
	collectionRootMu.RLock()
	rootFn := collectionRootResolver
	collectionRootMu.RUnlock()
	if rootFn != nil {
		if root := rootFn(sessionID); root != "" {
			return collectionctx.In(root).Resolve(name)
		}
	}
	return collectionctx.Resolve(name)
```

In `server/main.go`, next to `agent.SetCollectionResolver(...)`, only when `cookie != nil`:

```go
	if cookie != nil {
		agent.SetCollectionRootResolver(func(sessionID string) string {
			if m, ok := registry.Snapshot(sessionID); ok {
				return userRoot(m.UserID)
			}
			return ""
		})
		defer agent.SetCollectionRootResolver(nil)
	}
```

- [ ] **Step 6: Server call sites.** In `server/collections.go`, `collection_memory.go`, `collection_autoupdate.go`, `export_import.go`, `session_list.go` and the POST `/sessions` handler, replace every `sessions.<CollectionsFn>(…)` with `cols.<CollectionsFn>(…)` where `cols := collectionsFor(c)` in handlers, or `sessions.CollectionsIn(userRoot(owner))` in the auto-update worker / memory distiller (owner = the session's `UserID`; in single-user mode `userRoot(web-user)` would be WRONG — so use `userRoot(loginOrEmpty)` where `loginOrEmpty` is `""` when `d.Cookie == nil`). Same for `collectionctx.X(…)` → `ctxStoreFor(login).X(…)`. Add in `identity_cookie.go`:

```go
func collectionsFor(c *gin.Context) *sessions.Collections {
	return sessions.CollectionsIn(userRoot(requestLogin(c)))
}

func ctxStoreFor(login string) *collectionctx.Store { return collectionctx.In(userRoot(login)) }

// ownerRootLogin maps a session owner to the login used for per-user state:
// the owner in cookie mode, "" (shared root) otherwise.
func (d serverDeps) ownerRootLogin(owner string) string {
	if d.Cookie == nil {
		return ""
	}
	return owner
}
```

`grep -n "sessions\.\(ListCollections\|AddCollection\|RenameCollection\|RemoveCollection\|CollectionColors\|SetCollectionColor\|CollectionProfile\|SetCollectionProfile\|CollectionProfileFull\|UpdateCollectionProfile\|SetCollectionProfileData\|SetCollectionMemoryUpdate\)\|collectionctx\.[A-Z]" server/*.go | grep -v _test` must return nothing when done (except `sessions.NormalizeCollectionName` / `ValidCollectionName` / `ValidCollectionColor` / `ValidMemorySize` / `GeneralCollection`, which are pure helpers and stay package-level).

- [ ] **Step 7: Run** `make test` — Expected: PASS (the existing collections tests exercise the shared-root wrappers).

- [ ] **Step 8: Commit**

```bash
make vet
git add internal/sessions internal/collectionctx agent server
git commit -m "feat(collections): root-scoped collection stores; per-user collections in cookie mode"
```

---

### Task 10: Web UI — login redirect and identity footer

**Files:**
- Modify: `web/app.js` (`apiFetch`, `loadWhoami`), `web/index.html` (bump `app.js?v=`)
- Test: manual (Task 13) + `node --check web/app.js`

- [ ] **Step 1: Implement.** In `apiFetch`, replace the 401 line with:

```js
  if (res.status === 401) {
    let loginURL = "";
    try { loginURL = (await res.clone().json()).login_url || ""; } catch (_) { /* not JSON */ }
    if (loginURL) {
      // Shared (cookie-identity) server: the platform session is missing or
      // expired — go log in there and come back here.
      window.top.location.href = loginURL.replace("{return}", encodeURIComponent(window.location.href));
      throw new Error("unauthorized");
    }
    promptForToken();
    throw new Error("unauthorized");
  }
```

In `loadWhoami`, change the early-return to show the login whenever the server is in cookie mode:

```js
    const user = payload && typeof payload.user_id === "string" ? payload.user_id : "";
    const shared = payload && payload.identity_mode === "cookie";
    if (!user || (user === "web-user" && !shared)) return;
```

Bump the `app.js?v=` query in `web/index.html` (increment the number).

- [ ] **Step 2: Check syntax** — Run: `node --check web/app.js` — Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add web/app.js web/index.html
git commit -m "feat(web): redirect to the platform login on a cookie-mode 401; show the shared-mode login"
```

---

### Task 11: Documentation

**Files:**
- Modify: `CLAUDE.md`, `internal/features/FEATURES.md`
- Create: `docs/iaparc-shared-deployment.md`

- [ ] **Step 1: CLAUDE.md.** Add a section "### Shared deployment (cookie identity)" right after "### Multi-user deployment (per-user containers)", summarising: the mode and its exclusivity with milestone 1; `internal/identity` (validator, cache, token store); ownership guard keyed on `c.FullPath()` (and its route-enumeration test); per-user state under `$OMNIS_HOME/users/<login>/` (preferences, collections, work dir); push-event scoping (`pushMsg.Owner`, `pushVisible`); token delivery via `fstools.WithShellEnv` (not hooks/MCP/LSP/`run_tests`); the **accepted test-tier limits** (no OS isolation, shared Settings, `search_sessions`/`read_session` agent tools are not owner-filtered, the agent can print its own token); link to the spec. Add `internal/identity` to the Key packages table and the nine `OMNIS_IDENTITY_MODE` / `OMNIS_AUTH_*` vars to the env-var table.

- [ ] **Step 2: FEATURES.md.** Under the current `## A.B (in development)` section (create it above the latest release per the file's rules if absent), add:

```markdown
- **Shared server with platform login** — one omnis-server can serve several users of a platform that already has a web login: each user is recognised from their session cookie, sees only their own chats, and the agent runs platform CLI commands with their identity.
```

- [ ] **Step 3: Operator guide** `docs/iaparc-shared-deployment.md`: what the mode is, the security posture (trusted users only), the env vars with the IA Parc values, build/push/deploy commands from Task 12, and how to verify (Task 13 steps).

- [ ] **Step 4: Verify the features file still parses** — Run: `go test ./internal/features/` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md internal/features/FEATURES.md docs/iaparc-shared-deployment.md
git commit -m "docs: shared cookie-identity mode"
```

---

### Task 12: Test deployment artifacts

**Files:**
- Create: `packaging/k8s/iaparc-test/Dockerfile`, `build.sh`, `omnis.yaml` (all manifests), `README.md`
- Test: `packaging/k8s_iaparc_test.go`

- [ ] **Step 1: Failing guard test** — `packaging/k8s_iaparc_test.go`:

```go
package packaging

import (
	"os"
	"strings"
	"testing"
)

// The shared test deployment must never ship a token in the iapcli config,
// must mount it read-only, must not run as root, must not grant the pod a
// ServiceAccount token, and must keep A2A off.
func TestIaparcTestManifest(t *testing.T) {
	b, err := os.ReadFile("k8s/iaparc-test/omnis.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := string(b)
	for _, want := range []string{
		"namespace: test-system",
		"OMNIS_IDENTITY_MODE",
		"automountServiceAccountToken: false",
		"runAsNonRoot: true",
		"readOnly: true",
		"iapregistrykey",
		"nginx.ingress.kubernetes.io/auth-url",
		"path: /omnis",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
	for _, bad := range []string{"IAPCLI_TOKEN:", "OMNIS_CONFIG_PATH", "OMNIS_USER_ID", "a2a_enabled: true"} {
		if strings.Contains(m, bad) {
			t.Errorf("manifest must not contain %q", bad)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./packaging/ -run TestIaparcTestManifest` — Expected: FAIL (file missing).

- [ ] **Step 3: Dockerfile** `packaging/k8s/iaparc-test/Dockerfile` (build context = repo root; `build.sh` stages `.build/`):

```dockerfile
# Shared omnis-server for the IA Parc test platform (cookie identity mode).
# Built by packaging/k8s/iaparc-test/build.sh — do not build by hand.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 ca-certificates git \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -m -u 10001 -s /bin/bash omnis
COPY bin/omnis bin/omnis-server /usr/bin/
COPY .build/iapcli /usr/bin/iapcli
COPY config/*.json /etc/omnis/
COPY config/hooks/k8s-validate.py /etc/omnis/hooks/k8s-validate.py
COPY registry /etc/omnis/registry
COPY web /usr/share/omnis/web
COPY .build/agentskills /etc/agentskills
RUN chmod 0755 /etc/omnis/hooks/k8s-validate.py
ENV OMNIS_WEB_DIR=/usr/share/omnis/web HOME=/home/omnis OMNIS_HOME=/data
USER 10001
WORKDIR /home/omnis
EXPOSE 8080
ENTRYPOINT ["/usr/bin/omnis-server"]
```

- [ ] **Step 4: build.sh** (`chmod +x`):

```bash
#!/usr/bin/env bash
# Build and push iaparc/omnis-server:dev-<version> for the IA Parc test cluster.
# Requires: docker login to Docker Hub (org "iaparc"), iapcli installed locally,
# the iaparc skills at /etc/agentskills/skills/iaparc.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
VERSION=$(git describe --tags --always --dirty)
IMAGE=${IMAGE:-iaparc/omnis-server:dev-${VERSION}}
CGO_ENABLED=0 make build
rm -rf .build && mkdir -p .build/agentskills/skills
cp "$(command -v iapcli)" .build/iapcli
cp -r /etc/agentskills/skills/iaparc .build/agentskills/skills/
docker build -f packaging/k8s/iaparc-test/Dockerfile -t "$IMAGE" .
if [[ "${PUSH:-1}" == "1" ]]; then docker push "$IMAGE"; fi
echo "$IMAGE"
```

Add `.build/` to `.gitignore`.

- [ ] **Step 5: Manifests** `packaging/k8s/iaparc-test/omnis.yaml` (image tag substituted by `sed` at deploy time — `IMAGE_PLACEHOLDER`; login field / roles field / cookie values from Task 0):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: omnis-config
  namespace: test-system
data:
  OMNIS_IDENTITY_MODE: "cookie"
  OMNIS_AUTH_COOKIES: "iaparc_token,auth._token.local"
  OMNIS_AUTH_VALIDATE_CMD: "iapcli -C /etc/iapcli/.iapcli.yaml user get"
  OMNIS_AUTH_TOKEN_ENV: "IAPCLI_TOKEN"
  OMNIS_AUTH_LOGIN_FIELD: "login"
  OMNIS_AUTH_ROLES_FIELD: "roles"
  OMNIS_AUTH_ADMIN_ROLES: "admin"
  OMNIS_AUTH_LOGIN_URL: "https://test.iaparc.atoutlinux.net/sso/login"
  OMNIS_SERVER_BASE_PATH: "/omnis"
  OMNIS_SERVER_ADDR: ":8080"
  OMNIS_UPDATE_CHECK: "false"
  server.yaml: |
    open_browser: false
    a2a_enabled: false
    update_check: false
  .iapcli.yaml: |
    contexts:
        default:
            address: iaparc-go-operator-grpc.test-system.svc.cluster.local
            port: "443"
            insecure: ""
            auth: {token: ""}
    current_context: default
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: omnis-data
  namespace: test-system
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: nfs-provisioner
  resources:
    requests:
      storage: 10Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: omnis
  namespace: test-system
  labels: {app: omnis}
spec:
  replicas: 1
  strategy: {type: Recreate}
  selector:
    matchLabels: {app: omnis}
  template:
    metadata:
      labels: {app: omnis}
    spec:
      automountServiceAccountToken: false
      imagePullSecrets:
        - name: iapregistrykey
      securityContext:
        runAsNonRoot: true
        runAsUser: 10001
        fsGroup: 10001
      containers:
        - name: omnis
          image: IMAGE_PLACEHOLDER
          ports:
            - containerPort: 8080
          envFrom:
            - configMapRef: {name: omnis-config}
          env:
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef: {name: omnis-llm, key: api_key}
            - name: OPENAI_BASE_URL
              valueFrom:
                secretKeyRef: {name: omnis-llm, key: base_url}
          volumeMounts:
            - {name: data, mountPath: /data}
            - {name: config, mountPath: /etc/omnis/server.yaml, subPath: server.yaml, readOnly: true}
            - {name: config, mountPath: /etc/iapcli/.iapcli.yaml, subPath: .iapcli.yaml, readOnly: true}
          readinessProbe:
            httpGet: {path: /omnis/api/health, port: 8080}
          resources:
            requests: {cpu: 250m, memory: 512Mi}
            limits: {memory: 2Gi}
      volumes:
        - name: data
          persistentVolumeClaim: {claimName: omnis-data}
        - name: config
          configMap: {name: omnis-config}
---
apiVersion: v1
kind: Service
metadata:
  name: omnis
  namespace: test-system
spec:
  selector: {app: omnis}
  ports:
    - port: 8080
      targetPort: 8080
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: omnis
  namespace: test-system
  annotations:
    nginx.ingress.kubernetes.io/auth-url: http://iaparc-go-api.test-system.svc.cluster.local:9080/sso/verify/
    nginx.ingress.kubernetes.io/auth-signin: https://test.iaparc.atoutlinux.net/sso/login
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: "64m"
spec:
  ingressClassName: nginx
  rules:
    - host: test.iaparc.atoutlinux.net
      http:
        paths:
          - path: /omnis
            pathType: Prefix
            backend:
              service:
                name: omnis
                port: {number: 8080}
```

Before finalising: read the existing ingresses (`kubectl -n test-system get ingress iaparc-portal-ingress -o yaml`) and match their `ingressClassName`/TLS stanza (copy the `tls:` block if they have one), and read the gRPC service name/port the portal's operator uses (`kubectl -n test-system get svc | grep -i grpc`) to set the `.iapcli.yaml` address/port/`insecure`. If the in-cluster gRPC needs TLS SNI of the public host, use `address: test.iaparc.atoutlinux.net` / `port: "443"` instead (as on the dev machine).

- [ ] **Step 6: README.md** in `packaging/k8s/iaparc-test/` — the deploy sequence:

```bash
export KUBECONFIG=~/kubeconfig-milkyway-tests
IMAGE=$(packaging/k8s/iaparc-test/build.sh | tail -1)
kubectl -n test-system create secret generic omnis-llm \
  --from-literal=api_key="$OPENAI_API_KEY" --from-literal=base_url="$OPENAI_BASE_URL" \
  --dry-run=client -o yaml | kubectl apply -f -
sed "s|IMAGE_PLACEHOLDER|$IMAGE|" packaging/k8s/iaparc-test/omnis.yaml | kubectl apply -f -
kubectl -n test-system rollout status deploy/omnis
```

- [ ] **Step 7: Run** `go test ./packaging/ -run TestIaparcTestManifest` — Expected: PASS. Then `PUSH=0 packaging/k8s/iaparc-test/build.sh` — Expected: image builds locally. Then `docker run --rm --entrypoint iapcli <image> --version` — Expected: `iapcli version 0.25.2`.

- [ ] **Step 8: Commit**

```bash
git add packaging/k8s packaging/k8s_iaparc_test.go .gitignore
git commit -m "build: IA Parc test deployment for the shared cookie-identity server"
```

---

### Task 13: Deploy and validate on the cluster (manual, with the user)

- [ ] **Step 1: Confirm before publishing.** Ask the user to confirm the Docker Hub `iaparc/omnis-server` repository is private (the image embeds the IA Parc skills), then run `packaging/k8s/iaparc-test/build.sh` (push).

- [ ] **Step 2: Deploy** with the README sequence (confirm with the user before `kubectl apply` in `test-system` — it is a shared namespace).

- [ ] **Step 3: Unauthenticated redirect.** `curl -sI https://test.iaparc.atoutlinux.net/omnis/` — Expected: a 302 to `/sso/login` (nginx external auth).

- [ ] **Step 4: Logged-in smoke (user in their browser).** Open `/omnis/` → footer shows "Signed in as blouargant@chapsvision.com"; start a chat and ask "liste mes projets iaparc". Expected: the agent loads the `iaparc` skill and `iapcli projects …` answers under that identity. Check `kubectl -n test-system logs deploy/omnis | grep -c '<first 8 chars of the token>'` returns 0 (token never logged).

- [ ] **Step 5: No token fallback.** `kubectl -n test-system exec deploy/omnis -- sh -c 'cd /tmp && iapcli user get; echo exit=$?'` — Expected: "You need first to login", non-zero exit. And `kubectl -n test-system exec deploy/omnis -- sh -c 'ls -la ~/.iapcli.yaml 2>&1'` — Expected: no such file.

- [ ] **Step 6: Isolation.** With a second IA Parc account (the user provides it or creates a test user), confirm each user sees only their own sessions and collections, and that opening the other user's session URL answers 404.

- [ ] **Step 7: Record results** in `docs/iaparc-shared-deployment.md` ("Validated on <date>") and commit.

---

## Self-Review Notes

- Spec §3 → Tasks 1, 4, 10. §4 → Tasks 3, 5, 6, 8, 9 (A2A refusal in Task 4). §5 → Tasks 2, 7. §6 → Task 12 (`omnis-llm` secret). §7 → Task 12. §8 → tests in every task + Tasks 0 and 13. §10 → Task 11.
- Accepted limits written into CLAUDE.md (Task 11): the `sessions` agent tool group (`search_sessions`, `read_session`) is not owner-filtered in the test tier; Settings/`set_preference` tool writes the shared files.
