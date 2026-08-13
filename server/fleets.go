// fleets.go — HTTP surface for named fleets: CRUD over the fleets.json registry
// (internal/sessions) plus assign/unassign a project (which writes the
// collection's `fleet` tag). Membership is derived (FleetMembers); this file adds
// no persistence of its own beyond the sessions registry functions. Mirrors
// server/collections.go.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/fleet"
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

// fleetProjectResponse is a fleetView plus a non-fatal advisory (embedded, so the
// JSON stays the flat fleet object and an empty warning is omitted — byte-identical
// to the bare view). Used to tell the caller "the project was created, but its
// directory is not a git repository", which the fork/worktree isolation needs.
type fleetProjectResponse struct {
	fleetView
	Warning string `json:"warning,omitempty"`
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

// handleAssignProject puts a project in a fleet. Two modes, keyed on which field
// the body carries:
//
//	{"name":…, "dir":…, "engine":…, "depends_on":[…]}  — CREATE a purpose-built
//	  project: a NEW collection that exists to BE a project, with the things a
//	  Driver actually needs (its workspace directory, engine, dependencies) set at
//	  birth. This is what the web UI's "New project…" does. A project only reuses
//	  the collection mechanism for storage — it serves a different purpose, so the
//	  UI never asks the user to sacrifice a topic folder to make one.
//	{"collection":"<existing>"}  — assign/promote an EXISTING collection: the route
//	  a collection promoted via the collection editor's Fleet-project toggle takes
//	  into a fleet, and how a project is moved between fleets.
//
// Create mode validates EVERYTHING before writing anything and rolls the new
// collection back if a later write fails: a half-made project (no workspace, or one
// silently shadowing an existing topic folder) is worse than a clean refusal.
func handleAssignProject(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		fleetName := strings.TrimSpace(c.Param("name"))
		var body struct {
			Collection         string   `json:"collection"`
			Name               string   `json:"name"`
			Dir                string   `json:"dir"`
			Color              string   `json:"color"`
			Engine             string   `json:"engine"`
			DependsOn          []string `json:"depends_on"`
			ClaudeAllowedTools []string `json:"claude_allowed_tools"`
		}
		_ = c.ShouldBindJSON(&body)

		warning := ""
		if name := strings.TrimSpace(body.Name); name != "" {
			warn, status, err := createFleetProject(fleetName, name, body.Dir, body.Color, body.Engine, body.DependsOn, body.ClaudeAllowedTools)
			if err != nil {
				c.JSON(status, gin.H{"error": err.Error()})
				return
			}
			warning = warn
		} else if err := sessions.AssignProject(fleetName, strings.TrimSpace(body.Collection)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, fleetProjectResponse{fleetView: buildFleetView(fleetName, false), Warning: warning})
	}
}

// createFleetProject creates a new project collection and files it under fleetName.
// Returns a non-fatal warning (empty when none) and the HTTP status to use on error.
// Every check runs before the first write.
func createFleetProject(fleetName, name, dir, color, engine string, dependsOn, allowedTools []string) (string, int, error) {
	name = strings.TrimSpace(name)
	dir = strings.TrimSpace(dir)
	color = strings.TrimSpace(color)
	engine = strings.TrimSpace(engine)

	if !sessions.FleetExists(fleetName) {
		return "", http.StatusNotFound, fmt.Errorf("fleet %q not found", fleetName)
	}
	if !sessions.ValidCollectionName(name) || strings.EqualFold(name, sessions.GeneralCollection) {
		return "", http.StatusBadRequest, fmt.Errorf("invalid project name %q", name)
	}
	// A create must never silently adopt an existing collection — that is exactly
	// the topic-folder/project conflation this mode exists to avoid.
	existing, err := sessions.ListCollections()
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	for _, e := range existing {
		if strings.EqualFold(e, name) {
			return "", http.StatusConflict, fmt.Errorf("a collection named %q already exists", e)
		}
	}
	if !sessions.ValidCollectionColor(color) {
		return "", http.StatusBadRequest, fmt.Errorf("invalid colour")
	}
	if engine != "" && engine != "omnis" && engine != "claude" {
		return "", http.StatusBadRequest, fmt.Errorf("invalid engine (want omnis|claude or empty)")
	}
	// The workspace directory is mandatory: a Driver is dispatched INTO it, so a
	// project without one would run against the server's own working directory.
	if dir == "" {
		return "", http.StatusBadRequest, fmt.Errorf("a project needs a workspace directory")
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return "", http.StatusBadRequest, fmt.Errorf("workspace directory %q is not a directory", dir)
	}
	deps := []string{}
	for _, dp := range dependsOn {
		if dp = strings.TrimSpace(dp); dp != "" && !strings.EqualFold(dp, name) {
			deps = append(deps, dp)
		}
	}

	if _, _, err := sessions.AddCollection(name); err != nil {
		return "", http.StatusBadRequest, err
	}
	// From here on, unwind the collection on any failure so a rejected create never
	// leaves a stray collection behind.
	fail := func(status int, err error) (string, int, error) {
		_, _, _ = sessions.RemoveCollection(name)
		return "", status, err
	}
	if color != "" {
		if err := sessions.SetCollectionColor(name, color); err != nil {
			return fail(http.StatusBadRequest, err)
		}
	}
	if err := sessions.UpdateCollectionProfile(name, func(p *sessions.CollectionProfileData) {
		p.Cwd = dir
		p.Role = "project"
		p.Engine = engine
		p.DependsOn = deps
		if engine == "claude" {
			p.ClaudeAllowedTools = allowedTools
		}
	}); err != nil {
		return fail(http.StatusBadRequest, err)
	}
	// AssignProject writes the fleet tag and seeds the engine from the fleet default
	// when the caller left it empty.
	if err := sessions.AssignProject(fleetName, name); err != nil {
		return fail(http.StatusBadRequest, err)
	}
	if !fleet.IsGitRepo(dir) {
		return "not_a_git_repo", http.StatusOK, nil
	}
	return "", http.StatusOK, nil
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
