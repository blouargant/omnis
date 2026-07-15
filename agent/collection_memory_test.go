package agent

import (
	"context"
	"strings"
	"testing"
)

func TestBuildDistillRequest_Structure(t *testing.T) {
	req := buildDistillRequest("keep this fact", "## Session: A\nUser: hi\nAssistant: hello")
	if req.Config == nil || req.Config.SystemInstruction == nil {
		t.Fatal("missing system instruction")
	}
	sys := req.Config.SystemInstruction.Parts[0].Text
	if !strings.Contains(sys, "MEMORY") || !strings.Contains(sys, "SUPERSEDE") {
		t.Fatalf("system prompt should describe reconcile-not-append: %q", sys)
	}
	if len(req.Contents) != 1 || req.Contents[0].Role != "user" {
		t.Fatalf("expected one user content, got %+v", req.Contents)
	}
	body := req.Contents[0].Parts[0].Text
	for _, want := range []string{"CURRENT MEMORY:", "keep this fact", "RECENT SESSIONS", "Assistant: hello"} {
		if !strings.Contains(body, want) {
			t.Fatalf("user content missing %q:\n%s", want, body)
		}
	}
}

func TestBuildDistillRequest_EmptyMemoryMarker(t *testing.T) {
	req := buildDistillRequest("   ", "material")
	body := req.Contents[0].Parts[0].Text
	if !strings.Contains(body, "(empty") {
		t.Fatalf("empty current memory should be marked:\n%s", body)
	}
}

func TestBuildDistillRequest_MaterialCapped(t *testing.T) {
	big := strings.Repeat("x", collectionMaterialCap+5000)
	req := buildDistillRequest("", big)
	body := req.Contents[0].Parts[0].Text
	if !strings.Contains(body, "older sessions omitted") {
		t.Fatal("oversized material should be capped with an omission marker")
	}
	// The body should not carry the full oversized material.
	if len([]rune(body)) > collectionMaterialCap+2000 {
		t.Fatalf("material not capped: body has %d runes", len([]rune(body)))
	}
}

func TestDistillCollectionMemory_NilManagerAndEmptyMaterial(t *testing.T) {
	var m *Manager
	if _, err := m.DistillCollectionMemory(context.Background(), "", "x"); err == nil {
		t.Fatal("nil manager should error")
	}
}
