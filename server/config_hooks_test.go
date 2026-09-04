package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// roundTripParsedConfig PUTs body (a raw config-file JSON body, e.g. the
// contents that would live in hooks.json) through the parsed-config editor
// route for the given whitelisted section name, then GETs the same path and
// returns the response body. It reuses the same harness as
// TestHooksParsedRoundTrip (seedConfigFile / newTestEngine / editorFiles) so
// tests exercise the real router instead of a bespoke one.
func roundTripParsedConfig(t *testing.T, name, body string) string {
	t.Helper()
	filename, ok := configFileNames[name]
	if !ok {
		t.Fatalf("unknown config section %q", name)
	}
	// Seed a minimal, valid starting file so the merge/read side has
	// something to work with before the PUT overwrites it.
	seed := "{}\n"
	if name == "hooks" {
		seed = "{\"hooks\":{}}\n"
	}
	seedConfigFile(t, filename, seed)
	r := newTestEngine(t, editorFiles())

	var data any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	w := do(t, r, http.MethodPut, "/api/config/parsed/"+name, map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	w = do(t, r, http.MethodGet, "/api/config/parsed/"+name, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// A hooks.json round trip through the parsed-config GET/PUT must preserve
// fail_closed. If a save drops it, the validation hook silently degrades to
// fail-open — the exact failure mode it exists to prevent, reintroduced by an
// unrelated edit in the Settings UI.
func TestHooksConfigRoundTripPreservesFailClosed(t *testing.T) {
	body := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[` +
		`{"type":"command","command":"python3 /etc/omnis/hooks/k8s-validate.py","timeout":60,"fail_closed":true}` +
		`]}]}}`
	got := roundTripParsedConfig(t, "hooks", body)
	if !strings.Contains(got, `"fail_closed":true`) {
		t.Fatalf("fail_closed did not survive the round trip:\n%s", got)
	}
	if !strings.Contains(got, `"timeout":60`) {
		t.Fatalf("timeout did not survive the round trip:\n%s", got)
	}
}
