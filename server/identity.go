package main

import (
	"errors"
	"net/http"
	"os"
	"os/user"
	"strings"
	"sync"

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
		// A request carrying the identity header twice is refused outright: with
		// only the first value checked, a smuggled second value would ride along
		// unexamined behind a matching first one.
		if len(c.Request.Header.Values(header)) > 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "identity mismatch"})
			return
		}
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
func handleWhoami(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Cookie != nil {
			c.JSON(http.StatusOK, gin.H{"user_id": requestLogin(c), "identity_mode": "cookie", "identity_enforced": true})
			return
		}
		out := gin.H{
			"user_id":           sessions.UserID(),
			"identity_enforced": strings.TrimSpace(d.IdentityHeader) != "",
		}
		// A single-user install serves the shared default id, which names no
		// one: report the person it belongs to so the account button can show
		// their name rather than a generic label.
		if sessions.UserID() == sessions.DefaultUserID {
			if name := localDisplayName(); name != "" {
				out["display_name"] = name
			}
		}
		c.JSON(http.StatusOK, out)
	}
}

// localDisplayName is the name of the OS account running this server: its
// full name (the first GECOS field), else its login (without a Windows
// DOMAIN\ prefix), else $USER / $USERNAME. Looked up once.
var localDisplayName = sync.OnceValue(func() string {
	if u, err := user.Current(); err == nil {
		if name, _, _ := strings.Cut(u.Name, ","); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		login := u.Username
		if i := strings.LastIndexByte(login, '\\'); i >= 0 {
			login = login[i+1:]
		}
		if login = strings.TrimSpace(login); login != "" {
			return login
		}
	}
	for _, k := range []string{"USER", "USERNAME"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
})
