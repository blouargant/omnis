package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

func TestHasInputReferences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   Server
		want bool
	}{
		{"none", Server{Env: map[string]string{"FOO": "bar"}}, false},
		{"env", Server{Env: map[string]string{"TOKEN": "${input:pat}"}}, true},
		{"header-embedded", Server{Headers: map[string]string{"Authorization": "Bearer ${input:pat}"}}, true},
		{"args", Server{Args: []string{"--token", "${input:pat}"}}, true},
		{"url", Server{URL: "https://x/${input:host}/mcp/"}, true},
		{"command", Server{Command: "/usr/bin/${input:cli}"}, true},
		{"close-but-no-cigar", Server{Env: map[string]string{"X": "${env:HOME}"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasInputReferences(tc.in); got != tc.want {
				t.Fatalf("hasInputReferences = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInputResolverCachesAndCoalesces(t *testing.T) {
	t.Parallel()

	reg := askuser.NewRegistry()
	prompts := 0
	var mu sync.Mutex
	reg.SetNotify(func(q askuser.Question) {
		go func(qid, sid string) {
			mu.Lock()
			prompts++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			_ = reg.Resolve(sid, qid, askuser.Answer{Text: "ghp_xxx"})
		}(q.ID, q.SessionID)
	})

	r := NewInputResolver(reg)
	in := Input{ID: "github_pat", Type: InputPromptString, Description: "GitHub PAT"}
	ctx := context.Background()

	v1, err := r.Resolve(ctx, in)
	if err != nil || v1 != "ghp_xxx" {
		t.Fatalf("first Resolve = (%q, %v)", v1, err)
	}
	v2, err := r.Resolve(ctx, in)
	if err != nil || v2 != "ghp_xxx" {
		t.Fatalf("cached Resolve = (%q, %v)", v2, err)
	}
	mu.Lock()
	if prompts != 1 {
		t.Fatalf("prompts = %d after cache hit, want 1", prompts)
	}
	mu.Unlock()

	// Concurrent racers for a different id should coalesce to one prompt.
	other := Input{ID: "other", Type: InputPromptString}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(ctx, other)
		}()
	}
	wg.Wait()
	mu.Lock()
	if prompts != 2 {
		t.Fatalf("prompts = %d after concurrent batch, want 2", prompts)
	}
	mu.Unlock()
}

func TestInputResolverForwardsPasswordFlag(t *testing.T) {
	t.Parallel()

	reg := askuser.NewRegistry()
	var seen askuser.Question
	ready := make(chan struct{})
	reg.SetNotify(func(q askuser.Question) {
		seen = q
		close(ready)
		go func() { _ = reg.Resolve(q.SessionID, q.ID, askuser.Answer{Text: "ok"}) }()
	})

	r := NewInputResolver(reg)
	in := Input{ID: "pat", Type: InputPromptString, Description: "secret", Password: true}
	if _, err := r.Resolve(context.Background(), in); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	<-ready
	if !seen.Password {
		t.Fatalf("Question.Password = %v, want true", seen.Password)
	}
	if seen.Kind != askuser.KindText {
		t.Fatalf("Question.Kind = %q, want text", seen.Kind)
	}
}

func TestInputResolverPickStringChoosesSelected(t *testing.T) {
	t.Parallel()

	reg := askuser.NewRegistry()
	var got askuser.Question
	ready := make(chan struct{})
	reg.SetNotify(func(q askuser.Question) {
		got = q
		close(ready)
		go func() { _ = reg.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{"prod"}}) }()
	})

	r := NewInputResolver(reg)
	in := Input{ID: "env", Type: InputPickString, Options: []string{"dev", "staging", "prod"}}
	val, err := r.Resolve(context.Background(), in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if val != "prod" {
		t.Fatalf("Resolve = %q, want prod", val)
	}
	<-ready
	if got.Kind != askuser.KindSingle {
		t.Fatalf("Kind = %q, want single", got.Kind)
	}
	if len(got.Choices) != 3 || got.Choices[2] != "prod" {
		t.Fatalf("Choices = %v", got.Choices)
	}
}

func TestResolveServerTemplatesExpandsAllFields(t *testing.T) {
	t.Parallel()

	reg := askuser.NewRegistry()
	reg.SetNotify(func(q askuser.Question) {
		go func(qid, sid, prompt string) {
			val := "VAL"
			switch {
			case strings.Contains(prompt, "PAT"):
				val = "ghp_xxx"
			case strings.Contains(prompt, "host"):
				val = "example.com"
			}
			_ = reg.Resolve(sid, qid, askuser.Answer{Text: val})
		}(q.ID, q.SessionID, q.Prompt)
	})

	r := NewInputResolver(reg)
	inputs := []Input{
		{ID: "pat", Type: InputPromptString, Description: "PAT", Password: true},
		{ID: "host", Type: InputPromptString, Description: "host"},
	}
	s := Server{
		Name: "github",
		Type: TransportHTTP,
		URL:  "https://${input:host}/mcp/",
		Headers: map[string]string{
			"Authorization": "Bearer ${input:pat}",
			"X-Constant":    "literal",
		},
	}

	out, err := resolveServerTemplates(context.Background(), r, s, inputs)
	if err != nil {
		t.Fatalf("resolveServerTemplates: %v", err)
	}
	if out.URL != "https://example.com/mcp/" {
		t.Fatalf("URL = %q", out.URL)
	}
	if out.Headers["Authorization"] != "Bearer ghp_xxx" {
		t.Fatalf("Authorization = %q", out.Headers["Authorization"])
	}
	if out.Headers["X-Constant"] != "literal" {
		t.Fatal("non-template header was mutated")
	}
	// Original must not be mutated.
	if s.Headers["Authorization"] != "Bearer ${input:pat}" {
		t.Fatal("input Server was mutated")
	}
}

func TestResolveServerTemplatesUnknownInputErrors(t *testing.T) {
	t.Parallel()

	reg := askuser.NewRegistry()
	r := NewInputResolver(reg)
	s := Server{Name: "x", Env: map[string]string{"T": "${input:missing}"}}
	_, err := resolveServerTemplates(context.Background(), r, s, nil)
	if err == nil {
		t.Fatal("want error when referencing an undeclared input")
	}
	if !strings.Contains(err.Error(), "unknown input") {
		t.Fatalf("error = %v", err)
	}
}

func TestInputResolverNilRegistryErrors(t *testing.T) {
	t.Parallel()

	r := NewInputResolver(nil)
	_, err := r.Resolve(context.Background(), Input{ID: "x", Type: InputPromptString})
	if err == nil {
		t.Fatal("Resolve with nil registry must error")
	}
}

func TestInputResolverEmptyAnswerWithDefault(t *testing.T) {
	t.Parallel()

	reg := askuser.NewRegistry()
	reg.SetNotify(func(q askuser.Question) {
		go func() { _ = reg.Resolve(q.SessionID, q.ID, askuser.Answer{Text: ""}) }()
	})

	r := NewInputResolver(reg)
	in := Input{ID: "x", Type: InputPromptString, Default: "fallback"}
	val, err := r.Resolve(context.Background(), in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if val != "fallback" {
		t.Fatalf("Resolve = %q, want fallback", val)
	}
}
