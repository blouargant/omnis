package main

import (
	"context"
	"testing"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

type fakeReseeder struct {
	has     bool
	reseeds int
}

func (f *fakeReseeder) HasSessionContext(context.Context, string, string, string) bool { return f.has }
func (f *fakeReseeder) ReseedSessionContext(context.Context, string, string, string, []toolkitagent.Exchange) error {
	f.reseeds++
	return nil
}

func TestReseedIfCold(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_ = sessions.AppendConversationTurn("s1", "hi", "hello")

	cold := &fakeReseeder{has: false}
	reseedIfCold(context.Background(), cold, "u", "s1", "system")
	if cold.reseeds != 1 {
		t.Fatal("a session with turns but no in-memory context must be reseeded")
	}
	warm := &fakeReseeder{has: true}
	reseedIfCold(context.Background(), warm, "u", "s1", "system")
	if warm.reseeds != 0 {
		t.Fatal("a warm session must not be reseeded")
	}
	empty := &fakeReseeder{has: false}
	reseedIfCold(context.Background(), empty, "u", "no-turns", "system")
	if empty.reseeds != 0 {
		t.Fatal("a session with no persisted turns has nothing to reseed")
	}
}
