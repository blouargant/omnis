package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	// A login sanitisation leaves untouched keeps its (lower-cased) spelling;
	// one it had to alter gets a hash suffix so two logins can never collapse
	// onto one directory (a+b@x vs a_b@x).
	cases := map[string]string{
		"Alice@Example.com": "alice@example.com",
		"a/b":               "a_b-" + hash8("a/b"),
		"..":                "_-" + hash8(".."),
		".":                 "_-" + hash8("."),
		"":                  "_",
		"x y":               "x_y-" + hash8("x y"),
		"ok.name-1_2":       "ok.name-1_2",
		"a+b@x":             "a_b@x-" + hash8("a+b@x"),
		"a_b@x":             "a_b@x",
	}
	for in, want := range cases {
		if got := LoginSegment(in); got != want {
			t.Errorf("LoginSegment(%q)=%q want %q", in, got, want)
		}
	}
}

func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func TestLoginSegmentNoCollision(t *testing.T) {
	if LoginSegment("a+b@x") == LoginSegment("a_b@x") {
		t.Fatal("a+b@x and a_b@x must not share a directory")
	}
}

// '+' is a literal character in a cookie value (RFC 6265), not an encoded
// space: a token containing it must reach the validator unchanged.
func TestTokenFromRequestKeepsPlus(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "c", Value: "ab+cd%2Fef"})
	if got := TokenFromRequest(r, []string{"c"}); got != "ab+cd/ef" {
		t.Fatalf("got %q, want ab+cd/ef", got)
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
