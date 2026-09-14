# Multi-user milestone 1 — per-user container isolation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let omnis-server run as one process per user inside that user's existing container, behind the platform's OIDC gateway: it knows which user it serves, stamps that identity on every session, refuses requests whose asserted identity does not match, and ships with the supervisord/container assets and guard tests to deploy it.

**Architecture:** Kernel isolation comes from the container (the process runs as the user's UID, in the user's NFS home). omnis gains a process-wide configured user id (`sessions.UserID()`, from `OMNIS_USER_ID`) persisted as `user_id` on every conversation file, an identity middleware composed after the existing token middleware on `/api/*` (`OMNIS_IDENTITY_HEADER`), and a `GET /api/whoami` route surfaced in the web UI sidebar. No change to agents, tools, MCP, LSP, hooks, or the terminal.

**Tech Stack:** Go (gin, gopkg.in/yaml.v3), vanilla JS web UI, supervisord, Docker, Kubernetes NetworkPolicy (example only).

**Spec:** `docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md`

## Global Constraints

- **No-op contract:** with `OMNIS_USER_ID` and `OMNIS_IDENTITY_HEADER` unset, every existing deployment must behave byte-identically (sessions still carry `web-user`, no identity check, no sidebar label).
- **Identity header without an explicit user id is a startup error** (spec §6.3): the `"web-user"` fallback never satisfies the check.
- **Middleware order on `/api/*`: token first, identity second** (spec §6.3).
- **Persisted `user_id` is stamped only when empty** — a file already attributed to another login is never re-attributed (spec §8.1).
- **Packaging assets must never set `OMNIS_CONFIG_PATH` or `OMNIS_SYSTEM_CONFIG_DIR`** (spec §7.2; the `.deb` layout is already the default system layer).
- **The image's `server.yaml` must have `open_browser: false`, `update_check: false`, `a2a_enabled: false`, an empty `token`, a non-empty `identity_header`, and no `addr`** (spec §7.1).
- **Docs and FEATURES.md are English.** Every commit message ends with the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Run `gofmt`/`go vet` cleanly; the repo's `make test` must stay green after every task.
- Work happens on branch `feature/multi-user-m1` (already created; the spec is committed there).

---

## File structure

| File | Responsibility |
|---|---|
| `internal/sessions/sessions.go` (modify) | `UserID()` / `SetUserID()` — the process-wide configured session owner; `New`/`NewWithName` use it. |
| `internal/sessions/history.go` (modify) | `ConversationFile.UserID` persisted as `user_id`; stamped in `SaveConversationFile` when empty; read back by `LoadPersistedSessions`. |
| `internal/sessions/userid_test.go` (create) | Tests for the above. |
| `server/scheduler.go`, `server/spawn.go`, `server/fork_rewind.go`, `server/export_import.go`, `server/a2a_server.go`, `server/server.go` (modify) | Replace `sessions.DefaultUserID` with `sessions.UserID()` at every runtime call site. |
| `server/identity.go` (create) | `identityMiddleware`, `resolveIdentity` (env > yaml + validation), `handleWhoami`. |
| `server/identity_test.go` (create) | Unit tests for the three, plus a real-router test of the composed `/api/*` group. |
| `server/config.go` (modify) | `ServerConfig.UserID`, `ServerConfig.IdentityHeader`. |
| `server/server.go` (modify) | `serverDeps.IdentityHeader`; `auth` group composes the identity middleware; `GET /api/whoami`. |
| `server/main.go` (modify) | Resolve identity at boot, `sessions.SetUserID`, log, pass to deps. |
| `web/index.html`, `web/app.js`, `web/css/features/sidebar.css`, `web/i18n/{en,fr,es,de}.json`, `web/i18n/locales.js` (modify) | Sidebar "Signed in as <login>" label fed by `/api/whoami`. |
| `packaging/supervisord/omnis-server.conf` (create) | supervisord program template (envsubst placeholders). |
| `packaging/container/Dockerfile`, `packaging/container/server.yaml`, `packaging/container/networkpolicy.example.yaml` (create) | Reference image + image-side server config + example NetworkPolicy. |
| `packaging/container_test.go` (create) | Guard tests for the two assets above. |
| `docs/multi-user-containers.md` (create), `CLAUDE.md`, `internal/features/FEATURES.md`, `packaging/README.md` (modify) | Operator guide + project docs. |

---

### Task 1: Configurable session owner (`internal/sessions`)

**Files:**
- Modify: `internal/sessions/sessions.go` (the `DefaultUserID` block near line 17; `NewWithName` ~line 121; `New` ~line 164)
- Modify: `internal/sessions/history.go` (`ConversationFile` struct ~line 119; `SaveConversationFile` ~line 188; `LoadPersistedSessions` ~line 471)
- Test: `internal/sessions/userid_test.go`

**Interfaces:**
- Produces: `func UserID() string` — the configured owner, `DefaultUserID` (`"web-user"`) when unset. `func SetUserID(id string)` — trims; empty restores the default. `ConversationFile.UserID string` (`json:"user_id,omitempty"`).
- Later tasks call `sessions.UserID()` (Task 2, 3, 4) and `sessions.SetUserID(...)` (Task 4 at boot; tests).

- [ ] **Step 1: Write the failing tests**

Create `internal/sessions/userid_test.go`:

```go
package sessions

import (
	"encoding/json"
	"os"
	"testing"
)

func TestUserIDDefaultsToWebUserAndIsConfigurable(t *testing.T) {
	t.Cleanup(func() { SetUserID("") })

	if got := UserID(); got != DefaultUserID {
		t.Fatalf("UserID() before SetUserID = %q, want %q", got, DefaultUserID)
	}
	SetUserID("  alice ")
	if got := UserID(); got != "alice" {
		t.Fatalf("UserID() after SetUserID = %q, want trimmed %q", got, "alice")
	}
	reg := NewEmptyRegistry()
	if m := reg.New("system"); m.UserID != "alice" {
		t.Errorf("Registry.New: UserID = %q, want alice", m.UserID)
	}
	m, ok := reg.NewWithName("named-one", "system")
	if !ok || m.UserID != "alice" {
		t.Errorf("Registry.NewWithName: ok=%v UserID=%q, want alice", ok, m.UserID)
	}
	SetUserID("")
	if got := UserID(); got != DefaultUserID {
		t.Fatalf("SetUserID(\"\") must restore the default, got %q", got)
	}
}

func TestConversationFilePersistsUserID(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Cleanup(func() { SetUserID("") })
	SetUserID("alice")

	if err := AppendConversationTurn("teaching-kite", "hi", "hello"); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConversationFile("teaching-kite")
	if err != nil {
		t.Fatal(err)
	}
	if f.UserID != "alice" {
		t.Fatalf("persisted user_id = %q, want alice", f.UserID)
	}
	// A fork is written through SaveConversationFile too, so it inherits the owner.
	if _, err := ForkConversation("teaching-kite", "teaching-kite-fork", "fork", 1); err != nil {
		t.Fatal(err)
	}
	ff, err := LoadConversationFile("teaching-kite-fork")
	if err != nil || ff == nil || ff.UserID != "alice" {
		t.Fatalf("fork user_id: file=%v err=%v, want alice", ff, err)
	}
	metas := LoadPersistedSessions()
	if len(metas) != 2 {
		t.Fatalf("LoadPersistedSessions returned %d sessions, want 2", len(metas))
	}
	for _, m := range metas {
		if m.UserID != "alice" {
			t.Errorf("LoadPersistedSessions %s: UserID = %q, want alice", m.ID, m.UserID)
		}
	}
}

func TestLoadPersistedSessionsAttributesLegacyFilesToConfiguredUser(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Cleanup(func() { SetUserID("") })
	if err := os.MkdirAll(logsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	turns := `[{"user_text":"q","assistant_text":"a","at":"2026-01-01T00:00:00Z"}]`
	// Pre-multi-user file: no user_id key at all.
	legacy := `{"title":"old","turns":` + turns + `}`
	// A file already attributed to someone else must keep its owner.
	owned := `{"title":"hers","user_id":"carol","turns":` + turns + `}`
	if err := os.WriteFile(ConversationPath("legacy-fox"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConversationPath("owned-owl"), []byte(owned), 0o644); err != nil {
		t.Fatal(err)
	}
	SetUserID("bob")

	got := map[string]string{}
	for _, m := range LoadPersistedSessions() {
		got[m.ID] = m.UserID
	}
	if got["legacy-fox"] != "bob" {
		t.Errorf("legacy file: UserID = %q, want the configured user bob", got["legacy-fox"])
	}
	if got["owned-owl"] != "carol" {
		t.Errorf("owned file: UserID = %q, want carol (never re-attributed on load)", got["owned-owl"])
	}

	// A write stamps the configured user on the legacy file only.
	if err := AppendConversationTurn("legacy-fox", "q2", "a2"); err != nil {
		t.Fatal(err)
	}
	if err := AppendConversationTurn("owned-owl", "q2", "a2"); err != nil {
		t.Fatal(err)
	}
	var lf, of ConversationFile
	for id, dst := range map[string]*ConversationFile{"legacy-fox": &lf, "owned-owl": &of} {
		data, err := os.ReadFile(ConversationPath(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, dst); err != nil {
			t.Fatal(err)
		}
	}
	if lf.UserID != "bob" || of.UserID != "carol" {
		t.Fatalf("after write: legacy=%q (want bob) owned=%q (want carol)", lf.UserID, of.UserID)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sessions/ -run 'TestUserID|TestConversationFilePersistsUserID|TestLoadPersistedSessionsAttributesLegacy' -v`
Expected: compile FAIL — `undefined: UserID`, `undefined: SetUserID`, `f.UserID undefined`.

- [ ] **Step 3: Implement the configured owner in `sessions.go`**

Add `"strings"` to the import block. Replace the `DefaultUserID` block with:

```go
// DefaultUserID is the user ID every session is attributed to when no owner
// is configured (a single-user install: web UI, TUI, A2A). The value is part
// of the on-disk session naming scheme (agent.SessionSuffix), so do not change
// it without a migration. A multi-user deployment runs one omnis-server per
// user and configures the real login with SetUserID (OMNIS_USER_ID) — see
// docs/multi-user-containers.md.
const DefaultUserID = "web-user"

var (
	userIDMu sync.RWMutex
	userID   = DefaultUserID
)

// UserID returns the login every session created by this process is
// attributed to: the value given to SetUserID, or DefaultUserID when none was.
func UserID() string {
	userIDMu.RLock()
	defer userIDMu.RUnlock()
	return userID
}

// SetUserID configures the process-wide session owner. The server calls it
// once at boot, before the registry is built, from OMNIS_USER_ID. Whitespace
// is trimmed; an empty id restores DefaultUserID.
func SetUserID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = DefaultUserID
	}
	userIDMu.Lock()
	userID = id
	userIDMu.Unlock()
}
```

In both `NewWithName` and `New`, change the struct literal line `UserID: DefaultUserID,` to `UserID: UserID(),`.

- [ ] **Step 4: Persist and restore `user_id` in `history.go`**

In `ConversationFile`, add right after the `Title` field:

```go
	// UserID is the login this session is attributed to (sessions.UserID()).
	// Stamped by SaveConversationFile when empty, so every write path (turns,
	// forks, imports) records the owner. Absent in files written before
	// multi-user support; LoadPersistedSessions attributes those to the
	// process's configured user (one container, one user). Never overwritten
	// once set — a file that names another login keeps it.
	UserID string `json:"user_id,omitempty"`
```

At the top of `SaveConversationFile`, before `dir := logsDir()`:

```go
	if f.UserID == "" {
		f.UserID = UserID()
	}
```

In `LoadPersistedSessions`, replace `UserID: DefaultUserID,` with `UserID: uid,` and add, just before the `out = append(out, &SessionMeta{` line:

```go
		uid := f.UserID
		if uid == "" {
			uid = UserID()
		}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/sessions/ -v`
Expected: PASS for the three new tests and every existing test in the package.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/sessions && go vet ./internal/sessions/
git add internal/sessions/sessions.go internal/sessions/history.go internal/sessions/userid_test.go
git commit -m "$(cat <<'EOF'
feat(sessions): configurable session owner persisted as user_id

sessions.UserID()/SetUserID() replace the hard-coded "web-user" as the owner
of every new session (still the default when unset), and ConversationFile
gains a user_id stamped on write when empty and read back on load, so a
per-user omnis-server attributes its sessions to the real login. Groundwork
for the multi-user milestone 1 (per-user containers).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Server call sites use the configured owner

**Files:**
- Modify: `server/scheduler.go:53,62,92,98,197`, `server/spawn.go:36,215`, `server/fork_rewind.go:178,187,214`, `server/export_import.go:208,217`, `server/a2a_server.go:798`, `server/server.go:398,406,687`

**Interfaces:**
- Consumes: `sessions.UserID()` from Task 1.
- Produces: nothing new; every runtime path that registers, watches, injects, or falls back to a user id now uses the configured owner. Test files keep comparing against `sessions.DefaultUserID` (equal to `UserID()` when unset).

- [ ] **Step 1: Replace the constant at every non-test call site**

```bash
sed -i 's/sessions\.DefaultUserID/sessions.UserID()/g' \
  server/scheduler.go server/spawn.go server/fork_rewind.go \
  server/export_import.go server/a2a_server.go server/server.go
grep -rn "sessions.DefaultUserID" server/*.go | grep -v _test.go
```
Expected: the `grep` prints nothing.

- [ ] **Step 2: Fix the one comment the sed cannot reach**

In `server/spawn.go` the `spawnRequest` field comment reads `UserID string // owning user (empty ⇒ DefaultUserID)`. Change it to `UserID string // owning user (empty ⇒ sessions.UserID())`.

- [ ] **Step 3: Confirm import does not carry a foreign owner**

Run: `sed -n 183,190p server/export_import.go`
Expected: `dst := &sessions.ConversationFile{ … }` lists `Title`, `Squad`, `Collection`, `Turns` only — no `UserID`. Because `SaveConversationFile` stamps the local owner on an empty `UserID`, an imported session is attributed to this instance's user, as spec §8.1 requires. If `UserID` **is** copied there, delete that line.

- [ ] **Step 4: Build and run the server tests**

Run: `go build ./... && go test ./server/ 2>&1 | tail -5`
Expected: build OK; `ok  github.com/blouargant/omnis/server`.

- [ ] **Step 5: Commit**

```bash
gofmt -l server && go vet ./server/
git add server/scheduler.go server/spawn.go server/fork_rewind.go server/export_import.go server/a2a_server.go server/server.go
git commit -m "$(cat <<'EOF'
refactor(server): attribute sessions to the configured owner

Every runtime fallback to sessions.DefaultUserID (scheduler, spawn, fork,
import, a2a auto-create, session create/delete) now reads sessions.UserID(),
so a per-user omnis-server keys logs, mailboxes and registrations by the real
login. Behaviour is unchanged when no owner is configured.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Identity middleware, config resolution, and `whoami` handler

**Files:**
- Create: `server/identity.go`
- Test: `server/identity_test.go`

**Interfaces:**
- Consumes: `sessions.UserID()` (Task 1); `envOr(key, def string) string` (exists in `server/main.go:543`); `serverDeps` (exists in `server/server.go`) — this task reads a field `d.IdentityHeader` that Task 4 adds, so **Task 3's `handleWhoami` test compiles only after Task 4 adds the field**; to keep Task 3 self-contained, `handleWhoami` takes the header as a plain argument instead (see code).
- Produces:
  - `func identityMiddleware(header, expected string) gin.HandlerFunc` — no-op when `header == ""`; 401 `{"error":"missing identity header"}` when absent/blank; 403 `{"error":"identity mismatch"}` when trimmed value ≠ `expected`.
  - `type identityConfig struct { UserID, Header string }` and `func resolveIdentity(cfg ServerConfig) (identityConfig, error)` — env `OMNIS_USER_ID` / `OMNIS_IDENTITY_HEADER` over yaml `cfg.UserID` / `cfg.IdentityHeader`; error when header set and user id empty.
  - `func handleWhoami(identityHeader string) gin.HandlerFunc` — `200 {"user_id": sessions.UserID(), "identity_enforced": identityHeader != ""}`.

- [ ] **Step 1: Write the failing tests**

Create `server/identity_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
	"github.com/gin-gonic/gin"
)

func newIdentityRouter(header, expected string) *gin.Engine {
	r := gin.New()
	r.GET("/protected", identityMiddleware(header, expected), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestIdentityMiddlewareDisabledWhenNoHeaderConfigured(t *testing.T) {
	r := newIdentityRouter("", "alice")
	for _, v := range []string{"", "bob", "alice"} {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		if v != "" {
			req.Header.Set("X-Forwarded-User", v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("header value %q: got %d want 200 (middleware must be a no-op)", v, w.Code)
		}
	}
}

func TestIdentityMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		set        bool
		wantStatus int
		wantErr    string
	}{
		{"missing header", "", false, http.StatusUnauthorized, "missing identity header"},
		{"blank header", "   ", true, http.StatusUnauthorized, "missing identity header"},
		{"other login", "bob", true, http.StatusForbidden, "identity mismatch"},
		{"case differs", "Alice", true, http.StatusForbidden, "identity mismatch"},
		{"exact login", "alice", true, http.StatusOK, ""},
		{"surrounding whitespace is trimmed", "  alice ", true, http.StatusOK, ""},
	}
	r := newIdentityRouter("X-Forwarded-User", "alice")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tc.set {
				req.Header.Set("X-Forwarded-User", tc.value)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status: got %d want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantErr != "" && !strings.Contains(w.Body.String(), tc.wantErr) {
				t.Fatalf("body %q should mention %q", w.Body.String(), tc.wantErr)
			}
		})
	}
}

func TestResolveIdentity(t *testing.T) {
	t.Run("nothing configured", func(t *testing.T) {
		t.Setenv("OMNIS_USER_ID", "")
		t.Setenv("OMNIS_IDENTITY_HEADER", "")
		got, err := resolveIdentity(ServerConfig{})
		if err != nil || got.UserID != "" || got.Header != "" {
			t.Fatalf("got %+v err=%v, want empty config and nil error", got, err)
		}
	})
	t.Run("yaml values apply", func(t *testing.T) {
		t.Setenv("OMNIS_USER_ID", "")
		t.Setenv("OMNIS_IDENTITY_HEADER", "")
		got, err := resolveIdentity(ServerConfig{UserID: " alice ", IdentityHeader: "X-Forwarded-User"})
		if err != nil || got.UserID != "alice" || got.Header != "X-Forwarded-User" {
			t.Fatalf("got %+v err=%v, want alice / X-Forwarded-User", got, err)
		}
	})
	t.Run("env overrides yaml", func(t *testing.T) {
		t.Setenv("OMNIS_USER_ID", "bob")
		t.Setenv("OMNIS_IDENTITY_HEADER", "X-Auth-Request-User")
		got, err := resolveIdentity(ServerConfig{UserID: "alice", IdentityHeader: "X-Forwarded-User"})
		if err != nil || got.UserID != "bob" || got.Header != "X-Auth-Request-User" {
			t.Fatalf("got %+v err=%v, want bob / X-Auth-Request-User", got, err)
		}
	})
	t.Run("header without explicit user id is rejected", func(t *testing.T) {
		t.Setenv("OMNIS_USER_ID", "")
		t.Setenv("OMNIS_IDENTITY_HEADER", "X-Forwarded-User")
		if _, err := resolveIdentity(ServerConfig{}); err == nil {
			t.Fatal("expected an error: the web-user default must never satisfy an identity check")
		}
	})
	t.Run("user id alone is fine", func(t *testing.T) {
		t.Setenv("OMNIS_USER_ID", "alice")
		t.Setenv("OMNIS_IDENTITY_HEADER", "")
		got, err := resolveIdentity(ServerConfig{})
		if err != nil || got.UserID != "alice" || got.Header != "" {
			t.Fatalf("got %+v err=%v, want alice with no header", got, err)
		}
	})
}

func TestHandleWhoami(t *testing.T) {
	t.Cleanup(func() { sessions.SetUserID("") })
	sessions.SetUserID("alice")

	for _, tc := range []struct {
		header       string
		wantEnforced bool
	}{{"", false}, {"X-Forwarded-User", true}} {
		r := gin.New()
		r.GET("/api/whoami", handleWhoami(tc.header))
		req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", w.Code, w.Body.String())
		}
		var body struct {
			UserID   string `json:"user_id"`
			Enforced bool   `json:"identity_enforced"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.UserID != "alice" || body.Enforced != tc.wantEnforced {
			t.Fatalf("header=%q: got %+v, want user alice enforced=%v", tc.header, body, tc.wantEnforced)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./server/ -run 'TestIdentity|TestResolveIdentity|TestHandleWhoami' -v 2>&1 | head -5`
Expected: compile FAIL — `undefined: identityMiddleware`, `undefined: resolveIdentity`, `undefined: handleWhoami`, `ServerConfig has no field UserID` (the field arrives in Task 4; to compile this task alone, add the two `ServerConfig` fields from Task 4 Step 1 now — they are inert until wired).

- [ ] **Step 3: Add the two `ServerConfig` fields (from Task 4) so the test compiles**

In `server/config.go`, right after the `Token` field:

```go
	// UserID is the login this server instance serves. A multi-user deployment
	// runs one omnis-server per user (docs/multi-user-containers.md) and sets
	// it to that user's login; empty keeps the single-user default ("web-user").
	// Overridden by OMNIS_USER_ID.
	UserID string `yaml:"user_id,omitempty" json:"user_id,omitempty"`
	// IdentityHeader names the request header the SSO gateway fills with the
	// authenticated login (e.g. "X-Forwarded-User"). When set, every /api/*
	// request must carry it with exactly UserID's value: 401 when missing, 403
	// on a mismatch. Requires UserID — the default would match any misrouted
	// request. Overridden by OMNIS_IDENTITY_HEADER.
	IdentityHeader string `yaml:"identity_header,omitempty" json:"identity_header,omitempty"`
```

- [ ] **Step 4: Implement `server/identity.go`**

```go
package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/blouargant/omnis/internal/sessions"
	"github.com/gin-gonic/gin"
)

// identityConfig is the resolved per-process identity: the login this server
// serves and the gateway header carrying the asserted login (empty = no check).
// See docs/multi-user-containers.md and the design spec
// docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md §6.
type identityConfig struct {
	UserID string
	Header string
}

// resolveIdentity applies env > server.yaml for OMNIS_USER_ID and
// OMNIS_IDENTITY_HEADER and rejects the one unsafe combination: an identity
// header with no explicit user id. Comparing against the shared "web-user"
// default would let a misrouted request through, which is exactly what the
// check exists to refuse.
func resolveIdentity(cfg ServerConfig) (identityConfig, error) {
	uid := strings.TrimSpace(envOr("OMNIS_USER_ID", cfg.UserID))
	header := strings.TrimSpace(envOr("OMNIS_IDENTITY_HEADER", cfg.IdentityHeader))
	if header != "" && uid == "" {
		return identityConfig{}, errors.New("server: OMNIS_IDENTITY_HEADER (identity_header) is set but OMNIS_USER_ID (user_id) is not — " +
			"an identity check needs the explicit login to compare against; the \"web-user\" default would accept any misrouted request")
	}
	return identityConfig{UserID: uid, Header: header}, nil
}

// identityMiddleware enforces the login the SSO gateway asserts on every
// request of the protected /api/* group. It runs AFTER authMiddleware, so a
// caller without the container's bearer token never learns the expected login.
//
//   - header == ""            ⇒ not enforced (single-user install; no-op contract)
//   - header missing or blank ⇒ 401 "missing identity header"
//   - header ≠ expected        ⇒ 403 "identity mismatch" (exact, case-sensitive, trimmed)
//
// It is a belt against the gateway misrouting a request to the wrong user's
// container; the per-container bearer token is the braces (spec §5).
func identityMiddleware(header, expected string) gin.HandlerFunc {
	header = strings.TrimSpace(header)
	if header == "" {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		got := strings.TrimSpace(c.GetHeader(header))
		if got == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing identity header"})
			return
		}
		if got != expected {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "identity mismatch"})
			return
		}
		c.Next()
	}
}

// handleWhoami reports the user this process serves and whether the identity
// header is enforced. The web UI shows the login in the sidebar footer so a
// user landing on someone else's instance sees it before doing anything.
func handleWhoami(identityHeader string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"user_id":           sessions.UserID(),
			"identity_enforced": strings.TrimSpace(identityHeader) != "",
		})
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./server/ -run 'TestIdentity|TestResolveIdentity|TestHandleWhoami' -v 2>&1 | tail -20`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l server && go vet ./server/
git add server/identity.go server/identity_test.go server/config.go
git commit -m "$(cat <<'EOF'
feat(server): identity middleware, whoami handler, and identity config

identityMiddleware enforces the login an SSO gateway asserts (401 missing,
403 mismatch; no-op when unconfigured), resolveIdentity applies env > yaml
for OMNIS_USER_ID / OMNIS_IDENTITY_HEADER and refuses a header without an
explicit user id, and handleWhoami reports the served user. Not yet wired
into the router — see the following commit.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Wire identity into the router and the boot sequence

**Files:**
- Modify: `server/server.go` (`serverDeps` struct ~line 39; `newEngine` `auth := api.Group(...)` ~line 244)
- Modify: `server/main.go` (after the token log block ~line 149; the `deps := serverDeps{` literal ~line 409)
- Test: `server/identity_test.go` (append one real-router test)

**Interfaces:**
- Consumes: `identityMiddleware`, `resolveIdentity`, `handleWhoami` (Task 3); `sessions.SetUserID`/`UserID` (Task 1).
- Produces: `serverDeps.IdentityHeader string`; route `GET /api/whoami` on the protected group; boot-time `sessions.SetUserID(ident.UserID)` **before** `sessions.NewRegistry()` (the registry load falls back to `UserID()` for legacy files).

- [ ] **Step 1: Write the failing real-router test**

Append to `server/identity_test.go`:

```go
// TestIdentityEnforcedOnAPIGroup drives the real router: the token is checked
// first (a caller without the secret never learns the expected login), then the
// identity header, on every protected route; /api/health stays open.
func TestIdentityEnforcedOnAPIGroup(t *testing.T) {
	t.Cleanup(func() { sessions.SetUserID("") })
	sessions.SetUserID("alice")
	engine := newEngine(serverDeps{
		Token:          "s3cret",
		IdentityHeader: "X-Forwarded-User",
		Registry:       sessions.NewEmptyRegistry(),
		rootCtx:        context.Background(),
	})

	call := func(path, auth, login string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if login != "" {
			req.Header.Set("X-Forwarded-User", login)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	if w := call("/api/health", "", ""); w.Code != http.StatusOK {
		t.Fatalf("/api/health must stay open: %d", w.Code)
	}
	if w := call("/api/whoami", "", "alice"); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "bearer") {
		t.Fatalf("no token: got %d %s, want 401 about the bearer token (token is checked first)", w.Code, w.Body.String())
	}
	if w := call("/api/whoami", "Bearer wrong", "alice"); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid token") {
		t.Fatalf("wrong token + right login: got %d %s, want 401 invalid token", w.Code, w.Body.String())
	}
	if w := call("/api/whoami", "Bearer s3cret", ""); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "identity header") {
		t.Fatalf("token, no login: got %d %s, want 401 missing identity header", w.Code, w.Body.String())
	}
	if w := call("/api/whoami", "Bearer s3cret", "bob"); w.Code != http.StatusForbidden {
		t.Fatalf("token, other login: got %d %s, want 403", w.Code, w.Body.String())
	}
	w := call("/api/whoami", "Bearer s3cret", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("token + right login: got %d %s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		UserID   string `json:"user_id"`
		Enforced bool   `json:"identity_enforced"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "alice" || !body.Enforced {
		t.Fatalf("whoami body %+v, want alice / enforced", body)
	}
	// Another protected route is covered by the same group.
	if w := call("/api/sessions", "Bearer s3cret", "bob"); w.Code != http.StatusForbidden {
		t.Fatalf("/api/sessions with other login: got %d, want 403", w.Code)
	}
}
```

Add `"context"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./server/ -run TestIdentityEnforcedOnAPIGroup -v 2>&1 | head -5`
Expected: compile FAIL — `unknown field IdentityHeader in struct literal of type serverDeps`.

- [ ] **Step 3: Add the field and compose the group in `server/server.go`**

In `serverDeps`, right after `Token string`:

```go
	// IdentityHeader names the request header the SSO gateway fills with the
	// authenticated login. Non-empty ⇒ identityMiddleware enforces it on every
	// /api/* route against sessions.UserID(), after the token check. Empty in a
	// single-user install. See docs/multi-user-containers.md.
	IdentityHeader string
```

Replace `auth := api.Group("", authMiddleware(d.Token))` with:

```go
	// Token first, identity second: a caller without the container's secret
	// never learns which login the identity check expects (spec §6.3).
	auth := api.Group("", authMiddleware(d.Token), identityMiddleware(d.IdentityHeader, sessions.UserID()))
	// GET /api/whoami — the user this instance serves + whether the identity
	// header is enforced. Shown in the web UI sidebar footer.
	auth.GET("/whoami", handleWhoami(d.IdentityHeader))
```

- [ ] **Step 4: Resolve identity at boot in `server/main.go`**

Right after the `if token == "" { log.Println("server: OMNIS_SERVER_TOKEN not set — …") }` block, add:

```go
	// Per-user identity (multi-user deployments run one omnis-server per user
	// behind an SSO gateway — docs/multi-user-containers.md). Must run before
	// sessions.NewRegistry(): the registry attributes legacy conversation files
	// to sessions.UserID().
	ident, err := resolveIdentity(serverCfg)
	if err != nil {
		return err
	}
	sessions.SetUserID(ident.UserID)
	switch {
	case ident.Header != "":
		log.Printf("server: serving user %q — identity header %q enforced on /api/*", sessions.UserID(), ident.Header)
	case ident.UserID != "":
		log.Printf("server: serving user %q (no identity header check)", sessions.UserID())
	}
```

(If `err` is already declared in scope at that point, use `ident, err := …` → `ident, rerr := …` and `return rerr`.)

In the `deps := serverDeps{` literal, add `IdentityHeader: ident.Header,` right after `Token: token,`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go build ./... && go test ./server/ 2>&1 | tail -3 && go test ./server/ -run 'TestIdentity|TestAuth' -v 2>&1 | grep -E '^(=== RUN|--- (PASS|FAIL)|ok|FAIL)' | tail -25`
Expected: build OK, `ok github.com/blouargant/omnis/server`, every identity/auth test PASS (including the pre-existing `TestAuthMiddleware*`, which must still pass with the extra middleware absent when `IdentityHeader == ""`).

- [ ] **Step 6: Commit**

```bash
gofmt -l server && go vet ./server/
git add server/server.go server/main.go server/identity_test.go
git commit -m "$(cat <<'EOF'
feat(server): enforce the gateway-asserted identity on /api/* and add whoami

The protected group now runs authMiddleware then identityMiddleware, GET
/api/whoami reports the served user, and boot resolves OMNIS_USER_ID /
OMNIS_IDENTITY_HEADER (env > server.yaml), sets sessions.SetUserID before the
registry loads, and fails fast on an identity header without an explicit user
id. Unconfigured ⇒ byte-identical to before.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Web UI — "Signed in as <login>" in the sidebar footer

**Files:**
- Modify: `web/index.html` (inside `<div id="sidebar-footer">`, after the `documentation-btn` button; the `<script … ?v=>` tags at the bottom)
- Modify: `web/app.js` (new `loadWhoami()` near `maybePromptWhatsNew` ~line 8208; call it in the boot IIFE ~line 12532)
- Modify: `web/css/features/sidebar.css` (after the `#sidebar-footer button { margin-top: 0; }` rule ~line 648)
- Modify: `web/i18n/en.json`, `web/i18n/fr.json`, `web/i18n/es.json`, `web/i18n/de.json`; regenerate `web/i18n/locales.js`

**Interfaces:**
- Consumes: `GET /api/whoami` → `{user_id, identity_enforced}` (Task 4); existing `apiFetch`, `tr`, `data-i18n`.
- Produces: `#sidebar-user` / `#sidebar-user-name` elements; i18n key `app.whoami.label`.

- [ ] **Step 1: Add the markup**

In `web/index.html`, inside `<div id="sidebar-footer">`, immediately after the closing `</button>` of `<button id="documentation-btn" …>`, insert:

```html
      <div id="sidebar-user" hidden>
        <span data-i18n="app.whoami.label">Signed in as</span>
        <span id="sidebar-user-name"></span>
      </div>
```

At the bottom of the file bump the cache-busters: `assets/i18n/locales.js?v=62` → `?v=63` and `assets/app.js?v=91` → `?v=92`.

- [ ] **Step 2: Style it**

Append to `web/css/features/sidebar.css`, right after `#sidebar-footer button { margin-top: 0; }`:

```css
/* "Signed in as <login>" — shown only when this omnis-server serves a real
   user (multi-user deployments run one server per user); hidden on a
   single-user install and in the collapsed rail. */
#sidebar-user {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  font-size: 12px;
  color: var(--text-muted);
  min-width: 0;
}
#sidebar-user-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
#sidebar.collapsed #sidebar-user { display: none; }
```

- [ ] **Step 3: Add the i18n key to the four catalogues**

Insert one line right after the `"app.whatsnew.title": …,` line of each file (keep the trailing comma pattern of its neighbours):

| file | line |
|---|---|
| `web/i18n/en.json` | `"app.whoami.label": "Signed in as",` |
| `web/i18n/fr.json` | `"app.whoami.label": "Connecté en tant que",` |
| `web/i18n/es.json` | `"app.whoami.label": "Conectado como",` |
| `web/i18n/de.json` | `"app.whoami.label": "Angemeldet als",` |

Then run: `make i18n`
Expected: `web/i18n/locales.js` regenerated with no "missing keys" warning for fr/es/de.

- [ ] **Step 4: Fetch and render at boot in `web/app.js`**

Add right before `async function maybePromptWhatsNew()`:

```js
// loadWhoami shows the login this omnis-server serves in the sidebar footer.
// A multi-user deployment runs one server per user behind an SSO gateway
// (docs/multi-user-containers.md); a single-user install reports the default
// id and shows nothing, so the footer is unchanged there.
async function loadWhoami() {
  const box = document.getElementById("sidebar-user");
  const name = document.getElementById("sidebar-user-name");
  if (!box || !name) return;
  try {
    const res = await apiFetch("/api/whoami");
    if (!res.ok) return;
    const payload = await res.json();
    const user = payload && typeof payload.user_id === "string" ? payload.user_id : "";
    if (!user || user === "web-user") return;
    name.textContent = user;
    box.hidden = false;
  } catch (e) { console.error("whoami failed:", e); }
}
```

In the boot IIFE at the end of the file, add `loadWhoami();` as the first line of the `(async () => { … })();` block that calls `maybePromptLocale()` (fire-and-forget, before the `await`):

```js
  (async () => {
    loadWhoami(); // sidebar "Signed in as" — no-op on a single-user install
    await maybePromptLocale(); // may location.reload() when the user switches
    await maybePromptNotifications();
    maybePromptWhatsNew(); // once per upgrade; no-op on dev builds / when caught up
  })();
```

- [ ] **Step 5: Verify in the browser**

Run (from the repo root, in the background; `env -u OMNIS_CONFIG_PATH` because the login shell exports that bypass):

```bash
OMNIS_HOME=$(mktemp -d) OMNIS_USER_ID=alice OMNIS_SERVER_ADDR=127.0.0.1:18080 OMNIS_WEB_DIR=$(pwd)/web env -u OMNIS_CONFIG_PATH go run ./server
```

Open `http://127.0.0.1:18080/`. Expected: the sidebar footer shows **Signed in as alice** under Documentation; collapsing the sidebar hides it; no console error. Stop the server, rerun **without** `OMNIS_USER_ID`, reload: the footer is unchanged (no label). Switch the language to French in Settings → Appearance: the label reads **Connecté en tant que alice**.

- [ ] **Step 6: Commit**

```bash
git add web/index.html web/app.js web/css/features/sidebar.css web/i18n/en.json web/i18n/fr.json web/i18n/es.json web/i18n/de.json web/i18n/locales.js
git commit -m "$(cat <<'EOF'
feat(web): show the served login in the sidebar footer

GET /api/whoami feeds a "Signed in as <login>" line under the footer buttons
(en/fr/es/de). Hidden on a single-user install and in the collapsed rail, so
existing deployments look the same.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Packaging assets (supervisord program, reference image, NetworkPolicy) + guard tests

**Files:**
- Create: `packaging/supervisord/omnis-server.conf`
- Create: `packaging/container/Dockerfile`
- Create: `packaging/container/server.yaml`
- Create: `packaging/container/networkpolicy.example.yaml`
- Test: `packaging/container_test.go`
- Modify: `docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md` §7.2 (placeholder syntax — see Step 3)

**Interfaces:**
- Consumes: the `.deb` produced by `make package` (`dist/omnis_<version>_linux_x86_64.deb`, per `.goreleaser.yaml` nfpms `file_name_template`), the env vars from Task 4.
- Produces: files an operator copies into the platform's per-user image; guard tests in `make test`.

- [ ] **Step 1: Write the failing guard tests**

Create `packaging/container_test.go`:

```go
package packaging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// assetPath resolves a file under packaging/ relative to this test file, so
// the tests read the shipped assets from the repo tree (same precedent as
// profile_test.go).
func assetPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file location via runtime.Caller")
	}
	return filepath.Join(append([]string{filepath.Dir(thisFile)}, parts...)...)
}

// findIniAssign returns the first non-comment line assigning name (`name=…`),
// skipping supervisord (`;`) and shell (`#`) comment lines so a variable may be
// discussed in prose without tripping a check.
func findIniAssign(content, name string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, name+"=") {
			return line
		}
	}
	return ""
}

// TestSupervisordProgramRunsAsTheUserInsideTheirHome pins the properties the
// multi-user milestone-1 design (docs/superpowers/specs/2026-09-14-multi-user-
// container-isolation-design.md §7.2) relies on: the program runs as the user,
// keeps its state under that user's home, names the user, receives the
// per-container secret, and never bypasses the 3-layer config merge.
func TestSupervisordProgramRunsAsTheUserInsideTheirHome(t *testing.T) {
	path := assetPath(t, "supervisord", "omnis-server.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)

	if line := findIniAssign(content, "user"); !strings.Contains(line, "${OMNIS_LOGIN}") {
		t.Errorf("%s must run omnis-server as the user (user=${OMNIS_LOGIN}), got: %q", path, line)
	}
	if line := findIniAssign(content, "OMNIS_HOME"); !strings.Contains(line, `"${HOME}/.omnis"`) {
		t.Errorf("%s must place OMNIS_HOME under the user's HOME, got: %q", path, line)
	}
	if line := findIniAssign(content, "OMNIS_USER_ID"); !strings.Contains(line, "${OMNIS_LOGIN}") {
		t.Errorf("%s must set OMNIS_USER_ID to the login, got: %q", path, line)
	}
	if line := findIniAssign(content, "OMNIS_SERVER_TOKEN"); line == "" {
		t.Errorf("%s must pass OMNIS_SERVER_TOKEN (the per-container secret the gateway injects)", path)
	}
	for _, forbidden := range []string{"OMNIS_CONFIG_PATH", "OMNIS_SYSTEM_CONFIG_DIR"} {
		if line := findIniAssign(content, forbidden); line != "" {
			t.Errorf("%s must NOT set %s — the .deb layout is already the default system layer, and "+
				"OMNIS_CONFIG_PATH bypasses the 3-layer merge (CLAUDE.md \"Distribution / packaging\").\noffending line: %s",
				path, forbidden, line)
		}
	}
}

// TestContainerServerYAMLDisablesWhatTheImageOwns pins spec §7.1: the image is
// the update channel, has no display, exposes no A2A listener yet, gets its
// token from the environment, enforces an identity header, and leaves the
// listen address to the supervisord program.
func TestContainerServerYAMLDisablesWhatTheImageOwns(t *testing.T) {
	path := assetPath(t, "container", "server.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, key := range []string{"open_browser", "update_check", "a2a_enabled"} {
		v, ok := cfg[key]
		b, isBool := v.(bool)
		if !ok || !isBool || b {
			t.Errorf("%s: %s must be explicitly false (got %v)", path, key, v)
		}
	}
	if tok, _ := cfg["token"].(string); tok != "" {
		t.Errorf("%s: token must be empty — it comes from OMNIS_SERVER_TOKEN, never from the image", path)
	}
	if h, _ := cfg["identity_header"].(string); strings.TrimSpace(h) == "" {
		t.Errorf("%s: identity_header must name the gateway's login header", path)
	}
	if _, has := cfg["addr"]; has {
		t.Errorf("%s: addr must not be set here — the listen address is OMNIS_SERVER_ADDR in the supervisord program", path)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./packaging/ -run 'TestSupervisord|TestContainerServerYAML' -v 2>&1 | tail -6`
Expected: FAIL — `read …/supervisord/omnis-server.conf: no such file or directory` and the same for `container/server.yaml`.

- [ ] **Step 3: Create the supervisord template (and align the spec's snippet)**

Create `packaging/supervisord/omnis-server.conf`:

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
;   OMNIS_BASE_PATH     URL prefix when the gateway routes by path; empty when
;                       it routes by host
;   LITELLM_API_KEY     the user's LiteLLM virtual key (per-user cost attribution)
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
environment=HOME="${HOME}",OMNIS_HOME="${HOME}/.omnis",OMNIS_USER_ID="${OMNIS_LOGIN}",OMNIS_SERVER_TOKEN="${OMNIS_SERVER_TOKEN}",OMNIS_SERVER_ADDR="127.0.0.1:8080",OMNIS_SERVER_BASE_PATH="${OMNIS_BASE_PATH}",OMNIS_WEB_DIR="/usr/share/omnis/web",LITELLM_API_KEY="${LITELLM_API_KEY}"
```

Then update the spec's §7.2 snippet to match: replace its `%(ENV_…)s` program block with the file above and add this sentence under "Program definition": *"`supervisord` does not expand `%(ENV_…)s` in its `user=` option, so the template uses `${VAR}` placeholders rendered by `envsubst` in the container entrypoint."* Also amend the three bullets in the spec's "Rules the template must satisfy" list to reference `${OMNIS_LOGIN}` / `${HOME}`.

- [ ] **Step 4: Create the image-side `server.yaml`**

Create `packaging/container/server.yaml`:

```yaml
# omnis-server configuration baked into the per-user container image.
# Installed at /etc/omnis/server.yaml (the default system layer). Per-user
# overrides still land in $OMNIS_HOME (the user's NFS home) as usual.

# addr is NOT set here: the listen address is OMNIS_SERVER_ADDR in the
# supervisord program, so the platform owns it in one place.

# Comes from OMNIS_SERVER_TOKEN (a per-container secret the gateway injects);
# never store it in the image.
token: ""

# The container is the user's environment; there is no display to open.
open_browser: false

# The image is the update channel — no GitHub polling from inside a pod.
update_check: false

# Cross-container A2A is out of scope for milestone 1.
a2a_enabled: false

# The request header the SSO gateway fills with the authenticated login. Every
# /api/* request must carry it with exactly OMNIS_USER_ID's value (401 when
# missing, 403 on a mismatch). Adjust to the gateway's actual header name.
identity_header: X-Forwarded-User
```

- [ ] **Step 5: Create the reference Dockerfile and the NetworkPolicy example**

Create `packaging/container/Dockerfile`:

```dockerfile
# Reference image: omnis-server as a supervisord service inside a per-user
# container (docs/multi-user-containers.md). The platform owns the base image
# (Unix accounts, NFS home mount, supervisord, the home-init service and the
# envsubst entrypoint); this layer only adds omnis.
#
# Build from the repo root after `make package`:
#   docker build -f packaging/container/Dockerfile \
#     --build-arg BASE_IMAGE=<platform base image> -t omnis-user:dev .
ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE}

# The .deb lays binaries at /usr/bin, the system config at /etc/omnis (the
# default system layer — no OMNIS_SYSTEM_CONFIG_DIR needed) and the web UI at
# /usr/share/omnis/web. It declares python3 as a dependency (shipped k8s hook).
COPY dist/omnis_*_linux_x86_64.deb /tmp/omnis.deb
RUN apt-get update \
 && apt-get install -y --no-install-recommends /tmp/omnis.deb supervisor gettext-base \
 && rm -rf /var/lib/apt/lists/* /tmp/omnis.deb

# Image-side server settings (no browser, no self-update, no A2A, identity header).
COPY packaging/container/server.yaml /etc/omnis/server.yaml

# supervisord program TEMPLATE — the entrypoint renders it per user:
#   envsubst < /etc/omnis/supervisord/omnis-server.conf.tmpl \
#     > /etc/supervisor/conf.d/omnis-server.conf
COPY packaging/supervisord/omnis-server.conf /etc/omnis/supervisord/omnis-server.conf.tmpl

# No ENTRYPOINT/CMD: the platform's entrypoint starts supervisord.
```

Create `packaging/container/networkpolicy.example.yaml`:

```yaml
# EXAMPLE — allow the omnis port of a per-user pod to be reached ONLY from the
# SSO gateway. This is the second layer of spec §5 (the per-container bearer
# token is the first): without it, a user could reach another user's omnis
# port from their own pod and forge the identity header.
# Adapt namespace/labels to the platform; the port must match OMNIS_SERVER_ADDR.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: omnis-server-from-gateway-only
  namespace: user-workspaces
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/component: user-workspace
  policyTypes: ["Ingress"]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: sso-gateway
          podSelector:
            matchLabels:
              app.kubernetes.io/name: oidc-gateway
      ports:
        - protocol: TCP
          port: 8080
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./packaging/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: `--- PASS` for `TestSupervisordProgramRunsAsTheUserInsideTheirHome`, `TestContainerServerYAMLDisablesWhatTheImageOwns`, and every pre-existing packaging test; `ok`.

- [ ] **Step 7: Commit**

```bash
gofmt -l packaging && go vet ./packaging/
git add packaging/supervisord/omnis-server.conf packaging/container/Dockerfile packaging/container/server.yaml packaging/container/networkpolicy.example.yaml packaging/container_test.go docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md
git commit -m "$(cat <<'EOF'
feat(packaging): per-user container assets + guard tests

Adds the supervisord program template (envsubst placeholders, runs as the
user, state under the user's home, never OMNIS_CONFIG_PATH), the image-side
server.yaml (no browser / self-update / A2A, token from env, identity header),
a reference Dockerfile on top of the .deb, and an example NetworkPolicy
restricting the omnis port to the gateway. packaging/container_test.go pins
the properties the multi-user design relies on. The spec's §7.2 snippet is
aligned with the envsubst form.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Documentation — operator guide, CLAUDE.md, FEATURES.md, packaging README

**Files:**
- Create: `docs/multi-user-containers.md`
- Modify: `CLAUDE.md` (env-var table: add two rows after the `OMNIS_SERVER_BASE_PATH` row; new section right before "### Event audit log (`agent_events_<buildTimestamp>.log`)")
- Modify: `internal/features/FEATURES.md` (append one bullet to the `## 1.9 (in development)` section)
- Modify: `packaging/README.md` (layout list)

**Interfaces:**
- Consumes: everything shipped in Tasks 1–6 (names, routes, variables).

- [ ] **Step 1: Write the operator guide**

Create `docs/multi-user-containers.md`:

````markdown
# Multi-user deployment — one omnis-server per user, in the user's container

**Design:** `docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md`
(milestone 1). This guide is for the platform operator wiring omnis into an
existing per-user container platform behind an SSO gateway.

## Why one process per user

omnis-server is single-user by construction and its bearer token grants the
rights of the Unix account it runs as: the `Bash` tool, the `!` shell-escape,
the terminal, MCP stdio servers, language servers, lifecycle hooks, the Monaco
save route and the Files panel all run on the host with that account's rights.
The permission layer and the Bash safety floor are usability guards against
the *model*, not security boundaries against a *user*.

So the boundary that truly confines script execution to a user's environment
is the **container + the user's UID**: run one omnis-server per user, as that
user, inside that user's container, with its state in that user's home. A
compromised omnis (prompt injection, a malicious skill) then reaches exactly
what the user could reach by logging into their own container — nothing more.

## Topology

```
browser ──HTTPS──▶ OIDC gateway (yours) ──▶ user's container
                     │ authenticates (SSO)      │ supervisord
                     │ routes user → container  │  ├─ home-init (first)
                     │ sets on every request:   │  └─ omnis-server  (runs as the user)
                     │   Authorization: Bearer <container token>
                     │   X-Forwarded-User: <login>        $OMNIS_HOME = $HOME/.omnis (NFS)
```

- The gateway is the only intended entry. It terminates SSO and injects two
  headers: the container's **bearer token** and the authenticated **login**.
- omnis verifies both: the token (`OMNIS_SERVER_TOKEN`, unchanged mechanism)
  and the login (`OMNIS_IDENTITY_HEADER` must equal `OMNIS_USER_ID`).
- omnis does **not** implement OIDC and never parses a JWT.

## Configuration

| Variable (env) | `server.yaml` key | Meaning |
|---|---|---|
| `OMNIS_USER_ID` | `user_id` | The login this instance serves. Every session is attributed to it (`user_id` in the conversation file). Unset ⇒ the single-user default `web-user`. |
| `OMNIS_IDENTITY_HEADER` | `identity_header` | Header the gateway fills with the login, e.g. `X-Forwarded-User`. When set, every `/api/*` request must carry it with exactly `OMNIS_USER_ID`'s value: **401** when missing, **403** on a mismatch. Requires an explicit `OMNIS_USER_ID` (startup error otherwise). |
| `OMNIS_SERVER_TOKEN` | `token` | Per-container secret, generated by the platform, injected by the gateway. Checked **before** the identity header, so a caller without it never learns the expected login. |
| `OMNIS_HOME` | — | `$HOME/.omnis` — sessions, preferences, overlays, indexes. On the NFS home so it survives container restarts. |
| `OMNIS_SERVER_ADDR` | (`addr`) | Listen address inside the container, e.g. `127.0.0.1:8080`. |
| `OMNIS_SERVER_BASE_PATH` | `base_path` | Only when the gateway routes by path prefix; it must forward the prefix, not strip it. |
| `OMNIS_WEB_DIR` | `web_dir` | `/usr/share/omnis/web` on a `.deb` install. |
| `LITELLM_API_KEY` (or the name your `models.json` references) | — | A per-user LiteLLM virtual key gives per-user cost attribution with no omnis change: `models.json` resolves `api_key` values as environment-variable **names**. |

`GET /api/whoami` returns `{"user_id": "...", "identity_enforced": true|false}`;
the web UI shows the login in the sidebar footer.

**Never set `OMNIS_CONFIG_PATH`** (it bypasses the 3-layer config merge and
freezes every per-user override) and do not set `OMNIS_SYSTEM_CONFIG_DIR` on a
`.deb` install (`/etc/omnis` is already the default system layer).

## Assets shipped in this repository

| File | Use |
|---|---|
| `packaging/supervisord/omnis-server.conf` | supervisord program **template** with `${VAR}` placeholders. Render it with `envsubst` in the container entrypoint (supervisord does not expand its `user=` option). Runs as `${OMNIS_LOGIN}`, after the home-init service (`priority=900`), state under `${HOME}/.omnis`. |
| `packaging/container/server.yaml` | Image-side `/etc/omnis/server.yaml`: `open_browser: false`, `update_check: false`, `a2a_enabled: false`, empty `token`, `identity_header`. |
| `packaging/container/Dockerfile` | Reference layer on top of your base image: installs the `.deb` (+ `supervisor`, `gettext-base`), copies the two files above. |
| `packaging/container/networkpolicy.example.yaml` | Example policy allowing the omnis port only from the gateway. |

`packaging/container_test.go` pins these properties in `make test`.

## Gateway prerequisites

- **Set** (not append) `Authorization: Bearer <container token>` and the identity
  header on every proxied request. A browser holding a stale token in
  `localStorage` from an earlier direct deployment would otherwise send its own.
- WebSocket upgrade on `/api/terminal/ws`.
- No response buffering and long idle timeouts on `GET /api/events` and the turn
  stream (`POST …/messages`, `GET …/messages/stream`). A gateway that buffers SSE
  breaks streaming entirely; the client reconnects, but only if frames flow.
- When routing by path, forward the prefix and set `OMNIS_SERVER_BASE_PATH`.

## Two layers, and the weaker fallback

The residual threat omnis covers itself: a user who, from **their** container,
reaches the omnis port of **another** container and forges the login header.

1. **Token** — the per-container secret only the gateway knows. Forging the
   login is useless without it.
2. **Network** — a NetworkPolicy denying container→container traffic on the
   omnis port (see the example).

If your gateway **cannot** inject an `Authorization` header, run with an empty
`OMNIS_SERVER_TOKEN`, `OMNIS_IDENTITY_HEADER` set, and a **strict** NetworkPolicy.
This is one layer instead of two and is **not** the recommended configuration.

## Migrating an existing install

Conversation history (`conversation_<id>.json`) is keyed by session id only and
is untouched when `OMNIS_USER_ID` changes from `web-user` to a real login.
Files written before this version have no `user_id`; they are attributed to the
configured user on load and stamped on their next write. The auxiliary
per-session files (`agent_tasks_*`, `agent_todo_*`, `agent_memory_*`,
`agent_statelog_*`, mailboxes) are keyed by `(user, session)`; their old-suffix
copies become orphans and are swept by the GC. They are scratch state.

## What omnis does not protect against

- A misconfigured gateway that forwards **unauthenticated** traffic with a forged
  login header **and** the right token — the token is the platform's secret to
  keep.
- Anything the user could do themselves in their container: omnis runs with the
  user's rights, no fewer and no more.
- Resource exhaustion inside the container beyond the container's own cgroups
  (omnis's `turn_budget` bounds LLM spend, not CPU/RAM).

## Smoke test on one host (no container)

```bash
OMNIS_HOME=$(mktemp -d) OMNIS_USER_ID=alice OMNIS_IDENTITY_HEADER=X-Forwarded-User \
OMNIS_SERVER_TOKEN=t OMNIS_SERVER_ADDR=127.0.0.1:18080 OMNIS_WEB_DIR=/usr/share/omnis/web \
omnis-server &
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/api/whoami                                  # 401 (no token)
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer t' http://127.0.0.1:18080/api/whoami   # 401 (no login)
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer t' -H 'X-Forwarded-User: bob' http://127.0.0.1:18080/api/whoami    # 403
curl -s -H 'Authorization: Bearer t' -H 'X-Forwarded-User: alice' http://127.0.0.1:18080/api/whoami     # {"identity_enforced":true,"user_id":"alice"}
```
````

- [ ] **Step 2: Update CLAUDE.md**

In the **Environment variables** table, insert two rows right after the `OMNIS_SERVER_BASE_PATH` row:

```markdown
| `OMNIS_USER_ID` | The login this omnis-server instance serves (multi-user deployments run **one process per user** — see "Multi-user deployment (per-user containers)"). Overrides `server.yaml` `user_id`. Sets `sessions.UserID()`, the owner stamped on every session (`user_id` in the conversation file); unset ⇒ the single-user default `web-user` |
| `OMNIS_IDENTITY_HEADER` | Names the request header the SSO gateway fills with the authenticated login (e.g. `X-Forwarded-User`). When set, `identityMiddleware` ([server/identity.go](server/identity.go)) runs **after** the token check on every `/api/*` route: 401 when the header is missing, 403 when it differs from `OMNIS_USER_ID`. Requires an explicit `OMNIS_USER_ID` (startup error otherwise — the default would match any misrouted request). Overrides `server.yaml` `identity_header` |
```

Insert this section right before `### Event audit log (\`agent_events_<buildTimestamp>.log\`)`:

```markdown
### Multi-user deployment (per-user containers)

omnis-server is **single-user by construction**, and its bearer token grants the
rights of the Unix account it runs as (Bash, `!`, terminal, MCP stdio, LSP, hooks
and the file routes all run on the host under that account — the permission
layer and the Bash safety floor guard against the *model*, not against a
*user*). A secure multi-user deployment therefore runs **one omnis-server per
user, as that user, inside that user's container**, behind the platform's SSO
gateway; the container + UID is the security boundary. Design:
[docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md](docs/superpowers/specs/2026-09-14-multi-user-container-isolation-design.md);
operator guide: [docs/multi-user-containers.md](docs/multi-user-containers.md).

What omnis itself contributes (milestone 1):

- **A configured session owner.** `sessions.UserID()` / `SetUserID()`
  ([internal/sessions/sessions.go](internal/sessions/sessions.go)) replace the
  hard-coded `"web-user"` (still the default). Set once at boot from
  `OMNIS_USER_ID` **before** `sessions.NewRegistry()`; every runtime fallback in
  `server/` reads `sessions.UserID()`. `ConversationFile.UserID` (`user_id`) is
  **stamped by `SaveConversationFile` only when empty** — a file naming another
  login is never re-attributed — and read back by `LoadPersistedSessions`
  (legacy files ⇒ the configured user).
- **An identity check.** `identityMiddleware(header, expected)`
  ([server/identity.go](server/identity.go)) is composed **after**
  `authMiddleware` on the `/api/*` group (token first, so a caller without the
  container's secret never learns the expected login): 401 missing / 403
  mismatch; a no-op when `OMNIS_IDENTITY_HEADER` is empty. `resolveIdentity`
  applies env > `server.yaml` (`user_id`, `identity_header`) and **fails boot**
  on a header without an explicit user id. The terminal WebSocket is covered
  transitively (its short-lived token is minted over the checked
  `POST /api/terminal/token`).
- **`GET /api/whoami`** → `{user_id, identity_enforced}`; the web UI shows
  "Signed in as <login>" in the sidebar footer (`loadWhoami`, hidden for
  `web-user` and in the collapsed rail).
- **Packaging assets** under `packaging/supervisord/` (program template with
  `${VAR}` placeholders — supervisord does not expand `%(ENV_…)s` in `user=`,
  so the entrypoint renders it with `envsubst`) and `packaging/container/`
  (image-side `server.yaml`, reference `Dockerfile`, NetworkPolicy example),
  pinned by [packaging/container_test.go](packaging/container_test.go): runs
  as the user, state under the user's home, **never** `OMNIS_CONFIG_PATH` /
  `OMNIS_SYSTEM_CONFIG_DIR`; `open_browser`/`update_check`/`a2a_enabled` false,
  empty `token`, non-empty `identity_header`, no `addr`.

**Two defence layers, not one.** The identity header is a *belt* against the
gateway misrouting; the per-container token is the *braces* against a user
reaching another container's omnis port from their own pod. A NetworkPolicy
restricting that port to the gateway is the recommended third measure. The
documented fallback for a gateway that cannot inject `Authorization` (empty
token + identity header + strict NetworkPolicy) is weaker and says so.

**No-op contract:** with `OMNIS_USER_ID` and `OMNIS_IDENTITY_HEADER` unset,
sessions carry `web-user`, no middleware is installed, the footer shows nothing
— byte-identical to before. **Not in scope (milestones 2–3):** the shared hub
(directory, team collections, shared sessions) and user-to-user messaging;
milestone 1 only lays their two prerequisites — a real `user_id` on every
persisted session and a verified request identity.
```

- [ ] **Step 3: FEATURES.md and packaging README**

Append to the `## 1.9 (in development)` section of `internal/features/FEATURES.md`:

```markdown
- **Per-user identity** — omnis-server can run as one instance per user behind your SSO gateway: it knows which user it serves, refuses requests for anyone else, and shows "Signed in as <login>" in the sidebar.
```

In `packaging/README.md`, after the `/usr/share/omnis/web/` line of the layout block, add a short section:

````markdown
## Per-user container assets (multi-user deployments)

Not installed by the `.deb`/`.rpm`; copied into the platform's per-user image
(see `docs/multi-user-containers.md`):

```
packaging/supervisord/omnis-server.conf          supervisord program template (envsubst ${VAR} placeholders)
packaging/container/Dockerfile                   reference layer on top of the .deb
packaging/container/server.yaml                  image-side /etc/omnis/server.yaml
packaging/container/networkpolicy.example.yaml   restrict the omnis port to the gateway
```

Guarded by `packaging/container_test.go`.
````

- [ ] **Step 4: Verify the docs build nothing breaks**

Run: `go test ./internal/features/ && make test 2>&1 | tail -3`
Expected: the FEATURES parser test passes (the new bullet follows the `- **Title** — description.` shape); `make test` green.

- [ ] **Step 5: Commit**

```bash
git add docs/multi-user-containers.md CLAUDE.md internal/features/FEATURES.md packaging/README.md
git commit -m "$(cat <<'EOF'
docs: multi-user per-user container deployment guide

Operator guide (topology, variables, gateway prerequisites, the two defence
layers and the weaker fallback, migration, smoke test), CLAUDE.md section +
env-var rows, FEATURES.md bullet, packaging README pointer to the container
assets.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: End-to-end smoke verification on this host

**Files:** none modified. This task produces evidence, not code.

**Interfaces:**
- Consumes: the built server (Tasks 1–5), the smoke commands from the operator guide (Task 7).

- [ ] **Step 1: Build and start a server as "alice" with identity enforced**

```bash
make build-server
OMNIS_HOME=$(mktemp -d) OMNIS_USER_ID=alice OMNIS_IDENTITY_HEADER=X-Forwarded-User \
OMNIS_SERVER_TOKEN=t OMNIS_SERVER_ADDR=127.0.0.1:18080 OMNIS_WEB_DIR=$(pwd)/web \
env -u OMNIS_CONFIG_PATH bin/omnis-server > /tmp/omnis-smoke.log 2>&1 &
sleep 2 && grep -E "serving user|listening" /tmp/omnis-smoke.log
```
Expected log lines: `server: serving user "alice" — identity header "X-Forwarded-User" enforced on /api/*` and `server: listening on 127.0.0.1:18080`.

- [ ] **Step 2: Exercise the four outcomes**

```bash
B=http://127.0.0.1:18080
curl -s -o /dev/null -w 'health         %{http_code}\n' $B/api/health
curl -s -o /dev/null -w 'no token       %{http_code}\n' $B/api/whoami
curl -s -o /dev/null -w 'token no login %{http_code}\n' -H 'Authorization: Bearer t' $B/api/whoami
curl -s -o /dev/null -w 'token bob      %{http_code}\n' -H 'Authorization: Bearer t' -H 'X-Forwarded-User: bob' $B/api/whoami
curl -s -w '\n' -H 'Authorization: Bearer t' -H 'X-Forwarded-User: alice' $B/api/whoami
```
Expected:
```
health         200
no token       401
token no login 401
token bob      403
{"identity_enforced":true,"user_id":"alice"}
```

- [ ] **Step 3: A session created through the gateway path is attributed to alice**

```bash
SID=$(curl -s -X POST -H 'Authorization: Bearer t' -H 'X-Forwarded-User: alice' -H 'Content-Type: application/json' -d '{}' $B/api/sessions | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -s -H 'Authorization: Bearer t' -H 'X-Forwarded-User: alice' "$B/api/sessions" | python3 -c 'import sys,json;print({s["id"]:s["user_id"] for s in json.load(sys.stdin)["sessions"]})'
```
Expected: the map shows `'<SID>': 'alice'`.

- [ ] **Step 4: Startup refuses the unsafe combination**

```bash
kill %1; wait %1 2>/dev/null
OMNIS_HOME=$(mktemp -d) OMNIS_IDENTITY_HEADER=X-Forwarded-User OMNIS_SERVER_ADDR=127.0.0.1:18080 OMNIS_WEB_DIR=$(pwd)/web env -u OMNIS_CONFIG_PATH bin/omnis-server; echo "exit=$?"
```
Expected: the process exits non-zero immediately with the message naming `OMNIS_IDENTITY_HEADER` and `OMNIS_USER_ID`.

- [ ] **Step 5: No-op contract**

```bash
OMNIS_HOME=$(mktemp -d) OMNIS_SERVER_ADDR=127.0.0.1:18080 OMNIS_WEB_DIR=$(pwd)/web env -u OMNIS_CONFIG_PATH bin/omnis-server > /tmp/omnis-smoke2.log 2>&1 &
sleep 2
curl -s -w '\n' http://127.0.0.1:18080/api/whoami
grep -c "serving user" /tmp/omnis-smoke2.log; kill %1
```
Expected: `{"identity_enforced":false,"user_id":"web-user"}` with no token or header needed, and `0` "serving user" log lines (nothing configured ⇒ silent, as before).

- [ ] **Step 6: Record the result**

Append the observed outputs of Steps 1–5 as a short "Verified on <date>" note at the end of `docs/multi-user-containers.md` (under the smoke-test section) and commit:

```bash
git add docs/multi-user-containers.md
git commit -m "$(cat <<'EOF'
docs(multi-user): record the milestone-1 smoke verification

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

## Self-review

**Spec coverage** (spec section → task):
- §4 goal 1 (runs as the user, confined by the container) → Task 6 assets + Task 7 guide (deployment property; not code).
- §4 goal 2 (knows its user, stamps persisted identity) → Tasks 1, 2.
- §4 goal 3 (verifies the asserted identity) → Tasks 3, 4.
- §4 goal 4 (reproducible deployment: template, Dockerfile, docs, guard tests) → Tasks 6, 7.
- §6.1 token unchanged / no UI prompt → no code; documented in Task 7 (gateway must *set* the header).
- §6.2 `OMNIS_USER_ID` env > yaml, fallback, startup log → Tasks 3, 4.
- §6.3 table (401/403/pass), order, startup error, fallback mode → Tasks 3, 4 (code), 7 (fallback documented).
- §6.4 whoami + sidebar → Tasks 3, 4, 5.
- §7.1–7.5 image, program, credentials, state, gateway prerequisites → Tasks 6, 7.
- §8.1 `UserID()`/`SetUserID`, `ConversationFile.UserID`, migration note → Tasks 1, 2, 7.
- §8.2–8.4 middleware, config, main, web, packaging, docs, FEATURES → Tasks 3–7.
- §9 tests → Tasks 1, 3, 4, 6 (unit + router + packaging); manual smoke → Task 8.
- §10–11 out of scope / open points → recorded in the spec; no task.

**Deviation from the spec, recorded:** the supervisord template uses `${VAR}` placeholders rendered by `envsubst` instead of `%(ENV_…)s`, because supervisord does not expand its `user=` option; Task 6 Step 3 updates the spec snippet accordingly.

**Type consistency:** `sessions.UserID()`/`SetUserID(string)` (Task 1) used in Tasks 2–4; `identityMiddleware(header, expected string)`, `resolveIdentity(ServerConfig) (identityConfig, error)`, `handleWhoami(identityHeader string)` (Task 3) used in Task 4; `serverDeps.IdentityHeader`, `ServerConfig.UserID`/`IdentityHeader` (Tasks 3–4); JSON keys `user_id`, `identity_enforced` consistent across Go, JS, docs.
