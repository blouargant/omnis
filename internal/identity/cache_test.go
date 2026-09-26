package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingValidator struct {
	calls atomic.Int32
	delay time.Duration
	fn    func(token string) (Identity, error)
}

func (c *countingValidator) Validate(_ context.Context, token string) (Identity, error) {
	c.calls.Add(1)
	time.Sleep(c.delay)
	return c.fn(token)
}

func jwtWithExp(exp time.Time) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"auth_user":"x"}`, exp.Unix())))
	return "h." + p + ".s"
}

func TestJWTExp(t *testing.T) {
	want := time.Unix(1893456000, 0)
	got, ok := JWTExp(jwtWithExp(want))
	if !ok || !got.Equal(want) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := JWTExp("not-a-jwt"); ok {
		t.Fatal("non-JWT must report !ok")
	}
}

func TestCachingValidatorHitAndTTL(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	inner := &countingValidator{fn: func(tok string) (Identity, error) { return Identity{Login: "alice", Token: tok}, nil }}
	c := NewCachingValidator(inner, 15*time.Minute)
	c.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if id, err := c.Validate(context.Background(), "tok"); err != nil || id.Login != "alice" {
			t.Fatalf("got %+v %v", id, err)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("want 1 inner call, got %d", n)
	}
	now = now.Add(16 * time.Minute)
	_, _ = c.Validate(context.Background(), "tok")
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("after TTL want 2 calls, got %d", n)
	}
}

func TestCachingValidatorTTLCappedByJWTExp(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tok := jwtWithExp(now.Add(time.Minute))
	inner := &countingValidator{fn: func(tok string) (Identity, error) { return Identity{Login: "alice", Token: tok}, nil }}
	c := NewCachingValidator(inner, 15*time.Minute)
	c.Now = func() time.Time { return now }
	_, _ = c.Validate(context.Background(), tok)
	now = now.Add(2 * time.Minute)
	_, _ = c.Validate(context.Background(), tok)
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("JWT exp must cap the cache: want 2 calls, got %d", n)
	}
}

func TestCachingValidatorNegativeCache(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	inner := &countingValidator{fn: func(string) (Identity, error) { return Identity{}, ErrRejected }}
	c := NewCachingValidator(inner, 15*time.Minute)
	c.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if _, err := c.Validate(context.Background(), "bad"); !errors.Is(err, ErrRejected) {
			t.Fatalf("want ErrRejected, got %v", err)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("rejection must be cached: got %d calls", n)
	}
	now = now.Add(31 * time.Second)
	_, _ = c.Validate(context.Background(), "bad")
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("negative cache must expire after 30s: got %d calls", n)
	}
}

func TestCachingValidatorUnavailableNotCached(t *testing.T) {
	inner := &countingValidator{fn: func(string) (Identity, error) { return Identity{}, ErrUnavailable }}
	c := NewCachingValidator(inner, 15*time.Minute)
	_, _ = c.Validate(context.Background(), "t")
	_, _ = c.Validate(context.Background(), "t")
	if n := inner.calls.Load(); n != 2 {
		t.Fatalf("unavailable must not be cached: got %d calls", n)
	}
}

func TestCachingValidatorSingleFlight(t *testing.T) {
	inner := &countingValidator{delay: 100 * time.Millisecond, fn: func(tok string) (Identity, error) { return Identity{Login: "alice", Token: tok}, nil }}
	c := NewCachingValidator(inner, 15*time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Validate(context.Background(), "same") }()
	}
	wg.Wait()
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("concurrent validations must single-flight: got %d calls", n)
	}
}
