package packaging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// assetPath resolves a file under packaging/ relative to this test file, so
// the tests read the shipped assets from the repo tree (same precedent as
// profile_test.go).
func assetPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file location via runtime.Caller")
	}
	return filepath.Join(append([]string{filepath.Dir(thisFile)}, parts...)...)
}

// findIniAssign returns the first non-comment line assigning name (`name=…`),
// skipping supervisord (`;`) and shell (`#`) comment lines so a variable may be
// discussed in prose without tripping a check.
func findIniAssign(content, name string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, name+"=") {
			return line
		}
	}
	return ""
}

// TestSupervisordProgramRunsAsTheUserInsideTheirHome pins the properties the
// multi-user milestone-1 design (docs/superpowers/specs/2026-09-14-multi-user-
// container-isolation-design.md §7.2) relies on: the program runs as the user,
// keeps its state under that user's home, names the user, receives the
// per-container secret, and never bypasses the 3-layer config merge.
func TestSupervisordProgramRunsAsTheUserInsideTheirHome(t *testing.T) {
	path := assetPath(t, "supervisord", "omnis-server.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)

	if line := findIniAssign(content, "user"); !strings.Contains(line, "${OMNIS_LOGIN}") {
		t.Errorf("%s must run omnis-server as the user (user=${OMNIS_LOGIN}), got: %q", path, line)
	}
	if line := findIniAssign(content, "OMNIS_HOME"); !strings.Contains(line, `"${HOME}/.omnis"`) {
		t.Errorf("%s must place OMNIS_HOME under the user's HOME, got: %q", path, line)
	}
	if line := findIniAssign(content, "OMNIS_USER_ID"); !strings.Contains(line, "${OMNIS_LOGIN}") {
		t.Errorf("%s must set OMNIS_USER_ID to the login, got: %q", path, line)
	}
	if line := findIniAssign(content, "OMNIS_SERVER_TOKEN"); line == "" {
		t.Errorf("%s must pass OMNIS_SERVER_TOKEN (the per-container secret the gateway injects)", path)
	}
	for _, forbidden := range []string{"OMNIS_CONFIG_PATH", "OMNIS_SYSTEM_CONFIG_DIR"} {
		if line := findIniAssign(content, forbidden); line != "" {
			t.Errorf("%s must NOT set %s — the .deb layout is already the default system layer, and "+
				"OMNIS_CONFIG_PATH bypasses the 3-layer merge (CLAUDE.md \"Distribution / packaging\").\noffending line: %s",
				path, forbidden, line)
		}
	}
}

// TestContainerServerYAMLDisablesWhatTheImageOwns pins spec §7.1: the image is
// the update channel, has no display, exposes no A2A listener yet, gets its
// token from the environment, enforces an identity header, and leaves the
// listen address to the supervisord program.
func TestContainerServerYAMLDisablesWhatTheImageOwns(t *testing.T) {
	path := assetPath(t, "container", "server.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, key := range []string{"open_browser", "update_check", "a2a_enabled"} {
		v, ok := cfg[key]
		b, isBool := v.(bool)
		if !ok || !isBool || b {
			t.Errorf("%s: %s must be explicitly false (got %v)", path, key, v)
		}
	}
	if tok, _ := cfg["token"].(string); tok != "" {
		t.Errorf("%s: token must be empty — it comes from OMNIS_SERVER_TOKEN, never from the image", path)
	}
	if h, _ := cfg["identity_header"].(string); strings.TrimSpace(h) == "" {
		t.Errorf("%s: identity_header must name the gateway's login header", path)
	}
	if _, has := cfg["addr"]; has {
		t.Errorf("%s: addr must not be set here — the listen address is OMNIS_SERVER_ADDR in the supervisord program", path)
	}
}
