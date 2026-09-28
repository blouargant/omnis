package sessions

import (
	"strings"
	"time"
)

// Skill kinds recorded in LoadedSkill.Kind.
const (
	SkillKindSkill     = "skill"     // authored skill (load_skill)
	SkillKindSoftskill = "softskill" // curator-distilled soft-skill (load_softskill)
)

// maxSkillInstructions caps the SKILL.md body kept per loaded skill so a huge
// skill cannot bloat the conversation file. The web UI only needs it to show
// the skill's content on demand.
const maxSkillInstructions = 64 * 1024

// LoadedSkill is one skill (or soft-skill) an agent loaded during the session.
// The web UI lists these in a collapsible area of the composer instead of
// rendering each load as a tool block in the transcript. Persisted so the list
// survives a page reload / server restart.
type LoadedSkill struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Agent            string `json:"agent,omitempty"`
	Description      string `json:"description,omitempty"`
	Instructions     string `json:"instructions,omitempty"`
	DependencyStatus string `json:"dependency_status,omitempty"`
	Count            int    `json:"count"`
	// Turn is the index of the turn during which the skill was first loaded,
	// so a rewind / fork can drop the skills loaded after its cut point.
	Turn int       `json:"turn"`
	At   time.Time `json:"at"`
}

// RecordLoadedSkill adds s to the session's loaded-skill list, or bumps the
// count of an existing entry with the same (kind, name, agent). The turn index
// is the number of turns already persisted — the turn in flight is appended
// only when it completes.
func RecordLoadedSkill(sessionID string, s LoadedSkill) error {
	s.Name = strings.TrimSpace(s.Name)
	if sessionID == "" || s.Name == "" {
		return nil
	}
	if len(s.Instructions) > maxSkillInstructions {
		s.Instructions = s.Instructions[:maxSkillInstructions]
	}
	return mutateConversation(sessionID, func(f *ConversationFile) {
		for i := range f.Skills {
			e := &f.Skills[i]
			if e.Kind == s.Kind && e.Name == s.Name && e.Agent == s.Agent {
				e.Count++
				e.At = time.Now()
				if s.Instructions != "" {
					e.Instructions = s.Instructions
				}
				if s.Description != "" {
					e.Description = s.Description
				}
				e.DependencyStatus = s.DependencyStatus
				return
			}
		}
		s.Count = 1
		s.Turn = len(f.Turns)
		if s.At.IsZero() {
			s.At = time.Now()
		}
		f.Skills = append(f.Skills, s)
	})
}

// LoadConversationSkills returns the session's loaded-skill list (nil when none).
func LoadConversationSkills(sessionID string) ([]LoadedSkill, error) {
	f, err := LoadConversationFile(sessionID)
	if err != nil {
		return nil, err
	}
	return f.Skills, nil
}

// skillsBefore returns the entries first loaded before turn `keep`.
func skillsBefore(skills []LoadedSkill, keep int) []LoadedSkill {
	var out []LoadedSkill
	for _, s := range skills {
		if s.Turn < keep {
			out = append(out, s)
		}
	}
	return out
}
