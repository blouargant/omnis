package sessions

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestTokenUsageCostUSD pins the budget math: the four-rate split, the cache
// price fallback to input, and the legacy fallback to the default rate. This is
// the single source of truth mirrored by the web UI usageCostUSD.
func TestTokenUsageCostUSD(t *testing.T) {
	const defIn, defOut = 3.0, 15.0
	approx := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

	cases := []struct {
		name string
		u    TokenUsage
		want float64
	}{
		{
			// Frozen prices, no cache: fresh=1000@2, output=500@10.
			name: "frozen no cache",
			u:    TokenUsage{Prompt: 1000, Output: 500, InPricePerM: 2, OutPricePerM: 10},
			want: (1000*2 + 500*10) / 1e6,
		},
		{
			// Prompt includes cache tokens: fresh = 1000-600-200 = 200 @2,
			// cacheRead 600 @0.2, cacheCreate 200 @2.5, output 500 @10.
			name: "frozen with cache",
			u: TokenUsage{
				Prompt: 1000, Output: 500, CacheRead: 600, CacheCreate: 200,
				InPricePerM: 2, OutPricePerM: 10,
				CacheReadPricePerM: 0.2, CacheCreatePricePerM: 2.5,
			},
			want: (200*2 + 600*0.2 + 200*2.5 + 500*10) / 1e6,
		},
		{
			// Cache prices unset → fall back to input rate (2) for both.
			name: "cache price fallback to input",
			u: TokenUsage{
				Prompt: 1000, Output: 0, CacheRead: 400, CacheCreate: 100,
				InPricePerM: 2, OutPricePerM: 10,
			},
			want: (500*2 + 400*2 + 100*2) / 1e6, // == 1000*2/1e6 (all input-rate)
		},
		{
			// Legacy turn: no frozen prices → default rate.
			name: "legacy default rate",
			u:    TokenUsage{Prompt: 1000, Output: 500},
			want: (1000*defIn + 500*defOut) / 1e6,
		},
	}
	for _, tc := range cases {
		if got := tc.u.CostUSD(defIn, defOut); !approx(got, tc.want) {
			t.Errorf("%s: CostUSD = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestConversationSquadRoundTrip exercises the on-disk persistence of the
// per-session squad: writing it via SetConversationSquad and reading it
// back through LoadPersistedSessions (the path that rebuilds the session
// list after a server restart).
func TestConversationSquadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	logs := filepath.Join(tmp, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "sess-test"

	// Step 1: record the squad and a couple of turns.
	if err := SetConversationSquad(sid, "research"); err != nil {
		t.Fatalf("SetConversationSquad: %v", err)
	}
	if err := AppendConversationTurn(sid, "hi", "hello"); err != nil {
		t.Fatalf("AppendConversationTurn: %v", err)
	}

	// Step 2: read it back through LoadPersistedSessions (which is what
	// the server uses on startup to rebuild the in-memory registry).
	got := LoadPersistedSessions()
	var meta *SessionMeta
	for _, m := range got {
		if m.ID == sid {
			meta = m
			break
		}
	}
	if meta == nil {
		t.Fatalf("session %q missing from LoadPersistedSessions(): %+v", sid, got)
	}
	if meta.Squad != "research" {
		t.Fatalf("Squad = %q, want %q", meta.Squad, "research")
	}

	// Step 3: legacy conversation files (no squad field) load with an
	// empty Squad, which the server interprets as "default" at runtime.
	legacy := filepath.Join(logs, "conversation_legacy.json")
	body := `{"turns":[{"user_text":"x","assistant_text":"y","at":"2024-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	got = LoadPersistedSessions()
	var legacyMeta *SessionMeta
	for _, m := range got {
		if m.ID == "legacy" {
			legacyMeta = m
			break
		}
	}
	if legacyMeta == nil {
		t.Fatal("legacy session missing")
	}
	if legacyMeta.Squad != "" {
		t.Fatalf("legacy Squad = %q, want empty (default at runtime)", legacyMeta.Squad)
	}

	// Step 4: SessionMeta marshals squad as JSON omitempty.
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"squad":"research"`)) {
		t.Fatalf("marshalled meta missing squad: %s", b)
	}
}

// TestArchivedFlagRoundTrip verifies the archived flag survives a "server
// restart": SetConversationArchived persists it and LoadPersistedSessions
// reads it back into the rebuilt registry, without disturbing the
// conversation turns. Clearing the flag (unarchive) round-trips too.
func TestArchivedFlagRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "archive-test"
	if err := AppendConversationTurn(sid, "hi", "hello"); err != nil {
		t.Fatalf("AppendConversationTurn: %v", err)
	}

	reloaded := func() *SessionMeta {
		for _, m := range LoadPersistedSessions() {
			if m.ID == sid {
				return m
			}
		}
		return nil
	}

	if err := SetConversationArchived(sid, true); err != nil {
		t.Fatalf("SetConversationArchived: %v", err)
	}
	meta := reloaded()
	if meta == nil {
		t.Fatalf("session %q missing after archive", sid)
	}
	if !meta.Archived {
		t.Fatalf("Archived = false after archive, want true")
	}
	if meta.Turns != 1 {
		t.Fatalf("Turns = %d after archive, want 1 (turns must be preserved)", meta.Turns)
	}

	// Unarchive round-trips back to active.
	if err := SetConversationArchived(sid, false); err != nil {
		t.Fatalf("SetConversationArchived(false): %v", err)
	}
	if meta := reloaded(); meta == nil || meta.Archived {
		t.Fatalf("Archived still set after unarchive: %+v", meta)
	}
}

// TestConcurrentMutatorsDoNotLoseTurns exercises the per-session lock: many
// goroutines append turns while others flip the harvested/archived/title flags
// on the same session. Every append must survive — the old unsynchronised
// load-modify-save lost updates (and could read a half-written file and reset
// the whole history). Run with -race to also catch the data race directly.
func TestConcurrentMutatorsDoNotLoseTurns(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "concurrent-test"
	const appends = 50

	var wg sync.WaitGroup
	for i := 0; i < appends; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := AppendConversationTurn(sid, "u", "a"); err != nil {
				t.Errorf("AppendConversationTurn: %v", err)
			}
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = SetConversationHarvested(sid, true)
			_ = SetConversationTitle(sid, "t")
			_ = SetConversationArchived(sid, false)
		}()
	}
	wg.Wait()

	turns, err := LoadConversationTurns(sid)
	if err != nil {
		t.Fatalf("LoadConversationTurns: %v", err)
	}
	if len(turns) != appends {
		t.Fatalf("got %d turns after %d concurrent appends, want %d (turns were lost)", len(turns), appends, appends)
	}
}

// TestCorruptConversationFileQuarantined verifies that a syntactically corrupt
// conversation file does NOT cause the next write to silently discard the
// history: the original bytes are quarantined to a *.corrupt-* sidecar, and the
// session keeps working. This is the safety net behind the atomic-write fix.
func TestCorruptConversationFileQuarantined(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	logs := filepath.Join(tmp, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "corrupt-test"
	// Hand-write a truncated/corrupt conversation file (as an interrupted
	// non-atomic write from an old build would have left behind).
	corrupt := []byte(`{"turns":[{"user_text":"a","assistant_text":"b","at":"2024`)
	if err := os.WriteFile(ConversationPath(sid), corrupt, 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	// The next mutation must not error out and must not destroy the bytes.
	if err := AppendConversationTurn(sid, "hi", "hello"); err != nil {
		t.Fatalf("AppendConversationTurn over corrupt file: %v", err)
	}

	// The session recovered: the new file holds the appended turn.
	turns, err := LoadConversationTurns(sid)
	if err != nil {
		t.Fatalf("LoadConversationTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].UserText != "hi" {
		t.Fatalf("recovered turns = %+v, want one {hi}", turns)
	}

	// The corrupt bytes were preserved, not deleted.
	matches, _ := filepath.Glob(filepath.Join(logs, "conversation_"+sid+".json.corrupt-*"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one quarantined file, found %v", matches)
	}
	saved, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read quarantine: %v", err)
	}
	if !bytes.Equal(saved, corrupt) {
		t.Fatalf("quarantined bytes = %q, want original corrupt bytes preserved", saved)
	}
}

// TestRegistrySetArchived verifies the in-memory flag toggles and the missing
// session case returns false.
func TestRegistrySetArchived(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	reg := NewEmptyRegistry()
	m := reg.New("")
	if !reg.SetArchived(m.ID, true) {
		t.Fatalf("SetArchived(existing) = false, want true")
	}
	if got, _ := reg.Get(m.ID); got == nil || !got.Archived {
		t.Fatalf("in-memory Archived not set: %+v", got)
	}
	if reg.SetArchived("does-not-exist", true) {
		t.Fatalf("SetArchived(missing) = true, want false")
	}
}
