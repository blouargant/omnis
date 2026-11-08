package tools

import (
	"context"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/events"
)

func TestWebCacheServesRepeatsWithinScope(t *testing.T) {
	c := newWebCache(time.Minute, 10)
	calls := 0
	fetch := func() string { calls++; return "page body" }

	if got := c.get("s1", "fetch\x00https://a", fetch); got != "page body" {
		t.Fatalf("first get = %q", got)
	}
	c.get("s1", "fetch\x00https://a", fetch)
	if calls != 1 {
		t.Errorf("a repeat in the same session fetched again: %d calls", calls)
	}
	// Another session never sees the first one's results.
	c.get("s2", "fetch\x00https://a", fetch)
	if calls != 2 {
		t.Errorf("another session was served a cached result: %d calls", calls)
	}
}

func TestWebCacheDoesNotKeepFailures(t *testing.T) {
	c := newWebCache(time.Minute, 10)
	for _, bad := range []string{"error fetching URL: timeout", "(no results)", "  "} {
		calls := 0
		fetch := func() string { calls++; return bad }
		c.get("s", "k"+bad, fetch)
		c.get("s", "k"+bad, fetch)
		if calls != 2 {
			t.Errorf("%q was cached; a transient failure must be retried", bad)
		}
	}
}

func TestWebCacheExpiresAndEvicts(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newWebCache(time.Minute, 2)
	c.now = func() time.Time { return now }
	calls := 0
	fetch := func() string { calls++; return "v" }

	c.get("s", "a", fetch)
	now = now.Add(2 * time.Minute)
	c.get("s", "a", fetch)
	if calls != 2 {
		t.Errorf("an expired entry was served: %d calls", calls)
	}
	c.get("s", "b", fetch)
	now = now.Add(time.Second)
	c.get("s", "c", fetch)
	if len(c.m) > 2 {
		t.Errorf("cache holds %d entries, limit 2", len(c.m))
	}
}

func TestWebCacheScopePrefersRootSession(t *testing.T) {
	ctx := events.WithRootSession(context.Background(), "root")
	if got := webCacheScope(ctx, "ephemeral-subagent"); got != "root" {
		t.Errorf("scope = %q, want the root session", got)
	}
	if got := webCacheScope(context.Background(), "own"); got != "own" {
		t.Errorf("scope without a root = %q, want own", got)
	}
}

func TestPageCacheBudget(t *testing.T) {
	c := newPageCache(webCacheTTL, 64<<20)
	clock := time.Unix(1000, 0)
	c.now = func() time.Time { return clock }
	big := func() *fetchedPage { return &fetchedPage{doc: buildTextDoc("x"), size: 40 << 20} }
	loads := 0
	load := func() (*fetchedPage, string) { loads++; return big(), "" }
	c.get("s", "a", load)
	clock = clock.Add(time.Second)
	c.get("s", "b", load)
	c.get("s", "b", load)
	if loads != 2 {
		t.Errorf("loads = %d, want 2 (b cached)", loads)
	}
	c.get("s", "a", load)
	if loads != 3 {
		t.Errorf("a should have been evicted: loads = %d", loads)
	}
	if _, e := c.get("s", "err", func() (*fetchedPage, string) { return nil, "error x" }); e != "error x" {
		t.Errorf("error = %q", e)
	}
	c.get("s", "err", func() (*fetchedPage, string) { loads++; return big(), "" })
	if loads != 4 {
		t.Error("an error was cached")
	}
	c.get("t", "b", load)
	if loads != 5 {
		t.Error("scopes are shared")
	}
}
