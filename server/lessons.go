package main

import (
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/lessons"
)

var (
	lessonAgentRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)
	lessonIDRE    = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// lessonJSON is one lesson as the Settings list shows it.
type lessonJSON struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Cue       string `json:"cue"`
	Text      string `json:"text"`
	Worked    string `json:"worked"`
	State     string `json:"state"`
	Seen      int    `json:"seen"`
	Hinted    int    `json:"hinted"`
	Recovered int    `json:"recovered"`
	Repeated  int    `json:"repeated"`
	Created   string `json:"created"`
	LastSeen  string `json:"last_seen"`
}

// handleListLessons: GET /api/agents/:name/lessons → {"lessons":[…]}. An agent
// that learned nothing lists as empty, never 404.
func handleListLessons(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		if !lessonAgentRE.MatchString(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent name"})
			return
		}
		list, err := lessons.In(userRoot(requestLogin(c))).List(name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := make([]lessonJSON, 0, len(list))
		for _, l := range list {
			out = append(out, lessonJSON{
				ID: l.ID, Tool: l.Tool, Cue: l.Cue, Text: l.Text, Worked: l.Worked,
				State: string(l.State), Seen: l.Seen, Hinted: l.Hinted,
				Recovered: l.Recovered, Repeated: l.Repeated,
				Created:  l.Created.UTC().Format(time.RFC3339),
				LastSeen: l.LastSeen.UTC().Format(time.RFC3339),
			})
		}
		c.JSON(http.StatusOK, gin.H{"lessons": out})
	}
}

// handleDeleteLesson: DELETE /api/agents/:name/lessons/:id → {"ok":true}, 404
// when the lesson does not exist.
func handleDeleteLesson(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, id := c.Param("name"), c.Param("id")
		if !lessonAgentRE.MatchString(name) || !lessonIDRE.MatchString(id) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent name or lesson id"})
			return
		}
		store := lessons.In(userRoot(requestLogin(c)))
		list, err := store.List(name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		found := false
		for _, l := range list {
			if l.ID == id {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "lesson not found"})
			return
		}
		if err := store.Delete(name, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
