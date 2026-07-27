// fleets.go — HTTP surface for named fleets: CRUD over the fleets.json registry
// (internal/sessions) plus assign/unassign a project (which writes the
// collection's `fleet` tag). Membership is derived (FleetMembers); this file adds
// no persistence of its own beyond the sessions registry functions. Mirrors
// server/collections.go.
package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

type fleetMemberView struct {
	Name      string   `json:"name"`
	Engine    string   `json:"engine,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type fleetView struct {
	Name          string            `json:"name"`
	Color         string            `json:"color,omitempty"`
	Description   string            `json:"description,omitempty"`
	DefaultEngine string            `json:"default_engine,omitempty"`
	ProjectCount  int               `json:"project_count"`
	Engines       []string          `json:"engines,omitempty"`
	Members       []fleetMemberView `json:"members"`
	Ungrouped     bool              `json:"ungrouped,omitempty"`
}

// buildFleetView assembles one fleet's row: metadata + derived members (each with
// its engine + depends_on read from the collection profile) + the distinct engine
// set. Ungrouped carries no metadata.
func buildFleetView(name string, ungrouped bool) fleetView {
	members := sessions.FleetMembers(name)
	v := fleetView{Name: name, ProjectCount: len(members), Members: []fleetMemberView{}, Ungrouped: ungrouped}
	if !ungrouped {
		m := sessions.FleetMetaFor(name)
		v.Color, v.Description, v.DefaultEngine = m.Color, m.Description, m.DefaultEngine
	}
	seenEngine := map[string]bool{}
	for _, mem := range members {
		p := sessions.CollectionProfileFull(mem)
		v.Members = append(v.Members, fleetMemberView{Name: mem, Engine: p.Engine, DependsOn: p.DependsOn})
		if p.Engine != "" && !seenEngine[p.Engine] {
			seenEngine[p.Engine] = true
			v.Engines = append(v.Engines, p.Engine)
		}
	}
	return v
}

func handleListFleets(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		names, err := sessions.ListFleets()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := []fleetView{}
		for _, n := range names {
			out = append(out, buildFleetView(n, false))
		}
		// Append Ungrouped only when it has members.
		if ung := buildFleetView(sessions.UngroupedFleet, true); ung.ProjectCount > 0 {
			out = append(out, ung)
		}
		c.JSON(http.StatusOK, out)
	}
}

func handleCreateFleet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Name          string `json:"name"`
			Color         string `json:"color"`
			Description   string `json:"description"`
			DefaultEngine string `json:"default_engine"`
		}
		_ = c.ShouldBindJSON(&body)
		name := strings.TrimSpace(body.Name)
		if !sessions.ValidFleetName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fleet name"})
			return
		}
		if !sessions.ValidCollectionColor(body.Color) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid colour"})
			return
		}
		if !sessions.ValidDefaultEngine(body.DefaultEngine) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid default engine"})
			return
		}
		if _, _, err := sessions.AddFleet(name, sessions.FleetMetaData{
			Color: body.Color, Description: body.Description, DefaultEngine: body.DefaultEngine,
		}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(name, false))
	}
}

func handleUpdateFleet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		var body struct {
			Name          *string `json:"name"`
			Color         *string `json:"color"`
			Description   *string `json:"description"`
			DefaultEngine *string `json:"default_engine"`
		}
		_ = c.ShouldBindJSON(&body)
		// Validate everything BEFORE writing anything (atomic-on-rejection).
		if body.Name != nil && !sessions.ValidFleetName(strings.TrimSpace(*body.Name)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fleet name"})
			return
		}
		if body.Color != nil && !sessions.ValidCollectionColor(*body.Color) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid colour"})
			return
		}
		if body.DefaultEngine != nil && !sessions.ValidDefaultEngine(*body.DefaultEngine) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid default engine"})
			return
		}
		final := name
		if body.Name != nil {
			nn := strings.TrimSpace(*body.Name)
			// Exact-string compare (not EqualFold) so a case-only re-case
			// ("payments" → "Payments") reaches RenameFleet, which supports it and
			// migrates the metadata + member tags. Mirrors handleUpdateCollection;
			// a case-only skip here would silently strand the data-layer support
			// and diverge from collection renames. nn is already validated non-empty
			// + ValidFleetName above.
			if nn != name {
				if _, _, err := sessions.RenameFleet(name, nn); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
			}
			final = nn
		}
		if body.Color != nil || body.Description != nil || body.DefaultEngine != nil {
			if err := sessions.UpdateFleetMeta(final, func(m *sessions.FleetMetaData) {
				if body.Color != nil {
					m.Color = *body.Color
				}
				if body.Description != nil {
					m.Description = *body.Description
				}
				if body.DefaultEngine != nil {
					m.DefaultEngine = *body.DefaultEngine
				}
			}); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(final, false))
	}
}

func handleDeleteFleet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		if _, _, err := sessions.RemoveFleet(name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			// Members returned to Ungrouped changed a collection's role/visibility.
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func handleAssignProject(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		var body struct {
			Collection string `json:"collection"`
		}
		_ = c.ShouldBindJSON(&body)
		if err := sessions.AssignProject(name, strings.TrimSpace(body.Collection)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(name, false))
	}
}

func handleUnassignProject(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		collection := strings.TrimSpace(c.Param("collection"))
		if err := sessions.UnassignProject(collection); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(name, false))
	}
}

// registerFleetRoutes mounts the /api/fleets surface on the given (auth) group.
func registerFleetRoutes(g *gin.RouterGroup, d serverDeps) {
	g.GET("/fleets", handleListFleets(d))
	g.POST("/fleets", handleCreateFleet(d))
	g.PATCH("/fleets/:name", handleUpdateFleet(d))
	g.DELETE("/fleets/:name", handleDeleteFleet(d))
	g.POST("/fleets/:name/projects", handleAssignProject(d))
	g.DELETE("/fleets/:name/projects/:collection", handleUnassignProject(d))
}
