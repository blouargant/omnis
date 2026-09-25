package agent

import (
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestPrependAgentMD_EmptyBlockNoOp(t *testing.T) {
	req := &model.LLMRequest{}
	prependAgentMD(req, "")
	if req.Config != nil {
		t.Fatalf("empty block should not touch req.Config")
	}
}

func TestPrependAgentMD_NilConfig(t *testing.T) {
	req := &model.LLMRequest{}
	prependAgentMD(req, "BLOCK")
	if req.Config == nil || req.Config.SystemInstruction == nil {
		t.Fatal("system instruction not set")
	}
	if got := req.Config.SystemInstruction.Parts[0].Text; got != "BLOCK\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrependAgentMD_PrependsBeforeExisting(t *testing.T) {
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "ORIGINAL"}}},
	}}
	prependAgentMD(req, "BLOCK")
	parts := req.Config.SystemInstruction.Parts
	if len(parts) != 2 || parts[0].Text != "BLOCK\n\n" || parts[1].Text != "ORIGINAL" {
		t.Fatalf("unexpected parts: %+v", parts)
	}
}
