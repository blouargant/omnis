package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// skillsChangedHook is called after a skill load is persisted for a session.
// main installs one that broadcasts `skills_changed` on /api/events so every
// browser showing the session refreshes its composer skills area. Nil in
// tests ⇒ the load is only persisted.
var skillsChangedHook func(sessionID string)

// skillKindForTool maps a loader tool name to the recorded skill kind, or ""
// when the tool is not a skill loader.
func skillKindForTool(tool string) string {
	switch tool {
	case "load_skill":
		return sessions.SkillKindSkill
	case "load_softskill":
		return sessions.SkillKindSoftskill
	}
	return ""
}

// loadedSkillFromResponse builds the record for a successful skill load, or
// reports false when the tool is not a loader or the load failed (no
// instructions in the response).
func loadedSkillFromResponse(agent, tool string, resp map[string]any) (sessions.LoadedSkill, bool) {
	kind := skillKindForTool(tool)
	if kind == "" || resp == nil {
		return sessions.LoadedSkill{}, false
	}
	instr, _ := resp["instructions"].(string)
	if strings.TrimSpace(instr) == "" {
		return sessions.LoadedSkill{}, false
	}
	s := sessions.LoadedSkill{Kind: kind, Agent: agent, Instructions: instr}
	s.Name, _ = resp["skill_name"].(string)
	if fm, ok := resp["frontmatter"].(map[string]any); ok {
		if s.Name == "" {
			s.Name, _ = fm["name"].(string)
		}
		s.Description, _ = fm["description"].(string)
		if isSystemSkill(fm) {
			return sessions.LoadedSkill{}, false
		}
	}
	s.DependencyStatus, _ = resp["dependency_status"].(string)
	return s, s.Name != ""
}

// isSystemSkill reports whether a skill's frontmatter marks it as a system
// skill (`metadata: {system: "true"}`), e.g. the built-in wrap-session
// soft-skill. System skills are plumbing, not something the user chose to
// engage, so they are never listed in the composer skills dock.
func isSystemSkill(fm map[string]any) bool {
	md, ok := fm["metadata"].(map[string]any)
	if !ok {
		return false
	}
	v, _ := md["system"].(string)
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// recordSkillLoad persists a successful skill / soft-skill load for the
// session and notifies the browsers. A no-op for any other tool.
func recordSkillLoad(sessionID, agent, tool string, resp map[string]any) {
	if sessionID == "" {
		return
	}
	s, ok := loadedSkillFromResponse(agent, tool, resp)
	if !ok {
		return
	}
	if err := sessions.RecordLoadedSkill(sessionID, s); err != nil {
		log.Printf("server: record loaded skill %q for %s: %v", s.Name, sessionID, err)
		return
	}
	if skillsChangedHook != nil {
		skillsChangedHook(sessionID)
	}
}

// handleSessionSkills serves GET /api/sessions/:id/skills — the session's
// loaded skills, in load order.
func handleSessionSkills(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		skills, err := sessions.LoadConversationSkills(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if skills == nil {
			skills = []sessions.LoadedSkill{}
		}
		c.JSON(http.StatusOK, gin.H{"skills": skills})
	}
}
