package claudeformat

import "testing"

// TestParseCommandMarkdownFlexibleHint covers the Claude Code corpus pattern
// of writing argument-hint as a YAML flow sequence (e.g. `[name] [target]`).
// Before the flexibleText fix the sequence would fail the whole unmarshal
// and silently drop the description as well.
func TestParseCommandMarkdownFlexibleHint(t *testing.T) {
	src := []byte(`---
description: Do the thing.
argument-hint: [short description of the failure(s) to encode]
---

Prompt body here.
`)
	def, err := ParseCommandMarkdown(src)
	if err != nil {
		t.Fatal(err)
	}
	if def.Description != "Do the thing." {
		t.Errorf("description = %q, want %q", def.Description, "Do the thing.")
	}
	if def.ArgumentHint != "short description of the failure(s) to encode" {
		t.Errorf("argument-hint = %q", def.ArgumentHint)
	}
	if def.Prompt == "" {
		t.Error("prompt body lost")
	}
}

func TestParseCommandMarkdownScalarHint(t *testing.T) {
	src := []byte(`---
name: foo
description: Short.
argument-hint: <target>
---
Body.`)
	def, err := ParseCommandMarkdown(src)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "foo" || def.ArgumentHint != "<target>" {
		t.Errorf("unexpected: %+v", def)
	}
}

func TestParseCommandMarkdownNoFrontmatter(t *testing.T) {
	src := []byte("just a prompt with no frontmatter")
	def, err := ParseCommandMarkdown(src)
	if err != nil {
		t.Fatal(err)
	}
	if def.Prompt != "just a prompt with no frontmatter" {
		t.Errorf("prompt = %q", def.Prompt)
	}
	if def.Name != "" || def.Description != "" {
		t.Errorf("expected empty frontmatter fields, got %+v", def)
	}
}
