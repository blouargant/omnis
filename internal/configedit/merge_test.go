package configedit

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func mustParse(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("parse: %v\n%s", err, s)
	}
	return m
}

// canon deep-sorts every array of scalars/objects so equality is set-level for
// order-insensitive collections (agents list, permission tiers, squad order).
func canon(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = canon(e)
		}
		return out
	case []any:
		items := make([]any, len(x))
		for i, e := range x {
			items[i] = canon(e)
		}
		sort.Slice(items, func(i, j int) bool {
			return toJSON(items[i]) < toJSON(items[j])
		})
		return items
	default:
		return v
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func assertRoundTrip(t *testing.T, filename string, base, desired map[string]any) {
	t.Helper()
	overlay := DiffSection(filename, base, desired)
	layers := []map[string]any{base}
	if overlay != nil {
		layers = append(layers, overlay)
	}
	got := MergeSection(filename, layers)
	if !reflect.DeepEqual(canon(got), canon(desired)) {
		t.Fatalf("round-trip mismatch for %s\n base:    %s\n desired: %s\n overlay: %s\n got:     %s",
			filename, toJSON(base), toJSON(desired), toJSON(overlay), toJSON(got))
	}
}

// The reported bug: a package ships a new agent (system layer) but the user has
// a full forked agents.json (user layer) that predates it. The new agent must
// still surface after merge.
func TestPackageAddsAgentBehindUserOverride(t *testing.T) {
	system := mustParse(t, `{
		"agents": ["leader","investigator","newagent"],
		"squads": [
			{"name":"Default","leader":"leader","members":["investigator","newagent"]},
			{"name":"Fresh","leader":"leader","members":["newagent"]}
		]
	}`)
	// User forked the whole file before newagent/Fresh existed, and tweaked one
	// squad's members.
	user := mustParse(t, `{
		"agents": ["leader","investigator"],
		"squads": [
			{"name":"Default","leader":"leader","members":["investigator"]}
		]
	}`)
	merged := MergeSection("agents.json", []map[string]any{system, user})

	agents := toStringSlice(merged["agents"])
	if !contains(agents, "newagent") {
		t.Fatalf("newagent should surface through the user override; got %v", agents)
	}
	// The user's Default squad edit wins (members replace); the new Fresh squad
	// from the package appears.
	squads, _ := merged["squads"].([]any)
	byName := map[string]map[string]any{}
	for _, s := range squads {
		m := s.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if byName["Fresh"] == nil {
		t.Fatalf("package squad Fresh should surface; got %v", toJSON(merged["squads"]))
	}
	defMembers := toStringSlice(byName["Default"]["members"])
	if len(defMembers) != 1 || defMembers[0] != "investigator" {
		t.Fatalf("user's Default members should win (replace); got %v", defMembers)
	}
}

func TestModelsMapDeepMergeAndEvolution(t *testing.T) {
	system := mustParse(t, `{
		"providers": {"prod": {"kind":"openai_compat","base_url":"URL_A","api_key":"K"}},
		"models": {
			"premium": {"provider_ref":"prod","model":"m1","input_token_price_per_million":5},
			"cheap":   {"provider_ref":"prod","model":"m2"}
		}
	}`)
	// User only changed premium's price. Later the package added a NEW model and
	// updated premium's model id — the untouched field must flow through.
	user := mustParse(t, `{"models": {"premium": {"input_token_price_per_million":9}}}`)
	systemUpdated := mustParse(t, `{
		"providers": {"prod": {"kind":"openai_compat","base_url":"URL_A","api_key":"K"}},
		"models": {
			"premium": {"provider_ref":"prod","model":"m1-new","input_token_price_per_million":5},
			"cheap":   {"provider_ref":"prod","model":"m2"},
			"balanced":{"provider_ref":"prod","model":"m3"}
		}
	}`)
	merged := MergeSection("models.json", []map[string]any{systemUpdated, user})
	models := merged["models"].(map[string]any)
	if models["balanced"] == nil {
		t.Fatalf("new package model 'balanced' should surface")
	}
	prem := models["premium"].(map[string]any)
	if prem["model"] != "m1-new" {
		t.Fatalf("package update to premium.model should flow through; got %v", prem["model"])
	}
	if prem["input_token_price_per_million"].(float64) != 9 {
		t.Fatalf("user price override should win; got %v", prem["input_token_price_per_million"])
	}
	_ = system
}

func TestAgentsTombstoneRemovesPackageAgent(t *testing.T) {
	system := mustParse(t, `{"agents":["leader","investigator","spammy"]}`)
	user := mustParse(t, `{"agents_removed":["spammy"],"agents":["custom"]}`)
	merged := MergeSection("agents.json", []map[string]any{system, user})
	agents := toStringSlice(merged["agents"])
	if contains(agents, "spammy") {
		t.Fatalf("tombstone should drop spammy; got %v", agents)
	}
	if !contains(agents, "custom") || !contains(agents, "leader") {
		t.Fatalf("union should keep custom + leader; got %v", agents)
	}
	if _, leaked := merged["agents_removed"]; leaked {
		t.Fatalf("tombstone key must not appear in effective config")
	}
}

func TestPermissionsTierUnionAndDefaultMode(t *testing.T) {
	base := mustParse(t, `{"permissions":{"defaultMode":"default","deny":["Bash(rm *)"],"allow":["Read"]}}`)
	desired := mustParse(t, `{"permissions":{"defaultMode":"acceptEdits","deny":["Bash(rm *)","Bash(dd *)"],"allow":["Read","Edit"]}}`)
	assertRoundTrip(t, "permissions.json", base, desired)

	overlay := DiffSection("permissions.json", base, desired)
	perm := overlay["permissions"].(map[string]any)
	if perm["defaultMode"] != "acceptEdits" {
		t.Fatalf("changed defaultMode must be in overlay; got %v", perm["defaultMode"])
	}
	if got := toStringSlice(perm["deny"]); len(got) != 1 || got[0] != "Bash(dd *)" {
		t.Fatalf("overlay deny should hold only the added rule; got %v", got)
	}
}

func TestRoundTripAgents(t *testing.T) {
	base := mustParse(t, `{
		"agents":["leader","investigator"],
		"squads":[{"name":"Default","leader":"leader","members":["investigator"],"description":"d"}],
		"router_squad":"omnis"
	}`)
	desired := mustParse(t, `{
		"agents":["leader","investigator","custom"],
		"squads":[
			{"name":"Default","leader":"leader2","members":["investigator","custom"],"description":"d"},
			{"name":"Extra","leader":"leader","members":["custom"]}
		],
		"router_squad":"omnis"
	}`)
	assertRoundTrip(t, "agents.json", base, desired)
}

func TestRoundTripMCPAndA2A(t *testing.T) {
	base := mustParse(t, `{"servers":{"git":{"command":"g","args":["x"]}}}`)
	desired := mustParse(t, `{"servers":{"git":{"command":"g","args":["x"]},"fs":{"command":"f"}}}`)
	assertRoundTrip(t, "mcp_config.json", base, desired)

	baseA := mustParse(t, `{"agents":{"peer":{"url":"u"}},"inputs":[{"id":"tok","type":"promptString"}]}`)
	desiredA := mustParse(t, `{"agents":{"peer":{"url":"u2"},"peer2":{"url":"z"}},"inputs":[{"id":"tok","type":"promptString"},{"id":"k2","type":"promptString"}]}`)
	assertRoundTrip(t, "a2a_config.json", baseA, desiredA)
}

func TestRoundTripHooks(t *testing.T) {
	base := mustParse(t, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo a"}]}]}}`)
	desired := mustParse(t, `{"hooks":{
		"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo a"}]}],
		"PostToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"fmt"}]}]
	}}`)
	assertRoundTrip(t, "hooks.json", base, desired)
}

func contains(l []string, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}
