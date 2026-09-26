package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/askuser"
)

// A skill loaded by a SUB-AGENT (whose tool context carries agenttool's
// ephemeral session id) must raise its install confirmation under the turn's
// user-facing session — otherwise no pane shows it and, in cookie mode, no user
// owns it, so it can never be answered.
func TestSkillDepGateAttributesAskToRootSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", t.TempDir())
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	dir := filepath.Join(home, "registry", "skills", "depgate-attrib")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: depgate-attrib\ndescription: x\n---\nbody\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "requires.json"),
		[]byte(`{"requires":[{"command":"omnis-no-such-binary-zz9","install":"true"}]}`), 0o644)

	reg := askuser.NewRegistry()
	var mu sync.Mutex
	var seen []string
	reg.SetNotify(func(q askuser.Question) {
		mu.Lock()
		seen = append(seen, q.SessionID)
		mu.Unlock()
		go func() { _ = reg.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{"Skip"}}) }()
	})

	gate := newSkillDepGate(reg)
	tc := hookTestCtx{
		ctx:       events.WithRootSession(context.Background(), "root-sess"),
		sessionID: "ephemeral-sess",
		agentName: "sub",
	}
	if notice := gate(tc, "depgate-attrib"); notice == "" {
		t.Fatal("expected a dependency-unavailable notice after declining")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "root-sess" {
		t.Fatalf("install question registered under %v, want [root-sess]", seen)
	}
}

// realSessionID must also recover the root session planted by injected turns
// (mailbox/background/scheduled/A2A), which set events.WithRootSession but not
// WithSteerSession.
func TestRealSessionIDPrefersRootSession(t *testing.T) {
	tc := hookTestCtx{ctx: events.WithRootSession(context.Background(), "root-sess"), sessionID: "ephemeral-sess"}
	if got := realSessionID(tc); got != "root-sess" {
		t.Fatalf("realSessionID = %q, want root-sess", got)
	}
	steer := hookTestCtx{ctx: WithSteerSession(events.WithRootSession(context.Background(), "root-sess"), "steer-sess"), sessionID: "e"}
	if got := realSessionID(steer); got != "steer-sess" {
		t.Fatalf("realSessionID with steer = %q, want steer-sess", got)
	}
	if got := realSessionID(newHookTestCtx("plain")); got != "plain" {
		t.Fatalf("realSessionID fallback = %q, want plain", got)
	}
}
