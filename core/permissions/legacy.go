package permissions

import (
	"encoding/json"
	"strings"
)

// legacyRule is one entry in the OLD (pre-Claude-nomenclature) permissions.json
// format: a Go regexp matched against "toolName <json args>", with optional
// tool/cwd scoping. Retained only so old files can be detected and converted.
type legacyRule struct {
	Pattern string   `json:"pattern"`
	Reason  string   `json:"reason,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

// UnmarshalJSON accepts either a bare string (the pattern) or the object form.
func (r *legacyRule) UnmarshalJSON(data []byte) error {
	t := strings.TrimSpace(string(data))
	if len(t) > 0 && t[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		r.Pattern = s
		return nil
	}
	type raw legacyRule
	var tmp raw
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*r = legacyRule(tmp)
	return nil
}

// legacyRules is the parsed old-format config (three regex tiers).
type legacyRules struct {
	AlwaysDeny  []legacyRule `json:"always_deny"`
	AlwaysAllow []legacyRule `json:"always_allow"`
	AskUser     []legacyRule `json:"ask_user"`
}

// parseLegacy unmarshals old-format permissions.json bytes.
func parseLegacy(data []byte) (*legacyRules, error) {
	var r legacyRules
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// isLegacyFormat reports whether the JSON has any old top-level tier key
// (always_deny / always_allow / ask_user). Used to trigger auto-conversion.
func isLegacyFormat(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, d := probe["always_deny"]
	_, a := probe["always_allow"]
	_, u := probe["ask_user"]
	return d || a || u
}
