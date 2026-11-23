package collmem

import (
	"strings"
	"testing"
)

func TestRenderWithRulesOverride(t *testing.T) {
	s := In(t.TempDir())
	s.Save("P", Memory{Name: "m", Description: "d", Type: TypeFeedback, Body: "b"})
	got := s.RenderWith("P", "P", false, "MY RULES")
	if !strings.Contains(got, "MY RULES") || strings.Contains(got, Rules[:40]) {
		t.Errorf("override not applied:\n%s", got)
	}
	if def := s.Render("P", "P", false); !strings.Contains(def, Rules) {
		t.Errorf("Render lost the default rules")
	}
}

func TestRenderCacheKeysOnRules(t *testing.T) {
	s := In(t.TempDir())
	s.Save("P", Memory{Name: "m", Description: "d", Type: TypeFeedback, Body: "b"})
	a := s.RenderWith("P", "P", false, "RULES A")
	b := s.RenderWith("P", "P", false, "RULES B")
	if a == b || !strings.Contains(b, "RULES B") {
		t.Errorf("same cached block for two rules texts")
	}
}
