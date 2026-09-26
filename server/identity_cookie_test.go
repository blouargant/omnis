package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/identity"
	"github.com/blouargant/omnis/internal/sessions"
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
	r.GET("/x", cookieIdentityMiddleware(a), func(c *gin.Context) {
		if v, ok := c.Get("identity"); !ok || v.(identity.Identity).Login != requestLogin(c) {
			c.String(500, "identity missing from gin context")
			return
		}
		c.String(200, requestLogin(c))
	})

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

// TestCookieModeGuardsEveryAPIRoute drives the real router in cookie mode: the
// cookie check replaces the bearer token (a valid bearer alone is NOT enough)
// and also guards the terminal WebSocket; /api/health stays open.
func TestCookieModeGuardsEveryAPIRoute(t *testing.T) {
	engine := newEngine(serverDeps{
		Token:    "s3cret",
		Cookie:   testCookieAuth(),
		Registry: sessions.NewEmptyRegistry(),
		rootCtx:  context.Background(),
	})
	call := func(path, bearer, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "plat_token", Value: cookie})
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	if w := call("/api/health", "", ""); w.Code != http.StatusOK {
		t.Fatalf("/api/health must stay open: %d", w.Code)
	}
	for _, p := range []string{"/api/whoami", "/api/sessions", "/api/terminal/ws", "/api/collections", "/api/schedules"} {
		if w := call(p, "s3cret", ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s with bearer but no cookie: got %d, want 401", p, w.Code)
		}
		if w := call(p, "", "nope"); w.Code != http.StatusUnauthorized {
			t.Errorf("%s with rejected cookie: got %d, want 401", p, w.Code)
		}
	}
	w := call("/api/whoami", "", "tb")
	if w.Code != http.StatusOK {
		t.Fatalf("whoami with valid cookie: %d %s", w.Code, w.Body)
	}
	var body struct {
		UserID   string `json:"user_id"`
		Mode     string `json:"identity_mode"`
		Enforced bool   `json:"identity_enforced"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "bob" || body.Mode != "cookie" || !body.Enforced {
		t.Fatalf("whoami body %+v", body)
	}
}

func TestCookieMiddlewareRejectsEmptyLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := testCookieAuth()
	a.validator = fakeValidator{"empty": ""}
	r := gin.New()
	r.GET("/x", cookieIdentityMiddleware(a), func(c *gin.Context) { c.String(200, "ok") })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: "empty"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty login must be rejected: %d", w.Code)
	}
}

// TestCookieModeRefusesCrossSiteWrites: the ambient cookie must not let a
// hostile page drive state-changing routes (CSRF). Real router via newEngine.
func TestCookieModeRefusesCrossSiteWrites(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	engine := newEngine(serverDeps{
		Cookie:   testCookieAuth(),
		Registry: sessions.NewEmptyRegistry(),
		rootCtx:  context.Background(),
	})
	call := func(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Host = "omnis.example"
		req.AddCookie(&http.Cookie{Name: "plat_token", Value: "ta"})
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	refused := func(w *httptest.ResponseRecorder) bool {
		return w.Code == http.StatusForbidden && strings.Contains(w.Body.String(), "cross-site request refused")
	}
	for name, hdr := range map[string]map[string]string{
		"foreign Origin":             {"Origin": "https://evil.example"},
		"Sec-Fetch-Site cross-site":  {"Sec-Fetch-Site": "cross-site"},
		"foreign Referer, no Origin": {"Referer": "https://evil.example/page"},
	} {
		if w := call(http.MethodPost, "/api/collections", hdr); !refused(w) {
			t.Errorf("POST %s: got %d %s, want 403 cross-site", name, w.Code, w.Body)
		}
	}
	for name, hdr := range map[string]map[string]string{
		"same-host Origin":           {"Origin": "https://omnis.example"},
		"Sec-Fetch-Site same-origin": {"Sec-Fetch-Site": "same-origin", "Referer": "https://omnis.example/"},
		"no browser headers":         {},
	} {
		if w := call(http.MethodPost, "/api/collections", hdr); refused(w) || w.Code == http.StatusUnauthorized {
			t.Errorf("POST %s: got %d %s, want it past the guard", name, w.Code, w.Body)
		}
	}
	if w := call(http.MethodGet, "/api/collections", map[string]string{"Origin": "https://evil.example"}); refused(w) {
		t.Errorf("GET with foreign Origin refused by the guard: %d", w.Code)
	}
	// POST /api/terminal/token is not mounted in cookie mode.
	if w := call(http.MethodPost, "/api/terminal/token", nil); w.Code != http.StatusNotFound {
		t.Errorf("terminal/token in cookie mode: got %d, want 404", w.Code)
	}
}

// The middleware normalises the validated login once (trimmed, lower-case) so
// every owner comparison, token-store key and per-user directory agrees with
// LoginSegment's case folding: " Alice " and "alice" are one user.
func TestCookieMiddlewareNormalisesLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := testCookieAuth()
	a.validator = fakeValidator{"tA": " Alice ", "ta": "alice"}
	r := gin.New()
	r.GET("/x", cookieIdentityMiddleware(a), func(c *gin.Context) { c.String(200, requestLogin(c)) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "plat_token", Value: "tA"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "alice" {
		t.Fatalf("login not normalised: %d %q", w.Code, w.Body.String())
	}
	if tok, ok := a.tokens.Get("alice"); !ok || tok != "tA" {
		t.Fatalf("token must be keyed by the normalised login, got %q %v", tok, ok)
	}
}
