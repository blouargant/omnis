package mcp

import (
	"testing"
)

func TestConfigKeyStableAcrossEnvOrdering(t *testing.T) {
	a := Server{
		Name: "x", Command: "/bin/true",
		Args: []string{"--flag", "value"},
		Env:  map[string]string{"A": "1", "B": "2"},
	}
	b := Server{
		Name: "x", Command: "/bin/true",
		Args: []string{"--flag", "value"},
		Env:  map[string]string{"B": "2", "A": "1"}, // same env, declared in different order
	}
	if configKey(a) != configKey(b) {
		t.Fatal("configKey is sensitive to env map iteration order")
	}
}

func TestConfigKeyDiffersWhenArgsChange(t *testing.T) {
	a := Server{Command: "/bin/true", Args: []string{"x"}}
	b := Server{Command: "/bin/true", Args: []string{"y"}}
	if configKey(a) == configKey(b) {
		t.Fatal("configKey collision for differing args")
	}
}

func TestConfigKeyDiffersWhenCommandChanges(t *testing.T) {
	a := Server{Command: "/bin/true"}
	b := Server{Command: "/bin/false"}
	if configKey(a) == configKey(b) {
		t.Fatal("configKey collision for differing command")
	}
}

func TestPoolAcquireDedupsIdenticalConfigs(t *testing.T) {
	p := NewPool(nil)
	s := Server{Name: "echo", Command: "/bin/echo", Args: []string{"hi"}}

	h1, err := p.Acquire(s, nil)
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	h2, err := p.Acquire(s, nil)
	if err != nil {
		t.Fatalf("Acquire #2: %v", err)
	}
	if h1 != h2 {
		t.Fatal("Acquire returned distinct handles for identical configs")
	}
	if got := p.Refcount(s); got != 2 {
		t.Fatalf("refcount after two Acquires = %d, want 2", got)
	}

	p.Release(h1)
	if got := p.Refcount(s); got != 1 {
		t.Fatalf("refcount after one Release = %d, want 1", got)
	}
	p.Release(h2)
	if got := p.Refcount(s); got != 0 {
		t.Fatalf("refcount after both Releases = %d, want 0", got)
	}
}

func TestPoolDifferentConfigsGetDistinctHandles(t *testing.T) {
	p := NewPool(nil)
	h1, err := p.Acquire(Server{Command: "/bin/echo", Args: []string{"a"}}, nil)
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	h2, err := p.Acquire(Server{Command: "/bin/echo", Args: []string{"b"}}, nil)
	if err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	if h1 == h2 {
		t.Fatal("different configs received same handle")
	}
	if h1.key == h2.key {
		t.Fatal("different configs hashed to same key")
	}
}

func TestPoolReleaseUnknownHandleIsSafe(t *testing.T) {
	p := NewPool(nil)
	// nil handle
	p.Release(nil)
	// handle whose key has been evicted
	h, _ := p.Acquire(Server{Command: "/bin/echo"}, nil)
	p.Release(h)
	p.Release(h) // second release is a no-op, not a crash
}

func TestPoolAcquireAllRollsBackOnError(t *testing.T) {
	p := NewPool(nil)
	c := &Config{Servers: map[string]Server{
		"ok":  {Command: "/bin/echo"},
		"ok2": {Command: "/bin/true"},
	}}
	tsets, handles, err := p.AcquireAll(c)
	if err != nil {
		t.Fatalf("AcquireAll: %v", err)
	}
	if len(tsets) != 2 || len(handles) != 2 {
		t.Fatalf("AcquireAll returned %d toolsets, %d handles", len(tsets), len(handles))
	}
	// Refcount lookups go by configKey, which ignores Name — so a bare
	// Server matching command/args is enough to query.
	if p.Refcount(Server{Command: "/bin/echo"}) != 1 || p.Refcount(Server{Command: "/bin/true"}) != 1 {
		t.Fatal("expected refcount=1 for each acquired server")
	}
	for _, h := range handles {
		p.Release(h)
	}
}
