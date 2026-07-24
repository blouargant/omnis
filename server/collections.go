package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
	"github.com/gin-gonic/gin"
)

// collectionInfo is one row in the GET /api/collections response: the display
// name plus the number of (non-hidden) sessions filed under it.
type collectionInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	// Color is the palette token chosen for this collection (empty for General
	// and for uncoloured collections). The client resolves it to a theme colour.
	Color string `json:"color,omitempty"`
	// General is true only for the synthetic default bucket, so the client can
	// pin it on top and hide its rename/delete affordances.
	General bool `json:"general,omitempty"`
	// Squad / Cwd are the collection's per-session defaults seeded onto new chats
	// (empty when unset). HasContext reports whether the collection has any
	// instructions/memory prose, so the UI can badge configured collections
	// without shipping the full text.
	Squad      string `json:"squad,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	HasContext bool   `json:"has_context,omitempty"`
}

// collectionCounts tallies non-hidden sessions by their effective collection.
// A session whose Collection field is blank — or names a collection no longer in
// the stored list — folds into General, so the totals always reconcile with the
// session list the sidebar shows.
func collectionCounts(d serverDeps, known []string) map[string]int {
	knownByFold := make(map[string]string, len(known))
	for _, n := range known {
		knownByFold[strings.ToLower(n)] = n
	}
	counts := map[string]int{sessions.GeneralCollection: 0}
	for _, n := range known {
		counts[n] = 0
	}
	for _, m := range d.Registry.List() {
		if m.Hidden {
			continue
		}
		name := sessions.NormalizeCollectionName(m.Collection)
		if name == "" {
			counts[sessions.GeneralCollection]++
			continue
		}
		if canon, ok := knownByFold[strings.ToLower(name)]; ok {
			counts[canon]++
		} else {
			// Session references a deleted/unknown collection → General.
			counts[sessions.GeneralCollection]++
		}
	}
	return counts
}

// handleListCollections returns the ordered collection list (General first, then
// user-created collections in their stored order), each with a live session count.
func handleListCollections(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		known, err := sessions.ListCollections()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		colors, err := sessions.CollectionColors()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		counts := collectionCounts(d, known)
		out := make([]collectionInfo, 0, len(known)+1)
		out = append(out, collectionInfo{Name: sessions.GeneralCollection, Count: counts[sessions.GeneralCollection], General: true})
		for _, n := range known {
			squad, cwd := sessions.CollectionProfile(n)
			out = append(out, collectionInfo{
				Name:       n,
				Count:      counts[n],
				Color:      colors[n],
				Squad:      squad,
				Cwd:        cwd,
				HasContext: collectionctx.HasContext(n),
			})
		}
		c.JSON(http.StatusOK, gin.H{"collections": out})
	}
}

// handleCreateCollection adds a new collection, optionally with a colour.
func handleCreateCollection(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		_ = c.ShouldBindJSON(&body)
		name := strings.TrimSpace(body.Name)
		if !sessions.ValidCollectionName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection name"})
			return
		}
		color := strings.TrimSpace(body.Color)
		if !sessions.ValidCollectionColor(color) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection colour"})
			return
		}
		if _, _, err := sessions.AddCollection(name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if color != "" {
			if err := sessions.SetCollectionColor(name, color); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusCreated, gin.H{"name": name, "color": color})
	}
}

// handleUpdateCollection renames a collection and/or changes its colour. A
// rename cascades onto every session filed under the old name, so no session is
// orphaned. Both fields are optional: send `name` to rename, `color` to recolour
// (empty string clears the colour), or both.
func handleUpdateCollection(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		old := strings.TrimSpace(c.Param("name"))
		if old == "" || strings.EqualFold(old, sessions.GeneralCollection) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the General collection cannot be modified"})
			return
		}
		var body struct {
			Name       string  `json:"name"`
			Color      *string `json:"color"`
			Squad      *string `json:"squad"`
			Cwd        *string `json:"cwd"`
			MemorySize *string `json:"memory_size"`
			AutoUpdate *bool   `json:"auto_update"`

			// Fleet project fields (Plan 4b) — see the block below.
			Role               *string   `json:"role"`
			Engine             *string   `json:"engine"`
			DependsOn          *[]string `json:"depends_on"`
			ClaudeAllowedTools *[]string `json:"claude_allowed_tools"`
		}
		_ = c.ShouldBindJSON(&body)

		// Resolve the collection's name after any rename — colour/profile edits key off it.
		current := old
		if newName := strings.TrimSpace(body.Name); newName != "" && newName != old {
			if !sessions.ValidCollectionName(newName) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection name"})
				return
			}
			_, ok, err := sessions.RenameCollection(old, newName)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
				return
			}
			// Cascade onto member sessions (in-memory + persisted).
			for _, m := range d.Registry.List() {
				if strings.EqualFold(sessions.NormalizeCollectionName(m.Collection), old) {
					d.Registry.SetCollection(m.ID, newName)
				}
			}
			current = newName
		}

		if body.Color != nil {
			if err := sessions.SetCollectionColor(current, strings.TrimSpace(*body.Color)); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}

		// Validate the fleet role/engine closed sets BEFORE any scalar write below,
		// so a PATCH mixing a valid scalar with an invalid role/engine 400s without
		// persisting the scalar half — "profile unchanged on rejection" holds for
		// the whole body, not just the fleet block. The actual fleet write still
		// happens further down, after the scalar block.
		if body.Role != nil {
			r := strings.TrimSpace(*body.Role)
			if r != "" && r != "project" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role (want \"project\" or empty)"})
				return
			}
		}
		if body.Engine != nil {
			e := strings.TrimSpace(*body.Engine)
			if e != "" && e != "omnis" && e != "claude" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid engine (want omnis|claude or empty)"})
				return
			}
		}

		// Per-collection scalars (squad / cwd / memory_size / auto_update). Validate
		// the incoming values, then apply them ATOMICALLY via UpdateCollectionProfile
		// (single locked read-modify-write) so a field edit here never clobbers the
		// background auto-updater's concurrent last_memory_update write. Only fields
		// present in the body change; an empty string clears that field.
		if body.Squad != nil || body.Cwd != nil || body.MemorySize != nil || body.AutoUpdate != nil {
			cur := sessions.CollectionProfileFull(current)
			sq, cw := cur.Squad, cur.Cwd
			if body.Squad != nil {
				sq = strings.TrimSpace(*body.Squad)
			}
			if body.Cwd != nil {
				cw = strings.TrimSpace(*body.Cwd)
			}
			if body.MemorySize != nil && !sessions.ValidMemorySize(strings.TrimSpace(*body.MemorySize)) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid memory size"})
				return
			}
			if sq != "" && d.Manager != nil && !d.Manager.HasSquad(strings.ToLower(sq)) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown squad " + sq})
				return
			}
			if cw != "" {
				if info, err := os.Stat(cw); err != nil || !info.IsDir() {
					c.JSON(http.StatusBadRequest, gin.H{"error": "default folder is not a directory"})
					return
				}
			}
			if err := sessions.UpdateCollectionProfile(current, func(p *sessions.CollectionProfileData) {
				if body.Squad != nil {
					p.Squad = sq
				}
				if body.Cwd != nil {
					p.Cwd = cw
				}
				if body.MemorySize != nil {
					p.MemorySize = strings.TrimSpace(*body.MemorySize)
				}
				if body.AutoUpdate != nil {
					p.AutoUpdate = *body.AutoUpdate
				}
			}); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}

		// Fleet project fields (role/engine/depends_on/claude_allowed_tools). Only
		// fields present in the body change; role + engine were already validated
		// against their closed sets above, before any write. Applied via
		// UpdateCollectionProfile like the other scalars.
		if body.Role != nil || body.Engine != nil || body.DependsOn != nil || body.ClaudeAllowedTools != nil {
			if err := sessions.UpdateCollectionProfile(current, func(p *sessions.CollectionProfileData) {
				if body.Role != nil {
					p.Role = strings.TrimSpace(*body.Role)
				}
				if body.Engine != nil {
					p.Engine = strings.TrimSpace(*body.Engine)
				}
				if body.DependsOn != nil {
					p.DependsOn = *body.DependsOn
				}
				if body.ClaudeAllowedTools != nil {
					p.ClaudeAllowedTools = *body.ClaudeAllowedTools
				}
			}); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}

		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		out := gin.H{"name": current}
		if body.Color != nil {
			out["color"] = strings.TrimSpace(*body.Color)
		}
		c.JSON(http.StatusOK, out)
	}
}

// handleDeleteCollection removes a collection; its member sessions fall back to
// the General bucket (their Collection field is cleared).
func handleDeleteCollection(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		if name == "" || strings.EqualFold(name, sessions.GeneralCollection) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the General collection cannot be deleted"})
			return
		}
		_, ok, err := sessions.RemoveCollection(name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
			return
		}
		for _, m := range d.Registry.List() {
			if strings.EqualFold(sessions.NormalizeCollectionName(m.Collection), name) {
				d.Registry.SetCollection(m.ID, "") // → General
			}
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// resolveKnownCollection returns the canonical stored name for a collection path
// param, or "" (with a written 4xx response) when it is General or unknown. The
// context routes only operate on real, existing user collections.
func resolveKnownCollection(c *gin.Context) (string, bool) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" || strings.EqualFold(name, sessions.GeneralCollection) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the General collection has no context"})
		return "", false
	}
	known, err := sessions.ListCollections()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return "", false
	}
	for _, n := range known {
		if strings.EqualFold(n, name) {
			return n, true
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
	return "", false
}

// handleGetCollectionContext returns a full editor snapshot for one collection:
// its instructions + memory prose plus its per-session defaults (squad, cwd,
// color). Writes are split — prose via PUT …/context, scalars via PATCH — but a
// single GET hands the editor everything it needs.
func handleGetCollectionContext(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, ok := resolveKnownCollection(c)
		if !ok {
			return
		}
		prof := sessions.CollectionProfileFull(name)
		colors, _ := sessions.CollectionColors()
		c.JSON(http.StatusOK, gin.H{
			"name":               name,
			"instructions":       collectionctx.ReadInstructions(name),
			"memory":             collectionctx.ReadMemory(name),
			"squad":              prof.Squad,
			"cwd":                prof.Cwd,
			"color":              colors[name],
			"memory_size":        prof.MemorySize,
			"auto_update":        prof.AutoUpdate,
			"last_memory_update": prof.LastMemoryUpdate,
			"has_prev_memory":    collectionctx.HasPrevMemory(name),

			// Fleet project fields (Plan 4b).
			"role":                 prof.Role,
			"engine":               prof.Engine,
			"depends_on":           prof.DependsOn,
			"claude_allowed_tools": prof.ClaudeAllowedTools,
		})
	}
}

// handleSetCollectionContext replaces a collection's prose. Both fields are
// optional: send `instructions` and/or `memory` to overwrite that file (an empty
// string removes it). Per-session defaults (squad/cwd) go through PATCH, not here.
func handleSetCollectionContext(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, ok := resolveKnownCollection(c)
		if !ok {
			return
		}
		var body struct {
			Instructions *string `json:"instructions"`
			Memory       *string `json:"memory"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		if body.Instructions != nil {
			if err := collectionctx.WriteInstructions(name, *body.Instructions); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		if body.Memory != nil {
			if err := collectionctx.WriteMemory(name, *body.Memory); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			// A manual memory edit supersedes any unreviewed auto-commit: consume
			// the revert snapshot + clear the auto-update marker.
			_ = collectionctx.RemovePrevMemory(name)
			_ = sessions.SetCollectionMemoryUpdate(name, 0)
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{
			"name":         name,
			"has_context":  collectionctx.HasContext(name),
			"instructions": collectionctx.ReadInstructions(name),
			"memory":       collectionctx.ReadMemory(name),
		})
	}
}

// handleRevertCollectionMemory restores a collection's previous-memory snapshot
// (written by an auto-commit) and consumes it — undoing the last automatic
// memory update. 404 when there is no snapshot to restore.
func handleRevertCollectionMemory(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, ok := resolveKnownCollection(c)
		if !ok {
			return
		}
		prev := collectionctx.ReadPrevMemory(name)
		if strings.TrimSpace(prev) == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "no previous memory to revert to"})
			return
		}
		if err := collectionctx.WriteMemory(name, prev); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		_ = collectionctx.RemovePrevMemory(name)
		_ = sessions.SetCollectionMemoryUpdate(name, 0)
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"name": name, "memory": collectionctx.ReadMemory(name)})
	}
}

// handleMoveSession files a session under a collection (empty ⇒ General). The
// target must be General or an existing collection; an unknown name is rejected
// so a typo can't strand a session under a phantom collection.
func handleMoveSession(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		var body struct {
			Collection string `json:"collection"`
		}
		_ = c.ShouldBindJSON(&body)
		target := sessions.NormalizeCollectionName(body.Collection)
		if target != "" {
			known, err := sessions.ListCollections()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			found := ""
			for _, n := range known {
				if strings.EqualFold(n, target) {
					found = n
					break
				}
			}
			if found == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown collection"})
				return
			}
			target = found // canonical stored casing
		}
		if !d.Registry.SetCollection(id, target) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.PushEvents != nil {
			name := target
			if name == "" {
				name = sessions.GeneralCollection
			}
			d.PushEvents.broadcastData("session_moved", id, map[string]any{"collection": name})
		}
		meta, _ := d.Registry.Get(id)
		c.JSON(http.StatusOK, meta)
	}
}
