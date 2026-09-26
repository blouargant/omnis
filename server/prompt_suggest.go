package main

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

// Composer prompt suggestions: GET /sessions/:id/suggestion returns the user's
// predicted next message. The client pulls it when a turn ends in a visible pane;
// generation is on demand (Manager.SuggestNextPrompt, on the eval model) and cached
// per session keyed on the turn count, so reopening or reloading a session is free
// and a session nobody looks at never costs a model call.

const (
	suggestTurns   = 3
	suggestTimeout = 20 * time.Second
)

// suggestFunc generates a suggestion; ok=false means failure (not cached).
type suggestFunc func(ctx context.Context, sessionID string, turns []toolkitagent.Exchange) (string, bool)

type suggestEntry struct {
	turns int
	text  string
}

type suggestCall struct {
	done chan struct{}
	text string
}

// suggestStore caches one suggestion per session and single-flights concurrent
// generations for the same (session, turn count).
type suggestStore struct {
	mu      sync.Mutex
	entries map[string]suggestEntry
	calls   map[string]*suggestCall
}

func newSuggestStore() *suggestStore {
	return &suggestStore{entries: map[string]suggestEntry{}, calls: map[string]*suggestCall{}}
}

// get returns the cached suggestion for (sid, turns), or runs gen once for all
// concurrent callers. A waiter gives up when ctx ends (the generation carries on
// and still warms the cache). Only a successful generation is cached.
func (s *suggestStore) get(ctx context.Context, sid string, turns int, gen func() (string, bool)) string {
	s.mu.Lock()
	if e, ok := s.entries[sid]; ok && e.turns == turns {
		s.mu.Unlock()
		return e.text
	}
	key := sid + "#" + strconv.Itoa(turns)
	if c, ok := s.calls[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.text
		case <-ctx.Done():
			return ""
		}
	}
	c := &suggestCall{done: make(chan struct{})}
	s.calls[key] = c
	s.mu.Unlock()

	text, ok := gen()
	c.text = text
	s.mu.Lock()
	delete(s.calls, key)
	if ok {
		s.entries[sid] = suggestEntry{turns: turns, text: text}
	}
	s.mu.Unlock()
	close(c.done)
	return text
}

// forget drops a session's cached suggestion (session deleted). Nil-safe.
func (s *suggestStore) forget(sid string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.entries, sid)
	s.mu.Unlock()
}

// suggestGenerator resolves the generator: the injected one (tests), else the
// Manager's, else nil (feature inert).
func (d serverDeps) suggestGenerator() suggestFunc {
	if d.SuggestFn != nil {
		return d.SuggestFn
	}
	if d.Manager != nil {
		return d.Manager.SuggestNextPrompt
	}
	return nil
}

// suggestionsEnabled: absent preference ⇒ on; explicit false ⇒ off.
func suggestionsEnabled(store *preferencesStore) bool {
	if store == nil {
		return true
	}
	p := store.load()
	return p.PromptSuggestions == nil || *p.PromptSuggestions
}

// lastExchanges maps the trailing n persisted turns onto agent.Exchange.
func lastExchanges(turns []sessions.ConversationTurn, n int) []toolkitagent.Exchange {
	if len(turns) > n {
		turns = turns[len(turns)-n:]
	}
	out := make([]toolkitagent.Exchange, 0, len(turns))
	for _, t := range turns {
		out = append(out, toolkitagent.Exchange{User: t.UserText, Assistant: t.AssistantText})
	}
	return out
}

func handleSuggestion(d serverDeps, prefs *preferencesStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Snapshot(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		// A turn in flight: its persisted state is incomplete (Turns is bumped at
		// turn start), so generating now would cache a suggestion for the wrong
		// state under the new key. Tell the client to ask again shortly.
		if d.RunGuard != nil && d.RunGuard.busy(id) {
			c.JSON(http.StatusOK, gin.H{"suggestion": "", "turns": meta.Turns, "busy": true})
			return
		}
		empty := gin.H{"suggestion": "", "turns": meta.Turns, "busy": false}
		gen := d.suggestGenerator()
		if d.Suggest == nil || gen == nil || meta.Archived || meta.Hidden || meta.Turns == 0 || !suggestionsEnabled(prefs) {
			c.JSON(http.StatusOK, empty)
			return
		}
		root := d.rootCtx
		if root == nil {
			root = context.Background()
		}
		text := d.Suggest.get(c.Request.Context(), id, meta.Turns, func() (string, bool) {
			cf, err := sessions.LoadConversationFile(id)
			if err != nil {
				return "", false
			}
			if len(cf.Turns) == 0 {
				return "", true
			}
			// Server root context, not the request's: a client navigating away must
			// not waste a half-finished call — its result still warms the cache.
			ctx, cancel := context.WithTimeout(root, suggestTimeout)
			defer cancel()
			return gen(ctx, id, lastExchanges(cf.Turns, suggestTurns))
		})
		c.JSON(http.StatusOK, gin.H{"suggestion": text, "turns": meta.Turns, "busy": false})
	}
}
