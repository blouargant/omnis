package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func newAuthRouter(token string) *gin.Engine {
	r := gin.New()
	r.GET("/protected", authMiddleware(token), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestAuthMiddlewareNoToken(t *testing.T) {
	// When no token is configured every request must pass through.
	r := newAuthRouter("")
	for _, header := range []string{"", "Bearer whatever", "Basic abc"} {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("header=%q: got %d want 200", header, w.Code)
		}
	}
}

func TestAuthMiddleware(t *testing.T) {
	const token = "s3cret"

	cases := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic abc", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong", http.StatusUnauthorized},
		{"correct token", "Bearer " + token, http.StatusOK},
		{"trailing whitespace", "Bearer " + token + " ", http.StatusUnauthorized},
		{"case-sensitive scheme", "bearer " + token, http.StatusUnauthorized},
	}

	r := newAuthRouter(token)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status: got %d want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestConstantTimeEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"ab", "abc", false},
	}
	for _, tc := range cases {
		if got := constantTimeEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("constantTimeEqual(%q,%q)=%v want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
