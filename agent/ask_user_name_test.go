package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	fstools "github.com/blouargant/omnis/core/tools"
)

// The ask-user tool was renamed from ask_user to AskUserQuestion. Prompts that
// still named the old tool made the router call it and fail with
// "tool 'ask_user' not found"; exemption maps keyed on the old name silently
// stopped exempting it. These tests pin every such reference to the live name.

func askUserToolName() string { return fstools.NewAskUserTool(nil).Name() }

func TestPromptsNameTheLiveAskUserTool(t *testing.T) {
	name := askUserToolName()
	if name == "ask_user" {
		t.Skip("tool is named ask_user again; nothing is stale")
	}
	stale := []string{"`ask_user`", "'ask_user'", "ask_user("}

	check := func(where, text string) {
		t.Helper()
		for _, s := range stale {
			if strings.Contains(text, s) {
				t.Errorf("%s names %s, but the tool is %q", where, s, name)
			}
		}
	}
	check("defaultRouterInstruction", defaultRouterInstruction())
	if strings.Contains(defaultRouterInstruction(), "and ask_user") {
		t.Errorf("defaultRouterInstruction lists ask_user as a router tool; it is %q", name)
	}
	check("languagePolicyBlock", languagePolicyBlock(true))

	for _, glob := range []string{
		filepath.Join("..", "registry", "agents", "*", "instruction.md"),
		filepath.Join("..", "registry", "skills", "*", "SKILL.md"),
	} {
		files, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			check(f, string(b))
		}
	}
}

func TestExemptionsCoverTheLiveAskUserTool(t *testing.T) {
	name := askUserToolName()
	if !budgetExemptTools[name] {
		t.Errorf("budgetExemptTools does not exempt %q: after a budget Stop the agent could no longer ask the user anything", name)
	}
	if !shaperExempt[name] {
		t.Errorf("shaperExempt does not exempt %q: a long question could be truncated", name)
	}
}
