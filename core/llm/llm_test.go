package llm

import (
	"context"
	"strings"
	"testing"
)

func TestNewDefaultsToOpenAICompatAndFailsWithoutBaseURL(t *testing.T) {
	t.Setenv("GOAGENT_PROVIDER", "")
	t.Setenv("GOAGENT_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	m, err := New(context.Background())
	if err == nil {
		t.Fatalf("New() error = nil, want openai_compat base URL error (model=%v)", m)
	}
	if !strings.Contains(err.Error(), "openai_compat requires OPENAI_BASE_URL") {
		t.Fatalf("New() error = %q, want missing OPENAI_BASE_URL", err)
	}
}

func TestNewDefaultsToOpenAICompatWhenBaseURLIsSet(t *testing.T) {
	t.Setenv("GOAGENT_PROVIDER", "")
	t.Setenv("GOAGENT_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	m, err := New(context.Background())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if m == nil {
		t.Fatal("New() model = nil")
	}
	if got := m.Name(); got != "gpt-4o-mini" {
		t.Fatalf("New() model name = %q, want %q", got, "gpt-4o-mini")
	}
}