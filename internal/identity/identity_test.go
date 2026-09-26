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
