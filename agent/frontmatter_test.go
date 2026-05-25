package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInstructionMarkdown_NoFrontmatter(t *testing.T) {
	in := []byte("# Hello\n\nNo YAML here.\n")
	fm, body := ParseInstructionMarkdown(in)
	if fm.HasAny() {
		t.Fatalf("expected zero frontmatter, got %+v", fm)
	}
	if body != string(in) {
		t.Fatalf("expected body to equal input, got %q", body)
	}
}

func TestParseInstructionMarkdown_CommaTools(t *testing.T) {
	in := []byte(`---
name: api-designer
description: REST and GraphQL design.
tools: Read, Write, Edit, Bash, Glob, Grep
model: sonnet
---
You are an API designer.
`)
	fm, body := ParseInstructionMarkdown(in)
	if fm.Name != "api-designer" {
		t.Errorf("name: got %q", fm.Name)
	}
	if fm.Description != "REST and GraphQL design." {
		t.Errorf("description: got %q", fm.Description)
	}
	if fm.Model != "sonnet" {
		t.Errorf("model: got %q", fm.Model)
	}
	want := []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep"}
	if !reflect.DeepEqual([]string(fm.Tools), want) {
		t.Errorf("tools: got %v, want %v", fm.Tools, want)
	}
	if !strings.HasPrefix(body, "You are an API designer.") {
		t.Errorf("body: should start with body text, got %q", body)
	}
}

func TestParseInstructionMarkdown_ListTools(t *testing.T) {
	in := []byte(`---
name: x
tools:
  - Read
  - Bash
skills:
  - pdf
mcpServers:
  - my-server
---
body
`)
	fm, _ := ParseInstructionMarkdown(in)
	if !reflect.DeepEqual([]string(fm.Tools), []string{"Read", "Bash"}) {
		t.Errorf("tools: %v", fm.Tools)
	}
	if !reflect.DeepEqual([]string(fm.Skills), []string{"pdf"}) {
		t.Errorf("skills: %v", fm.Skills)
	}
	if !reflect.DeepEqual([]string(fm.MCPServers), []string{"my-server"}) {
		t.Errorf("mcpServers: %v", fm.MCPServers)
	}
}

func TestApplyInstructionFrontmatter_Overrides(t *testing.T) {
	e := AgentEntry{
		Name:        "old",
		Description: "old desc",
		Tools:       []string{"Bash"},
		Skills:      []string{"a"},
	}
	fm := InstructionFrontmatter{
		Name:        "new",
		Description: "new desc",
		Tools:       []string{"Read", "Write"},
	}
	applyInstructionFrontmatter(&e, fm)
	if e.Name != "new" || e.Description != "new desc" {
		t.Errorf("override failed: %+v", e)
	}
	if !reflect.DeepEqual(e.Tools, []string{"Read", "Write"}) {
		t.Errorf("tools should override: %v", e.Tools)
	}
	// Skills not provided in frontmatter — should be preserved.
	if !reflect.DeepEqual(e.Skills, []string{"a"}) {
		t.Errorf("skills should be preserved when frontmatter omits them: %v", e.Skills)
	}
}

func TestStripInstructionFrontmatter(t *testing.T) {
	in := []byte("---\nname: x\n---\nbody here\n")
	if got := StripInstructionFrontmatter(in); got != "body here\n" {
		t.Errorf("got %q", got)
	}
}
