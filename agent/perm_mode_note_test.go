package agent

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/permissions"
)

func TestPermModeNote(t *testing.T) {
	if got := permModeNote(permissions.ModeDefault); got != "" {
		t.Fatalf("default mode should add no note, got %q", got)
	}
	plan := permModeNote(permissions.ModePlan)
	if !strings.Contains(plan, "AskUserQuestion") || !strings.Contains(plan, "leave plan mode") {
		t.Fatalf("plan note missing wording: %q", plan)
	}
	if dd := permModeNote(permissions.ModeDontAsk); !strings.Contains(dd, "do not retry") {
		t.Fatalf("dontAsk note must forbid retrying: %q", dd)
	}
	seen := map[string]permissions.Mode{}
	for _, m := range []permissions.Mode{permissions.ModeAcceptEdits, permissions.ModePlan, permissions.ModeAuto, permissions.ModeDontAsk, permissions.ModeBypass} {
		n := permModeNote(m)
		if n == "" {
			t.Fatalf("mode %s: empty note", m)
		}
		if prev, dup := seen[n]; dup {
			t.Fatalf("modes %s and %s share a note", prev, m)
		}
		seen[n] = m
	}
}

func TestPermModePluginAppends(t *testing.T) {
	store := permissions.NewModeStore()
	store.Set("s1", permissions.ModePlan)

	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "ORIGINAL"}}},
	}}
	appendPermModeNote(req, store, "s1")
	parts := req.Config.SystemInstruction.Parts
	if len(parts) != 2 || parts[0].Text != "ORIGINAL" || parts[1].Text != "\n\n"+permModeNote(permissions.ModePlan) {
		t.Fatalf("note not appended after original: %+v", parts)
	}

	// Another session in default mode is untouched.
	other := &model.LLMRequest{Config: &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "ORIGINAL"}}},
	}}
	appendPermModeNote(other, store, "s2")
	if n := len(other.Config.SystemInstruction.Parts); n != 1 {
		t.Fatalf("default session should be unchanged, got %d parts", n)
	}

	// Nil config is created when a note is needed.
	fresh := &model.LLMRequest{}
	appendPermModeNote(fresh, store, "s1")
	if fresh.Config == nil || fresh.Config.SystemInstruction == nil {
		t.Fatal("expected system instruction to be created")
	}

	// nil store is safe and means default.
	none := &model.LLMRequest{}
	appendPermModeNote(none, nil, "s1")
	if none.Config != nil {
		t.Fatal("nil store should add nothing")
	}
}

func TestPermModePluginBuilds(t *testing.T) {
	p, err := permModePlugin(permissions.NewModeStore())
	if err != nil || p == nil {
		t.Fatalf("permModePlugin: %v", err)
	}
}
