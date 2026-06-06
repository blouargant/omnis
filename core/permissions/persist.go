package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// persistApproval appends an allow rule for (toolName, input) to the given user
// config file. cwd, when non-empty, scopes the rule to that project root.
// Idempotent: an equivalent rule already present leaves the file untouched.
func persistApproval(path, toolName, input, cwd string) error {
	if path == "" {
		return fmt.Errorf("user config path not configured")
	}
	rule := buildApprovalRule(toolName, input, cwd)

	cfg := &Config{}
	if data, err := os.ReadFile(path); err == nil {
		parsed, perr := parseConfig(data)
		if perr != nil {
			return fmt.Errorf("parse existing %s: %w", path, perr)
		}
		cfg = parsed
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	for _, r := range cfg.Permissions.Allow {
		if r.Rule == rule.Rule && r.Regex == rule.Regex && r.CWD == rule.CWD {
			return nil // already persisted
		}
	}
	cfg.Permissions.Allow = append(cfg.Permissions.Allow, rule)
	return writeConfigAtomic(path, cfg)
}

// buildApprovalRule constructs the allow Rule persisted for an approved tool
// call. Granularity differs by tool:
//
//   - File tools (Read/Write/Edit/revert) broaden to "this tool class on any
//     path" via a bare new-nomenclature spec (Read / Edit), so one approval
//     covers the other files touched in the same task. cwd still scopes
//     "Allow in this project".
//   - Bash keeps an exact-command match via the regex escape hatch (a literal
//     match on the command JSON), since a blanket shell allow is a footgun.
func buildApprovalRule(toolName, input, cwd string) Rule {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(input), &args)

	scope := "always"
	if cwd != "" {
		scope = "for this project (" + cwd + ")"
	}

	switch toolName {
	case "Bash":
		if cmd, ok := args["command"].(string); ok && cmd != "" {
			return Rule{
				Regex:  `^Bash \{"command":"` + regexp.QuoteMeta(cmd) + `"`,
				Reason: "User-approved shell command " + scope,
				CWD:    cwd,
				Tools:  []string{"Bash"},
			}
		}
	case "Read":
		return Rule{Rule: "Read", Reason: "User-approved Read on any file " + scope, CWD: cwd}
	case "Write":
		return Rule{Rule: "Write", Reason: "User-approved Write on any file " + scope, CWD: cwd}
	case "Edit", "revert":
		return Rule{Rule: "Edit", Reason: "User-approved Edit on any file " + scope, CWD: cwd}
	}
	// Fallback: exact-probe regex match.
	probe := toolName + " " + input
	return Rule{
		Regex:  "^" + regexp.QuoteMeta(probe) + "$",
		Reason: "User-approved tool call " + scope,
		CWD:    cwd,
	}
}

// writeConfigAtomic marshals cfg to path via a temp file + rename.
func writeConfigAtomic(path string, cfg *Config) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	data, err := jsonMarshalIndent(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".permissions-*.json")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func jsonMarshalIndent(v any) ([]byte, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(sb.String(), "\n") + "\n"), nil
}
