package main

import (
	"context"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestMaterializeSessionFilesUnderCollection verifies that materializeSession
// files a freshly-created session under spawnOptions.Collection, mirroring
// both the in-memory registry (meta.Collection) and the persisted conversation
// file (ConversationFile.Collection).
func TestMaterializeSessionFilesUnderCollection(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	reg := sessions.NewEmptyRegistry()
	d := serverDeps{Registry: reg, rootCtx: context.Background()}

	meta := materializeSession(d, spawnOptions{
		Squad:      "coding",
		Collection: "Service A",
		Title:      "driver: Service A",
	})
	if meta == nil {
		t.Fatal("expected a session")
	}
	if meta.Collection != "Service A" {
		t.Fatalf("session not filed under collection: %q", meta.Collection)
	}

	f, err := sessions.LoadConversationFile(meta.ID)
	if err != nil {
		t.Fatalf("load conversation file: %v", err)
	}
	if f.Collection != "Service A" {
		t.Fatalf("collection not persisted: %q", f.Collection)
	}
}
