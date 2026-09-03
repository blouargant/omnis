package agent

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/adk"
)

// mark returns a BeforeToolCallback that appends name to log when invoked.
func mark(log *[]string, name string) llmagent.BeforeToolCallback {
	return func(adk.ToolContext, tool.Tool, map[string]any) (map[string]any, error) {
		*log = append(*log, name)
		return nil, nil
	}
}

// Hooks must run BEFORE permissions: a PreToolUse hook that refuses a call must
// do so without the user having already approved it, and hooks'
// permissionDecision:"allow" bypass is unreachable if the prompt already fired.
// Budget stays LAST so a call refused by a hook or the user is not charged.
func TestBeforeToolChainRunsHooksBeforePermissions(t *testing.T) {
	var log []string
	chain := beforeToolChain(
		mark(&log, "events"),
		mark(&log, "hooks"),
		mark(&log, "perms"),
		mark(&log, "budget"),
	)
	for _, cb := range chain {
		if _, err := cb(nil, nil, nil); err != nil {
			t.Fatalf("callback error: %v", err)
		}
	}
	want := []string{"events", "hooks", "perms", "budget"}
	if len(log) != len(want) {
		t.Fatalf("chain ran %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("chain ran %v, want %v", log, want)
		}
	}
}

// A nil layer is skipped, not appended — this is what makes the reorder a
// byte-identical no-op for a build with no hooks engine.
func TestBeforeToolChainSkipsNilLayers(t *testing.T) {
	var log []string
	chain := beforeToolChain(mark(&log, "events"), nil, mark(&log, "perms"), nil)
	if len(chain) != 2 {
		t.Fatalf("chain length = %d, want 2", len(chain))
	}
}

// The root's plugin order is a list literal inside buildPlugins, which needs a
// whole Infrastructure to build — so it is guarded at the source level, like
// internal/adkguard guards raw ADK forms. This asserts the ordering only; the
// behavioural guarantee is TestBeforeToolChainRunsHooksBeforePermissions.
func TestRootPluginOrderMountsHooksBeforePermissions(t *testing.T) {
	src, err := os.ReadFile("build_plugins.go")
	if err != nil {
		t.Fatalf("read build_plugins.go: %v", err)
	}
	s := string(src)
	hooks := strings.Index(s, "plugins = append(plugins, hp)")
	perms := strings.Index(s, "plugins = append(plugins, permPlugin)")
	if hooks < 0 || perms < 0 {
		t.Fatal("could not find both plugin appends — update this guard with the code")
	}
	if hooks > perms {
		t.Fatal("the hooks plugin must be appended BEFORE the permission plugin; see agent/tool_chain.go for why")
	}
}
