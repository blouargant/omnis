// Package adkguard hosts a source-level guardrail (no production code): it
// fails if a raw ADK seam that ADK v2 breaks is used outside core/adk. All
// such seams must go through core/adk (adk.ToolContext, adk.EndTurnAfterToolCall,
// adk.NewEvent) so the v1->v2 migration stays a one-file change. If this test
// fails, route the offending call site through core/adk — do NOT weaken the
// guard.
package adkguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// rawADK matches a raw use of an ADK symbol v2 breaks. The alternatives that
// capture a qualifier let us skip our own façade (adk.ToolContext, adk.NewEvent).
// Limitation: an import of google.golang.org/adk/tool under a NON-default
// alias would evade the branch matching the `tool` package's `Context` type;
// omnis imports it as `tool`, and the suffix branches below catch aliased
// agent-context imports regardless of name.
var rawADK = regexp.MustCompile(
	`\btool\.Context\b` +
		`|\b(\w+)\.(ToolContext|CallbackContext|ReadonlyContext|InvocationContext)\b` +
		// Split across two literals (same compiled pattern) so this line of
		// guard source doesn't itself contain the literal substring the
		// guard scans for — otherwise the guard would flag its own file.
		`|\.Skip` + `Summarization\b` +
		`|\b(\w+)\.NewEvent(WithContext)?\(`,
)

func TestNoRawADKSeamsOutsideFacade(t *testing.T) {
	root := repoRoot(t)
	allow := map[string]bool{
		filepath.FromSlash("core/adk/adk.go"):      true,
		filepath.FromSlash("core/adk/adk_test.go"): true,
	}
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "web" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if allow[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range rawADK.FindAllString(line, -1) {
				if strings.HasPrefix(m, "adk.") { // our own façade — allowed
					continue
				}
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("raw ADK seams found outside core/adk — route them through core/adk (see docs/adk-v2-readiness.md):\n%s",
			strings.Join(offenders, "\n"))
	}
}

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}
