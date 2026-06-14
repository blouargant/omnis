package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/hooks"
)

// buildHooksPlugin returns nothing for the router squad (hooks fire on the
// answering squad), and a real plugin otherwise.
func TestBuildHooksPluginRouterSkipped(t *testing.T) {
	engine := hooks.NewReloader(filepath.Join(t.TempDir(), "hooks.json"), nil)

	if p, err := buildHooksPlugin(engine, true); err != nil || p != nil {
		t.Fatalf("router squad: got (%v, %v), want (nil, nil)", p, err)
	}
	if p, err := buildHooksPlugin(engine, false); err != nil || p == nil {
		t.Fatalf("answering squad: got (%v, %v), want a plugin", p, err)
	}
	if p, err := buildHooksPlugin(nil, false); err != nil || p != nil {
		t.Fatalf("nil engine: got (%v, %v), want (nil, nil)", p, err)
	}
}

// A SessionStart hook fires when EventSessionStart is emitted on the bus through
// the once-wired listeners — the path the CLI/TUI use.
func TestWireHookListenersFiresOnBusEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "fired")
	cfgPath := filepath.Join(dir, "hooks.json")
	body := `{"hooks":{"SessionStart":[{"hooks":[{"command":"touch ` + sentinel + `"}]}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := hooks.NewReloader(cfgPath, nil)
	bus := events.NewBus()
	wireHookListeners(context.Background(), bus, engine)

	bus.Emit(events.EventSessionStart, map[string]any{"session_id": "s1"})

	// SessionStart fires synchronously, but allow a brief settle for the OS.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sentinel); err == nil {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("SessionStart hook did not run (sentinel not created)")
}
