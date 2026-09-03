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

// The shipped fleet must not let either mutating agent sign its own work.
func TestShippedMutatingAgentsCannotAttest(t *testing.T) {
	for _, name := range []string{"k8s_editor", "k8s_cleaner"} {
		data := readShippedAgentJSON(t, name)
		if strings.Contains(data, `"attest"`) {
			t.Fatalf("%s declares the attest tool group — it could approve its own changes", name)
		}
	}
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
