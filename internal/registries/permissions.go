package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/blouargant/yoke/core/permissions"
)

// PermissionFile is the manifest filename for a permission rule-set in a remote
// registry. Each rule-set lives in its own directory under the registry root,
// e.g. permissions/kubectl-readonly/permissions.json — the directory leaf is
// the rule-set name.
const PermissionFile = "permissions.json"

// PermissionSetName returns the rule-set name for a permissions manifest path
// (the directory leaf, e.g. "kubectl-readonly" for
// "permissions/kubectl-readonly/permissions.json").
func PermissionSetName(dirPath string) string {
	dirPath = strings.TrimSuffix(dirPath, "/")
	dir := dirPath
	if path.Base(dirPath) == PermissionFile {
		dir = path.Dir(dirPath)
	}
	leaf := path.Base(dir)
	if leaf == "." || leaf == "/" {
		return ""
	}
	return leaf
}

// BrowsePermissions lists the permission rule-sets discoverable under the
// registry root. installedPatterns, when non-nil, is the set of rule patterns
// already present in the user's permissions.json; a rule-set whose patterns are
// all present is flagged Installed.
func BrowsePermissions(ref RepoRef, token string, installedPatterns map[string]bool) ([]PermissionInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	var out []PermissionInfo
	for _, e := range entries {
		if e.Path == "__truncated__" {
			out = append(out, PermissionInfo{Name: "__truncated__", DirPath: "__truncated__"})
			continue
		}
		if e.Type != "blob" || path.Base(e.Path) != PermissionFile {
			continue
		}
		dir := path.Dir(e.Path)
		if dir == "." || dir == "" {
			continue
		}
		slash := strings.LastIndex(dir, "/")
		var group, leaf string
		if slash >= 0 {
			group, leaf = dir[:slash], dir[slash+1:]
		} else {
			leaf = dir
		}

		info := PermissionInfo{Name: leaf, DirPath: e.Path, Group: group}
		if raw, status, ferr := ref.RawFile(e.Path, token); ferr == nil && status == 200 {
			if rules, perr := parsePermissionRules(raw); perr == nil {
				patterns := rulePatterns(rules)
				info.Rules = len(patterns)
				if installedPatterns != nil && len(patterns) > 0 {
					allPresent := true
					for _, p := range patterns {
						if !installedPatterns[p] {
							allPresent = false
							break
						}
					}
					info.Installed = allPresent
				}
			}
		}
		out = append(out, info)
	}
	if out == nil {
		out = []PermissionInfo{}
	}
	return out, nil
}

// FetchPermissionJSON returns the raw permissions.json at dirPath. dirPath may
// be the full manifest path or the rule-set directory (the manifest filename is
// appended in the latter case).
func FetchPermissionJSON(ref RepoRef, token, dirPath string) ([]byte, error) {
	target := dirPath
	if path.Base(dirPath) != PermissionFile {
		target = strings.TrimSuffix(dirPath, "/") + "/" + PermissionFile
	}
	raw, status, err := ref.RawFile(target, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, target)
	}
	return raw, nil
}

// MergePermissionsFile parses the incoming permissions.json bytes and merges
// its rules into the rule-set at readPath, writing the result to writePath.
// Rules already present in a tier (matched by exact pattern string) are not
// duplicated. Returns the number of newly-added rules across all tiers.
func MergePermissionsFile(readPath, writePath string, raw []byte) (int, error) {
	incoming, err := parsePermissionRules(raw)
	if err != nil {
		return 0, fmt.Errorf("parse remote permissions.json: %w", err)
	}
	base, err := permissions.Load(readPath)
	if err != nil {
		return 0, fmt.Errorf("load %s: %w", readPath, err)
	}

	added := 0
	base.AlwaysDeny, added = appendMissing(base.AlwaysDeny, incoming.AlwaysDeny, added)
	base.AlwaysAllow, added = appendMissing(base.AlwaysAllow, incoming.AlwaysAllow, added)
	base.AskUser, added = appendMissing(base.AskUser, incoming.AskUser, added)

	out, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshal permissions.json: %w", err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}
	if err := atomicWriteFile(writePath, out); err != nil {
		return 0, fmt.Errorf("write %s: %w", writePath, err)
	}
	return added, nil
}

// appendMissing appends rules from add to base, skipping any whose Pattern is
// already present in base. Returns the grown slice and the running added count.
func appendMissing(base, add []permissions.Rule, added int) ([]permissions.Rule, int) {
	seen := make(map[string]bool, len(base))
	for _, r := range base {
		seen[r.Pattern] = true
	}
	for _, r := range add {
		if seen[r.Pattern] {
			continue
		}
		seen[r.Pattern] = true
		base = append(base, r)
		added++
	}
	return base, added
}

// parsePermissionRules unmarshals permissions.json bytes into a Rules value
// (without compiling regexps — callers only need the rule data here).
func parsePermissionRules(raw []byte) (*permissions.Rules, error) {
	var r permissions.Rules
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// rulePatterns flattens every tier's patterns into one slice.
func rulePatterns(r *permissions.Rules) []string {
	var out []string
	for _, tier := range [][]permissions.Rule{r.AlwaysDeny, r.AlwaysAllow, r.AskUser} {
		for _, rule := range tier {
			if rule.Pattern != "" {
				out = append(out, rule.Pattern)
			}
		}
	}
	return out
}

// InstalledPermissionPatterns returns the set of rule patterns currently in the
// permissions.json at path. Used to annotate the Installed flag when browsing a
// permissions registry. Returns an empty set on any read/parse error.
func InstalledPermissionPatterns(path string) map[string]bool {
	out := map[string]bool{}
	rules, err := permissions.Load(path)
	if err != nil {
		return out
	}
	for _, p := range rulePatterns(rules) {
		out[p] = true
	}
	return out
}
