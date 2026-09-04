package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/attest"
)

// The whole guarantee rests on this: an agent that can attest its own changes
// has no reviewer. This property is invisible when reading the config, so it is
// tested.
//
// store is a real (non-nil) attest.Store: the "attest" tool group mounts only
// when both the group is declared AND the store is non-nil (a nil store is the
// CLI/TUI/example no-op contract, exercised elsewhere), so a nil store here
// would make has(["attest"]) false regardless of what toolsForAgentConfig does
// with the declared groups — the assertion below would not be a guard.
func TestAttestGroupMountsOnlyWhereDeclared(t *testing.T) {
	store := attest.New()
	has := func(keys []string) bool {
		tools, _, _, _ := toolsForAgentConfig(
			context.Background(),
			RuntimeAgentConfig{Name: "probe", Tools: keys},
			RuntimeSettings{},
			nil, nil, nil, nil, nil, nil, nil, nil, store, false, nil,
		)
		for _, tl := range tools {
			if tl.Name() == "record_validation" {
				return true
			}
		}
		return false
	}
	if !has([]string{"attest"}) {
		t.Fatal("an agent declaring the attest group must get record_validation")
	}
	if has([]string{"Bash", "Read", "Write"}) {
		t.Fatal("an agent that did not declare the attest group must NOT get record_validation")
	}
}

// The shipped fleet must let exactly ONE agent sign a verdict: k8s_validator.
//
// This is a WHITELIST, not a blacklist. An earlier version of this test only
// asserted that k8s_editor and k8s_cleaner (the two agents known to mutate the
// cluster at the time) don't declare the attest group — a blacklist of two
// names. That can never catch a THIRD mutating agent added later (a
// k8s_patcher, say) that nobody remembered to add to the list: the whole point
// of the attestation is that the signer is never the actor, so the set of
// signers must be enumerated and pinned down directly, not inferred from the
// set of known actors. The whitelist strictly subsumes the blacklist it
// replaces.
func TestOnlyTheValidatorCanAttest(t *testing.T) {
	var attesters []string
	for _, name := range shippedAgentNames(t) {
		data := readShippedAgentJSON(t, name)
		if strings.Contains(data, `"attest"`) {
			attesters = append(attesters, name)
		}
	}
	if len(attesters) != 1 || attesters[0] != "k8s_validator" {
		t.Fatalf("agents declaring the attest tool group = %v, want exactly [k8s_validator]", attesters)
	}
}

// shippedAgentNames lists every agent directory under registry/agents in the
// shipped registry (each holding an agent.json), relative to the package
// directory — the same tree readShippedAgentJSON reads from.
func shippedAgentNames(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "registry", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// readShippedAgentJSON reads the shipped registry/agents/<name>/agent.json
// relative to the package directory, for asserting against the real shipped
// config. Mirrors the relative path setupAgentsRegistry uses in
// runtime_config_test.go to reach the same tree.
func readShippedAgentJSON(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "registry", "agents", name, "agent.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
