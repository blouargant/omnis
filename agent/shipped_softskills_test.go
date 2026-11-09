package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// softSkillRE matches every spelling: softskill, soft-skill, soft skill.
var softSkillRE = regexp.MustCompile(`(?i)soft[- ]?skill`)

// TestShippedAgentsDeclareNoSoftskills guards the removal of soft-skills: no
// shipped agent may still declare the (now inert) "softskills" tool group or a
// softskills_dir, and no shipped instruction may tell the model to load a
// soft-skill that no longer exists.
func TestShippedAgentsDeclareNoSoftskills(t *testing.T) {
	for _, name := range shippedAgentNames(t) {
		data := readShippedAgentJSON(t, name)
		var entry map[string]any
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			t.Fatalf("%s/agent.json: %v", name, err)
		}
		if _, ok := entry["softskills_dir"]; ok {
			t.Errorf("%s/agent.json still declares softskills_dir", name)
		}
		if tools, ok := entry["tools"].([]any); ok {
			for _, tool := range tools {
				if tool == "softskills" {
					t.Errorf("%s/agent.json still declares the softskills tool group", name)
				}
			}
		}
		path := filepath.Join("..", "registry", "agents", name, "instruction.md")
		instr, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		if softSkillRE.Match(instr) {
			t.Errorf("%s/instruction.md still mentions soft-skills", name)
		}
	}
}
