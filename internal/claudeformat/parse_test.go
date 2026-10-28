package claudeformat

import "testing"

func TestParseMarkdownKeepsModelAlias(t *testing.T) {
	def, err := ParseMarkdown([]byte("---\nname: a\nmodel: haiku\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if def.ModelAlias != "haiku" {
		t.Errorf("ModelAlias = %q, want haiku", def.ModelAlias)
	}
	if def.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("Model = %q, want the normalized id", def.Model)
	}
}

func TestParseMarkdownModelAliasIsTrimmedAndOptional(t *testing.T) {
	def, err := ParseMarkdown([]byte("---\nname: a\nmodel: \"  inherit \"\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if def.ModelAlias != "inherit" || def.Model != "" {
		t.Errorf("ModelAlias=%q Model=%q", def.ModelAlias, def.Model)
	}
	def, err = ParseMarkdown([]byte("---\nname: a\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if def.ModelAlias != "" {
		t.Errorf("ModelAlias = %q, want empty", def.ModelAlias)
	}
}
