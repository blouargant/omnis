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
