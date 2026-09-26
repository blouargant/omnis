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
	"crypto/sha256"
	"encoding/hex"
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

// NormalizeLogin is the canonical form of a login used for every owner
// comparison and key: trimmed and lower-cased (the platform treats logins
// case-insensitively, and LoginSegment folds case for directory names).
func NormalizeLogin(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// LoginSegment turns a login into a safe single path segment: lowercased,
// every rune outside [a-z0-9._@-] replaced by '_', and "", "." and ".."
// mapped to "_" so it can never escape its parent directory. When
// sanitisation had to alter the (lower-cased) login, "-" + the first 8 hex of
// its SHA-256 is appended, so two logins differing only in replaced runes
// (a+b@x vs a_b@x) never share a directory.
func LoginSegment(login string) string {
	s := NormalizeLogin(login)
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
		out = "_"
	}
	if s != "" && out != s {
		sum := sha256.Sum256([]byte(s))
		out += "-" + hex.EncodeToString(sum[:])[:8]
	}
	return out
}
