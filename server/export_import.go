package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/sessions"
)

// exportKind marks a file produced by handleExportSession so an importer can
// recognise it (and so a future format change can bump exportVersion without
// silently mis-reading an old file).
const (
	exportKind    = "omnis.session.export"
	exportVersion = 1
	// maxImportBytes caps an uploaded export so a huge/hostile file can't
	// exhaust memory. Generous — a very long transcript is still well under this.
	maxImportBytes = 32 << 20 // 32 MiB
)

// sessionExport is the portable envelope written by GET /sessions/:id/export and
// read back by POST /import/session. The whole conversation lives in
// Conversation, which is self-contained (title, squad, collection, turns), so a
// session round-trips between Omnis instances as a single JSON file.
type sessionExport struct {
	Kind         string                     `json:"kind"`
	Version      int                        `json:"version"`
	ExportedAt   time.Time                  `json:"exported_at"`
	SourceID     string                     `json:"source_id,omitempty"`
	Conversation *sessions.ConversationFile `json:"conversation"`
}

// handleExportSession streams a session's conversation as a downloadable JSON
// envelope. GET /api/sessions/:id/export. The file is self-contained and can be
// re-imported on another Omnis instance via POST /api/import/session.
func handleExportSession(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		f, err := sessions.LoadConversationFile(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if f == nil {
			f = &sessions.ConversationFile{}
		}
		// The registry may hold a fresher title than the file (e.g. an in-memory
		// rename not yet flushed); prefer it so the export carries the name the
		// user sees.
		if meta.Title != "" {
			f.Title = meta.Title
		}
		exp := sessionExport{
			Kind:         exportKind,
			Version:      exportVersion,
			ExportedAt:   time.Now().UTC(),
			SourceID:     id,
			Conversation: f,
		}
		data, err := json.MarshalIndent(exp, "", "  ")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		filename := fmt.Sprintf("omnis-session-%s.json", id)
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		c.Data(http.StatusOK, "application/json; charset=utf-8", data)
	}
}

// parseImportedConversation accepts the three shapes an import body may take and
// returns the conversation to seed a new session from:
//   - the sessionExport envelope written by handleExportSession;
//   - a bare ConversationFile (someone hand-crafted or extracted one);
//   - a legacy plain-array file (`[ {turn}, … ]`).
func parseImportedConversation(data []byte) (*sessions.ConversationFile, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("empty import body")
	}
	// Legacy plain-array transcript.
	if trimmed[0] == '[' {
		var turns []sessions.ConversationTurn
		if err := json.Unmarshal(data, &turns); err != nil {
			return nil, fmt.Errorf("not a valid session export: %w", err)
		}
		return &sessions.ConversationFile{Turns: turns}, nil
	}
	// Envelope? Probe for the nested conversation without committing to the shape.
	var env sessionExport
	if err := json.Unmarshal(data, &env); err == nil && env.Conversation != nil {
		return env.Conversation, nil
	}
	// Bare ConversationFile.
	var f sessions.ConversationFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("not a valid session export: %w", err)
	}
	return &f, nil
}

// handleImportSession creates a fresh session seeded from an uploaded export and
// wires it up exactly like POST /sessions (register + pin + watch + reseed +
// broadcast). POST /api/import/session with the export JSON as the body.
//
// Portability sanitisation: the squad is validated against this instance and
// falls back to the router/default squad when unknown; the collection is kept
// only if it already exists here (else General); and the machine-specific /
// transient fields (cwd, goal, harvested, archived, hidden) are dropped so an
// import always lands as a normal, active chat rooted at the process cwd.
// Returns {session_id, squad, title, turns, squad_changed}.
func handleImportSession(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxImportBytes+1))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "could not read import body"})
			return
		}
		if len(raw) > maxImportBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "import file too large"})
			return
		}
		conv, err := parseImportedConversation(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if conv == nil || len(conv.Turns) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "import contains no conversation turns"})
			return
		}

		// Resolve the squad against THIS instance. An unknown/blank squad falls
		// back to the router (when routing is enabled) else the default squad, so
		// a session exported from a differently-configured instance still runs.
		wantSquad := strings.ToLower(strings.TrimSpace(conv.Squad))
		squad := wantSquad
		squadChanged := false
		if squad == "" || (d.Manager != nil && !d.Manager.HasSquad(squad)) {
			squad = toolkitagent.DefaultSquadName
			if d.Manager != nil {
				if rs := d.Manager.RouterSquad(); rs != "" {
					squad = rs
				}
			}
			squadChanged = wantSquad != "" && !strings.EqualFold(wantSquad, squad)
		}

		// Keep the collection only when it already exists here; otherwise the
		// session lands in General (an unknown collection folds to General anyway).
		collection := ""
		if want := sessions.NormalizeCollectionName(conv.Collection); want != "" {
			if known, kerr := sessions.ListCollections(); kerr == nil {
				for _, n := range known {
					if strings.EqualFold(n, want) {
						collection = n
						break
					}
				}
			}
		}

		title := strings.TrimSpace(conv.Title)
		if title == "" {
			title = "Imported session"
		}

		newMeta := d.Registry.New(squad)
		dst := &sessions.ConversationFile{
			Title:      title,
			Squad:      squad,
			Collection: collection,
			Turns:      conv.Turns,
		}
		if err := sessions.SaveConversationFile(newMeta.ID, dst); err != nil {
			d.Registry.Delete(newMeta.ID) // roll back the empty registry entry
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		d.Registry.SetTurns(newMeta.ID, len(conv.Turns))
		d.Registry.SetTitle(newMeta.ID, title)
		if collection != "" {
			d.Registry.SetCollection(newMeta.ID, collection)
		}

		// Record the starting cwd durably (the fixed initial root) so a restart in
		// a different process cwd doesn't move the session — mirroring POST
		// /sessions. The imported cwd is deliberately NOT used (it points at the
		// source machine's filesystem).
		bashCwd.set(newMeta.ID, bashCwd.get(newMeta.ID))

		// Mirror the POST /sessions wiring so the import is a first-class session.
		if d.RegisterSession != nil {
			_ = d.RegisterSession(sessions.DefaultUserID, newMeta.ID, title)
		}
		if d.Manager != nil {
			d.Manager.Pin(newMeta.ID)
			ctx, cancel := context.WithTimeout(d.rootCtx, reseedTimeout)
			_ = d.Manager.ReseedSessionContext(ctx, sessionUserID(newMeta), newMeta.ID, squad, toExchanges(conv.Turns))
			cancel()
		}
		if d.PushMgr != nil {
			d.PushMgr.Watch(d.rootCtx, d, newMeta.ID, sessions.DefaultUserID)
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("session_created", newMeta.ID)
		}
		if d.Manager != nil {
			go d.Manager.Infra().FireHook(context.Background(), hooks.SessionStart, "", hooks.Input{
				SessionID: newMeta.ID,
				Cwd:       bashCwd.get(newMeta.ID),
				Source:    "web",
			})
		}

		c.JSON(http.StatusCreated, gin.H{
			"session_id":    newMeta.ID,
			"squad":         squad,
			"title":         title,
			"turns":         len(conv.Turns),
			"squad_changed": squadChanged,
		})
	}
}
