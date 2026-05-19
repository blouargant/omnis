package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// AddSkillToAgent appends skillName to the agent's skills list in its
// agent.json. readDir is searched for the existing file (first-existing-wins);
// writeDir is where the updated file is saved (always $YOKE_HOME in the web
// UI, implementing fork-on-first-edit). The operation is idempotent.
func AddSkillToAgent(readDir, writeDir, agentName, skillName string) (added bool, err error) {
	if !SkillNameRe.MatchString(skillName) {
		return false, fmt.Errorf("skill name %q is not valid", skillName)
	}
	readPath := filepath.Join(readDir, agentName, "agent.json")
	data, err := os.ReadFile(readPath)
	if err != nil {
		return false, fmt.Errorf("agent %q: %w", agentName, err)
	}
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		return false, fmt.Errorf("agent %q: parse json: %w", agentName, err)
	}

	// Normalise the existing skills list.
	var skills []string
	if raw, ok := entry["skills"]; ok {
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					skills = append(skills, s)
				}
			}
		case []string:
			skills = v
		}
	}

	if slices.Contains(skills, skillName) {
		return false, nil
	}
	skills = append(skills, skillName)
	entry["skills"] = skills

	out, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return false, fmt.Errorf("agent %q: marshal json: %w", agentName, err)
	}
	out = append(out, '\n')
	writePath := filepath.Join(writeDir, agentName, "agent.json")
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return false, fmt.Errorf("agent %q: mkdir: %w", agentName, err)
	}
	if err := os.WriteFile(writePath, out, 0o644); err != nil {
		return false, fmt.Errorf("agent %q: write json: %w", agentName, err)
	}
	return true, nil
}

// RemoveSkillFromAgent removes skillName from the agent's skills list.
// readDir is searched for the existing file; writeDir is where the updated
// file is saved (fork-on-first-edit). Idempotent: no error if skill absent.
func RemoveSkillFromAgent(readDir, writeDir, agentName, skillName string) (removed bool, err error) {
	readPath := filepath.Join(readDir, agentName, "agent.json")
	data, err := os.ReadFile(readPath)
	if err != nil {
		return false, fmt.Errorf("agent %q: %w", agentName, err)
	}
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		return false, fmt.Errorf("agent %q: parse json: %w", agentName, err)
	}

	var skills []string
	if raw, ok := entry["skills"]; ok {
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					skills = append(skills, s)
				}
			}
		case []string:
			skills = v
		}
	}

	idx := slices.Index(skills, skillName)
	if idx < 0 {
		return false, nil
	}
	skills = slices.Delete(skills, idx, idx+1)
	if len(skills) == 0 {
		entry["skills"] = []string{}
	} else {
		entry["skills"] = skills
	}

	out, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return false, fmt.Errorf("agent %q: marshal json: %w", agentName, err)
	}
	out = append(out, '\n')
	writePath := filepath.Join(writeDir, agentName, "agent.json")
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return false, fmt.Errorf("agent %q: mkdir: %w", agentName, err)
	}
	if err := os.WriteFile(writePath, out, 0o644); err != nil {
		return false, fmt.Errorf("agent %q: write json: %w", agentName, err)
	}
	return true, nil
}
