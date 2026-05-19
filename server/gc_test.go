package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/internal/paths"
)

// gcTestEnv points $YOKE_HOME at a fresh temp directory so logsDir(),
// uploadsBaseDir() and paths.MailboxesDir() all return paths under it
// for the duration of the test.
func gcTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YOKE_HOME", dir)
	return dir
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunGC_RemovesOrphansKeepsActiveAndGlobals(t *testing.T) {
	gcTestEnv(t)

	reg := &registry{items: map[string]*SessionMeta{
		"alive-cat": {ID: "alive-cat", UserID: defaultUserID, CreatedAt: time.Now()},
	}}
	aliveSuffix := agent.SessionSuffix(defaultUserID, "alive-cat")
	deadSuffix := agent.SessionSuffix(defaultUserID, "dead-fox")

	// Active session files — must survive.
	mustWriteFile(t, filepath.Join(logsDir(), "conversation_alive-cat.json"), `{"turns":[]}`)
	mustWriteFile(t, filepath.Join(logsDir(), "agent_tasks_"+aliveSuffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_todo_"+aliveSuffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_memory_"+aliveSuffix+".md"), "memory")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_statelog_"+aliveSuffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(uploadsBaseDir(), "alive-cat", "doc.txt"), "hi")
	mustWriteFile(t, filepath.Join(paths.MailboxesDir(), aliveSuffix+":peer.jsonl"), "")

	// Orphan session files — must go.
	mustWriteFile(t, filepath.Join(logsDir(), "conversation_dead-fox.json"), `{"turns":[]}`)
	mustWriteFile(t, filepath.Join(logsDir(), "agent_tasks_"+deadSuffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_todo_"+deadSuffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_memory_"+deadSuffix+".md"), "memory")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_statelog_"+deadSuffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(uploadsBaseDir(), "dead-fox", "doc.txt"), "bye")
	mustWriteFile(t, filepath.Join(paths.MailboxesDir(), deadSuffix+":peer.jsonl"), "")

	// Global event log — must survive (not session-scoped).
	mustWriteFile(t, filepath.Join(logsDir(), "agent_events_20260511_120000.log"), "event")

	stats := runGC(reg, gcDeps{})

	if stats.Conversations != 1 {
		t.Errorf("conversations: got %d, want 1", stats.Conversations)
	}
	if stats.AgentFiles != 4 {
		t.Errorf("agent files: got %d, want 4", stats.AgentFiles)
	}
	if stats.Uploads != 1 {
		t.Errorf("uploads: got %d, want 1", stats.Uploads)
	}
	if stats.Mailboxes != 1 {
		t.Errorf("mailboxes: got %d, want 1", stats.Mailboxes)
	}
	if len(stats.Errors) != 0 {
		t.Errorf("unexpected errors: %v", stats.Errors)
	}

	mustExist := []string{
		filepath.Join(logsDir(), "conversation_alive-cat.json"),
		filepath.Join(logsDir(), "agent_tasks_"+aliveSuffix+".json"),
		filepath.Join(logsDir(), "agent_todo_"+aliveSuffix+".json"),
		filepath.Join(logsDir(), "agent_memory_"+aliveSuffix+".md"),
		filepath.Join(logsDir(), "agent_statelog_"+aliveSuffix+".json"),
		filepath.Join(uploadsBaseDir(), "alive-cat", "doc.txt"),
		filepath.Join(paths.MailboxesDir(), aliveSuffix+":peer.jsonl"),
		filepath.Join(logsDir(), "agent_events_20260511_120000.log"),
	}
	for _, p := range mustExist {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to survive GC: %v", p, err)
		}
	}

	mustNotExist := []string{
		filepath.Join(logsDir(), "conversation_dead-fox.json"),
		filepath.Join(logsDir(), "agent_tasks_"+deadSuffix+".json"),
		filepath.Join(logsDir(), "agent_todo_"+deadSuffix+".json"),
		filepath.Join(logsDir(), "agent_memory_"+deadSuffix+".md"),
		filepath.Join(logsDir(), "agent_statelog_"+deadSuffix+".json"),
		filepath.Join(uploadsBaseDir(), "dead-fox"),
		filepath.Join(paths.MailboxesDir(), deadSuffix+":peer.jsonl"),
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed; stat err=%v", p, err)
		}
	}
}

func TestRunGC_RenamedSessionFilesSurvive(t *testing.T) {
	// A renamed session only changes the Title field; ID and UserID are
	// immutable, and every on-disk filename is keyed on ID/UserID. The GC
	// must not delete any file belonging to a session just because its
	// display title was changed.
	gcTestEnv(t)

	reg := &registry{items: map[string]*SessionMeta{
		"happy-newt": {
			ID:     "happy-newt",
			UserID: defaultUserID,
			Title:  "Q3 incident triage", // user-assigned title after rename
		},
	}}
	suffix := agent.SessionSuffix(defaultUserID, "happy-newt")

	mustWriteFile(t, filepath.Join(logsDir(), "conversation_happy-newt.json"), `{"title":"Q3 incident triage","turns":[]}`)
	mustWriteFile(t, filepath.Join(logsDir(), "agent_tasks_"+suffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(logsDir(), "agent_statelog_"+suffix+".json"), "{}")
	mustWriteFile(t, filepath.Join(uploadsBaseDir(), "happy-newt", "screenshot.png"), "png")
	mustWriteFile(t, filepath.Join(paths.MailboxesDir(), suffix+":leader.jsonl"), "")

	stats := runGC(reg, gcDeps{})

	if stats.total() != 0 {
		t.Fatalf("renamed session should not lose any files; got %+v", stats)
	}
	mustExist := []string{
		filepath.Join(logsDir(), "conversation_happy-newt.json"),
		filepath.Join(logsDir(), "agent_tasks_"+suffix+".json"),
		filepath.Join(logsDir(), "agent_statelog_"+suffix+".json"),
		filepath.Join(uploadsBaseDir(), "happy-newt", "screenshot.png"),
		filepath.Join(paths.MailboxesDir(), suffix+":leader.jsonl"),
	}
	for _, p := range mustExist {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected renamed-session file %s to survive: %v", p, err)
		}
	}
}

func TestRunGC_MissingDirsAreNotErrors(t *testing.T) {
	gcTestEnv(t)
	reg := &registry{items: map[string]*SessionMeta{}}
	stats := runGC(reg, gcDeps{})
	if len(stats.Errors) != 0 {
		t.Errorf("expected no errors when logs/ is absent, got %v", stats.Errors)
	}
	if stats.total() != 0 {
		t.Errorf("expected zero deletions, got %+v", stats)
	}
}

func TestRunGC_PrunesStaleMailboxRegistryEntries(t *testing.T) {
	gcTestEnv(t)

	reg := &registry{items: map[string]*SessionMeta{
		"alive-cat": {ID: "alive-cat", UserID: defaultUserID, Title: "Live triage"},
	}}
	aliveSuffix := agent.SessionSuffix(defaultUserID, "alive-cat")
	deadSuffix := agent.SessionSuffix(defaultUserID, "dead-fox")

	// Simulated cross-session registry state: one live, one orphan, one
	// renamed (live ID, but display name differs from petname), and one
	// unparseable entry that the GC must leave alone.
	store := map[string]string{
		"Live triage":    aliveSuffix + ":leader",
		"old-ghost":      deadSuffix + ":leader",
		"alive-cat":      aliveSuffix + ":leader", // pre-rename petname entry, still valid
		"weird-no-colon": "no-colon-here",
	}
	unregistered := map[string]bool{}

	deps := gcDeps{
		listRegistry: func() map[string]string {
			out := make(map[string]string, len(store))
			for k, v := range store {
				out[k] = v
			}
			return out
		},
		unregister: func(name string) error {
			delete(store, name)
			unregistered[name] = true
			return nil
		},
	}

	stats := runGC(reg, deps)

	if stats.RegistryEntries != 1 {
		t.Errorf("registry entries removed: got %d, want 1", stats.RegistryEntries)
	}
	if !unregistered["old-ghost"] {
		t.Errorf("expected 'old-ghost' to be unregistered, got: %v", unregistered)
	}
	if unregistered["Live triage"] || unregistered["alive-cat"] {
		t.Errorf("live entries should not be unregistered, got: %v", unregistered)
	}
	if unregistered["weird-no-colon"] {
		t.Errorf("unparseable address must be left alone, got unregister call")
	}
	if _, still := store["old-ghost"]; still {
		t.Errorf("orphan entry should be gone from store")
	}
}

func TestParseGCInterval(t *testing.T) {
	cases := []struct {
		raw         string
		wantEnabled bool
		wantDur     time.Duration
	}{
		{"", true, defaultGCInterval},
		{"30m", true, 30 * time.Minute},
		{"2h", true, 2 * time.Hour},
		{"0", false, 0},
		{"off", false, 0},
		{"DISABLED", false, 0},
		{"bogus", true, defaultGCInterval},
		{"-5m", true, defaultGCInterval},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			d, enabled := parseGCInterval(tc.raw)
			if enabled != tc.wantEnabled {
				t.Fatalf("enabled: got %v, want %v", enabled, tc.wantEnabled)
			}
			if d != tc.wantDur {
				t.Fatalf("duration: got %s, want %s", d, tc.wantDur)
			}
		})
	}
}
