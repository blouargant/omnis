package agentkit

import (
	"context"
	"iter"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
)

type stubLLM struct{}

func (stubLLM) Name() string { return "stub" }
func (stubLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

func instructionOf(t *testing.T, a agent.Agent) string {
	t.Helper()
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	return v.FieldByName("State").FieldByName("Instruction").String()
}

func TestAgentkitContractOverride(t *testing.T) {
	a, err := New(AgentConfig{Model: stubLLM{}, Instruction: "own", Contract: "CUSTOM"})
	if err != nil {
		t.Fatal(err)
	}
	if got := instructionOf(t, a); got != "CUSTOM\n\nown" {
		t.Errorf("root: %q", got)
	}
	a, _ = New(AgentConfig{Model: stubLLM{}, Instruction: "own", Contract: "CUSTOM", SubAgent: true})
	if got := instructionOf(t, a); got != "CUSTOM\n\nown" {
		t.Errorf("sub-agent: %q", got)
	}
	a, _ = New(AgentConfig{Model: stubLLM{}, Instruction: "own", Contract: "CUSTOM", NoContract: true})
	if got := instructionOf(t, a); got != "own" {
		t.Errorf("NoContract must win: %q", got)
	}
	a, _ = New(AgentConfig{Model: stubLLM{}, Instruction: "own"})
	if got := instructionOf(t, a); !strings.HasPrefix(got, SystemPrompt) {
		t.Errorf("default contract lost")
	}
}
