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

func TestNewWithExplicitProviderAndModel(t *testing.T) {
	t.Setenv("GOAGENT_PROVIDER", "anthropic")
	t.Setenv("GOAGENT_MODEL", "ignored")
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "")

	m, err := NewWith(context.Background(), "openai_compat", "role-model")
	if err != nil {
		t.Fatalf("NewWith() error = %v", err)
	}
	if m == nil {
		t.Fatal("NewWith() model = nil")
	}
	if got := m.Name(); got != "role-model" {
		t.Fatalf("NewWith() model name = %q, want %q", got, "role-model")
	}
}

func TestNewWithProviderSpecificDefaultModel(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "")

	m, err := NewWith(context.Background(), "openai_compat", "")
	if err != nil {
		t.Fatalf("NewWith() error = %v", err)
	}
	if got := m.Name(); got != "gpt-4o-mini" {
		t.Fatalf("NewWith() model name = %q, want %q", got, "gpt-4o-mini")
	}
}

func TestNewWithSelectionUsesExplicitBaseURLAndAPIKey(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")

	m, err := NewWithSelection(context.Background(), Selection{
		Provider: "openai_compat",
		Model:    "explicit-model",
		BaseURL:  "http://explicit-host/v1",
		APIKey:   "explicit-key",
	})
	if err != nil {
		t.Fatalf("NewWithSelection() error = %v", err)
	}
	if got := m.Name(); got != "explicit-model" {
		t.Fatalf("NewWithSelection() model name = %q, want %q", got, "explicit-model")
	}
}

func TestNewWithSelectionOpenAIRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewWithSelection(context.Background(), Selection{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("NewWithSelection() error = nil, want missing OPENAI_API_KEY")
	}
	if !strings.Contains(err.Error(), "openai requires OPENAI_API_KEY") {
		t.Fatalf("NewWithSelection() error = %q, want missing OPENAI_API_KEY", err)
	}
}
