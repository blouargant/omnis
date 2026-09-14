package main

import (
	"context"
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
