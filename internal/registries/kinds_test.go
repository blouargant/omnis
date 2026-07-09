package registries

import (
	"reflect"
	"testing"
)

func TestEffectiveKinds(t *testing.T) {
	cases := []struct {
		name string
		reg  Registry
		want []string
	}{
		{"empty legacy defaults to skills", Registry{}, []string{KindSkills}},
		{"legacy single", Registry{Kind: KindMCP}, []string{KindMCP}},
		{"legacy both expands", Registry{Kind: KindBoth}, []string{KindSkills, KindAgents}},
		{"legacy joined string", Registry{Kind: "skills+mcp"}, []string{KindSkills, KindMCP}},
		{"canonical kinds preferred over legacy", Registry{Kind: KindSkills, Kinds: []string{KindMCP, KindA2A}}, []string{KindMCP, KindA2A}},
		{"kinds dedup + drop invalid", Registry{Kinds: []string{KindMCP, "bogus", KindMCP, KindSquads}}, []string{KindMCP, KindSquads}},
		{"kinds both alias expands", Registry{Kinds: []string{KindBoth, KindMCP}}, []string{KindSkills, KindAgents, KindMCP}},
		{"all invalid falls back to skills", Registry{Kinds: []string{"nope"}}, []string{KindSkills}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reg.EffectiveKinds(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("EffectiveKinds() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestServesMultiKind(t *testing.T) {
	r := Registry{Kinds: []string{KindSkills, KindMCP}}
	for _, k := range []string{KindSkills, KindMCP} {
		if !r.Serves(k) {
			t.Fatalf("expected Serves(%q) true", k)
		}
	}
	for _, k := range []string{KindAgents, KindA2A, KindSquads, KindCommands, KindPermissions} {
		if r.Serves(k) {
			t.Fatalf("expected Serves(%q) false", k)
		}
	}
	// Legacy "both" still serves skills+agents.
	both := Registry{Kind: KindBoth}
	if !both.Serves(KindSkills) || !both.Serves(KindAgents) || both.Serves(KindMCP) {
		t.Fatalf("legacy both Serves mismatch: %+v", both.EffectiveKinds())
	}
}

func TestPrimaryKind(t *testing.T) {
	cases := []struct {
		reg  Registry
		want string
	}{
		{Registry{Kind: KindMCP}, KindMCP},
		{Registry{Kind: KindBoth}, KindBoth},
		{Registry{Kinds: []string{KindSkills, KindAgents}}, KindBoth},
		{Registry{Kinds: []string{KindAgents, KindSkills}}, KindBoth},
		{Registry{Kinds: []string{KindSkills, KindMCP}}, KindSkills},
		{Registry{}, KindSkills},
	}
	for _, tc := range cases {
		if got := tc.reg.primaryKind(); got != tc.want {
			t.Fatalf("primaryKind(%+v) = %q, want %q", tc.reg.EffectiveKinds(), got, tc.want)
		}
	}
}
