// Package packaging holds regression tests for the static packaging assets
// under this directory (the .deb/.rpm login-shell profile script, etc.) that
// are otherwise only ever exercised by actually installing a package.
package packaging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProfileScriptDoesNotBypassConfigLayers guards against reintroducing the
// OMNIS_CONFIG_PATH "explicit-file bypass" (agent/runtime_config.go
// loadRuntimeConfig, documented in CLAUDE.md's Configuration files section)
// into the shipped .deb/.rpm login-shell profile script.
//
// OMNIS_CONFIG_PATH reads agents.json VERBATIM from a single path — no
// 3-layer merge. Because /etc/profile.d/omnis.sh is sourced by every login
// shell on a packaged install, exporting it there silently kills the
// per-user overlay ($HOME/.omnis/agents.json) for the ENTIRE life of the
// install: a Settings-UI change to the agents list, squads, router_squad,
// turn_budget, embed_model_ref, eval_model_ref, or serper_key is written
// correctly to the user layer (the fork-on-first-edit write path is layer-
// aware and unaffected), but the running server generation — built from the
// same process-wide agent.Options whose ConfigPath carries the bypass — never
// incorporates it. Worse, the Settings UI's own GET for agents.json always
// re-merges the 3 layers independently of the bypass (configedit.
// LoadMergedSection ignores OMNIS_CONFIG_PATH), so the edit keeps *displaying*
// as saved and a "Reload" reports success — the divergence between what's
// shown and what's actually running has no error anywhere to surface it.
//
// The Homebrew formula, the Windows MSI installer, and the pip launcher all
// use OMNIS_SYSTEM_CONFIG_DIR instead — which relocates only the system layer
// and leaves $HOME/.omnis (and project-local .agents/) at their documented
// higher precedence. This test is what keeps the .deb/.rpm installer from
// being the one outlier that silently disables per-user overrides.
func TestProfileScriptDoesNotBypassConfigLayers(t *testing.T) {
	path := profileScriptPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)

	// Check EXECUTABLE lines only (skip comments) — the script is expected to
	// explain, in a comment, why OMNIS_CONFIG_PATH is deliberately absent, and
	// that explanation naming the variable is not itself a regression.
	if line := findExportLine(content, "OMNIS_CONFIG_PATH"); line != "" {
		t.Errorf("%s must NOT export OMNIS_CONFIG_PATH — it is the explicit-file "+
			"bypass documented in CLAUDE.md (\"Configuration files\" / the env-var "+
			"table): it reads agents.json verbatim from a single path instead of "+
			"merging the 3-layer search chain (.agents > $HOME/.omnis > /etc/omnis), "+
			"which silently disables every per-user override living in agents.json "+
			"on this install channel.\noffending line: %s", path, line)
	}
	if line := findExportLine(content, "OMNIS_WEB_DIR"); line == "" {
		t.Errorf("%s should still export OMNIS_WEB_DIR — unlike OMNIS_CONFIG_PATH "+
			"it does not bypass anything; it only locates the static web assets, "+
			"and every other packaging wrapper (brew, MSI, pip) keeps exporting the "+
			"web-dir equivalent.\ncontent:\n%s", path, content)
	}
}

// findExportLine returns the first non-comment line in content that assigns
// or exports the given shell variable name (e.g. `export FOO=` or `FOO=`),
// or "" if no such line exists. Comment lines (after trimming leading
// whitespace, starting with "#") are skipped, so a variable name may still be
// discussed in prose without tripping the check.
func findExportLine(content, varName string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, varName+"=") {
			return line
		}
	}
	return ""
}

func profileScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file location via runtime.Caller")
	}
	return filepath.Join(filepath.Dir(thisFile), "profile.d", "omnis.sh")
}
