package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/identity"
	"github.com/blouargant/omnis/internal/paths"
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
		if strings.TrimSpace(id.Login) == "" {
			// A validator that "succeeds" without a login must never let the
			// request through: ownerFor would fall back to the process owner.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated", "login_url": a.cfg.LoginURL})
			return
		}
		exp, _ := identity.JWTExp(tok)
		a.tokens.Put(id.Login, tok, exp)
		c.Set("identity", id)
		c.Request = c.Request.WithContext(identity.WithIdentity(c.Request.Context(), id))
		c.Next()
	}
}

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

// ownerOfSession is the UserID that owns session id, or "" when the session is
// unknown. Used to look up the shell-env token for a session-scoped route
// (e.g. the "!" shell-escape) whose caller identity is the session's owner,
// not necessarily the requester (single-user mode has no requester login at
// all, so the session's stamped owner is the only source of truth).
func ownerOfSession(d serverDeps, id string) string {
	if m, ok := d.Registry.Snapshot(id); ok {
		return m.UserID
	}
	return ""
}

// userRoot is the per-user state root: the shared write root
// (paths.ConfigWriteDir()) for login "" (single-user / non-cookie modes,
// unchanged behaviour), else a sanitised per-login subdirectory under it so
// concurrent users on one shared server never share preferences, a working
// directory, or any other per-user state.
func userRoot(login string) string {
	if login == "" {
		return paths.ConfigWriteDir()
	}
	return filepath.Join(paths.ConfigWriteDir(), "users", identity.LoginSegment(login))
}

// userWorkDir is the per-user default working directory — the cwd new
// sessions start in and the session-less Files-panel browse cwd resolves to
// in cookie mode. "" outside cookie mode (login ""), so every caller's
// fallback-to-shared-behaviour stays intact. Created on demand.
func userWorkDir(login string) string {
	if login == "" {
		return ""
	}
	dir := filepath.Join(userRoot(login), "work")
	_ = os.MkdirAll(dir, 0o755)
	return dir
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

// sameOriginGuard is the CSRF defence of cookie mode. The platform cookie is
// ambient, so a hostile page could send a preflight-free "simple" POST
// (text/plain body — ShouldBindJSON ignores Content-Type) to a state-changing
// route such as the shell escape. Unsafe methods are refused when the browser
// says the request is cross-site (Sec-Fetch-Site), or when Origin — or, absent
// Origin, Referer — names another host. Requests carrying none of these
// headers (curl, scripts, tests) are not browser-initiated and pass.
func sameOriginGuard() gin.HandlerFunc {
	refuse := func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-site request refused"})
	}
	foreignHost := func(raw, host string) bool {
		u, err := url.Parse(raw)
		return err != nil || !strings.EqualFold(u.Host, host)
	}
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		h := c.Request.Header
		if sfs := strings.ToLower(strings.TrimSpace(h.Get("Sec-Fetch-Site"))); sfs != "" && sfs != "same-origin" && sfs != "none" {
			refuse(c)
			return
		}
		if origin := h.Get("Origin"); origin != "" {
			if foreignHost(origin, c.Request.Host) {
				refuse(c)
				return
			}
		} else if ref := h.Get("Referer"); ref != "" && foreignHost(ref, c.Request.Host) {
			refuse(c)
			return
		}
		c.Next()
	}
}

// visibleTo reports whether an item owned by owner is visible to login.
// login "" (not cookie mode) sees everything — the no-op contract.
func visibleTo(login, owner string) bool { return login == "" || owner == login }

// ownerGuard refuses, with 404, any route addressing a session or schedule
// the caller does not own — and a `session` query parameter naming one. It
// keys on the MATCHED route pattern (c.FullPath()), so every route under
// /sessions/:id and /schedules/:id is covered, including ones added later.
// 404 (not 403) so another user's ids cannot even be confirmed to exist.
// Outside cookie mode (requestLogin == "") it is a pass-through.
func ownerGuard(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		login := requestLogin(c)
		if login == "" {
			c.Next()
			return
		}
		notFound := func() { c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"}) }
		fp := c.FullPath()
		if strings.Contains(fp, "/sessions/:id") && d.Registry != nil {
			if m, ok := d.Registry.Snapshot(c.Param("id")); ok && m.UserID != login {
				notFound()
				return
			}
		}
		if sid := c.Query("session"); sid != "" && d.Registry != nil {
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
