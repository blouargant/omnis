package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
	"github.com/gin-gonic/gin"
)

const (
	// distillMaxSessions bounds how many recent sessions feed one distillation.
	distillMaxSessions = 12
	// distillMaterialCap bounds the gathered material size (the distiller caps
	// again). Chosen so a dozen short sessions fit but a runaway can't blow up the
	// prompt.
	distillMaterialCap = 24000
)

// gatherCollectionMaterial builds the distiller input from a collection's recent,
// non-hidden, non-empty sessions: most-recent-first, each session's user +
// assistant turn text only (never tool calls — mirrors the session-search corpus),
// capped in count and total size. Returns "" when the collection has nothing to
// learn from yet.
func gatherCollectionMaterial(d serverDeps, collection string) string {
	var metas []*sessions.SessionMeta
	for _, m := range d.Registry.List() {
		if m == nil || m.Hidden || m.Turns == 0 {
			continue
		}
		if !strings.EqualFold(sessions.NormalizeCollectionName(m.Collection), collection) {
			continue
		}
		metas = append(metas, m)
	}
	// Most recently used first — the freshest workstream context.
	sort.Slice(metas, func(i, j int) bool { return metas[i].LastUsedAt.After(metas[j].LastUsedAt) })
	if len(metas) > distillMaxSessions {
		metas = metas[:distillMaxSessions]
	}

	var b strings.Builder
	for _, m := range metas {
		f, err := sessions.LoadConversationFile(m.ID)
		if err != nil || f == nil {
			continue
		}
		title := strings.TrimSpace(m.Title)
		if title == "" {
			title = m.ID
		}
		fmt.Fprintf(&b, "\n## Session: %s\n", title)
		for _, t := range f.Turns {
			if s := strings.TrimSpace(t.UserText); s != "" {
				fmt.Fprintf(&b, "User: %s\n", s)
			}
			if s := strings.TrimSpace(t.AssistantText); s != "" {
				fmt.Fprintf(&b, "Assistant: %s\n", s)
			}
		}
		if b.Len() > distillMaterialCap {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > distillMaterialCap {
		out = out[:distillMaterialCap]
	}
	return out
}

// handleDistillCollectionMemory gathers a collection's recent sessions, asks the
// distiller to reconcile them with the current memory, and returns the PROPOSAL —
// it deliberately does NOT write memory.md. The client shows the proposal in the
// editable memory field; the user reviews and saves it via PUT …/context. This
// propose-then-commit flow is the safeguard against an evolving memory silently
// injecting a stale or wrong fact into every new chat.
func handleDistillCollectionMemory(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, ok := resolveKnownCollection(c)
		if !ok {
			return
		}
		if d.Manager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no agent manager available"})
			return
		}
		material := gatherCollectionMaterial(d, name)
		if material == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no chats in this collection to learn from yet"})
			return
		}
		current := collectionctx.ReadMemory(name)
		size := sessions.CollectionProfileFull(name).MemorySize
		proposed, err := d.Manager.DistillCollectionMemory(c.Request.Context(), current, material, toolkitagent.SizeWordLimit(size))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"proposed": proposed, "current": current})
	}
}
