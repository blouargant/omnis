package main

import (
	"testing"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/collmem"
)

func TestApplyReviewOps(t *testing.T) {
	s := collmem.In(t.TempDir())
	save := func(name, typ, body string) collmem.Memory {
		m, _, err := s.Save("", collmem.Memory{Name: name, Type: typ, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a := save("tabs", "feedback", "Indent with tabs.")
	s.SaveWith("", a, collmem.SaveConfirm)
	a, _ = s.Get("", "tabs")
	b := save("indent", "feedback", "Use tabs.")
	c := save("old", "project", "Old fact.")
	dd := save("db", "project", "DB is stg2.")
	e := save("gone", "project", "x")
	v := toolkitagent.MemoryVersion

	ops := []reviewDecision{
		{ReviewOp: toolkitagent.ReviewOp{Op: "merge", Names: []string{"tabs", "indent"},
			Into:     &toolkitagent.ReviewInto{Name: "tabs", Type: "feedback", Description: "Tabs", Content: "Indent with tabs."},
			Versions: map[string]string{"tabs": v(a), "indent": v(b)}}},
		{ReviewOp: toolkitagent.ReviewOp{Op: "delete", Names: []string{"old"}, Versions: map[string]string{"old": v(c)}}},
		{ReviewOp: toolkitagent.ReviewOp{Op: "check", Names: []string{"db"}, Versions: map[string]string{"db": v(dd)}}, Decision: "keep"},
		{ReviewOp: toolkitagent.ReviewOp{Op: "delete", Names: []string{"gone"}, Versions: map[string]string{"gone": "stale-version"}}},
	}
	applied, skipped := applyReviewOps(s, "", ops)
	if applied != 3 || len(skipped) != 1 {
		t.Fatalf("applied %d, skipped %v", applied, skipped)
	}
	if _, ok := s.Get("", "indent"); ok {
		t.Fatal("merge must remove the other source")
	}
	if m, _ := s.Get("", "tabs"); m.Confirmations != 1 {
		t.Fatalf("merge must keep the confirmations: %+v", m)
	}
	if _, ok := s.Get("", "old"); ok {
		t.Fatal("delete not applied")
	}
	if m, _ := s.Get("", "db"); m.Confirmations != 1 {
		t.Fatalf("keep must count a confirmation: %+v", m)
	}
	if _, ok := s.Get("", "gone"); !ok || e.Name == "" {
		t.Fatal("a changed memory must not be deleted")
	}
}
