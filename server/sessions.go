package main

import (
	"log"
	"sort"
	"sync"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
)

// SessionMeta is what we know about a chat session at the HTTP layer. ADK's
// in-memory session service holds the actual conversation history; here we
// only track lifecycle metadata for listing in the UI.
type SessionMeta struct {
	ID         string    `json:"id"`
	Title      string    `json:"title,omitempty"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Turns      int       `json:"turns"`
	// Squad is the agent squad this session uses. Chosen at session
	// creation and persisted in the conversation file. Empty means the
	// default squad (back-compat for pre-squad conversation files).
	Squad string `json:"squad,omitempty"`
	// Harvested is set by the idle harvester after it fires curator evaluation
	// for this session. A harvested session is skipped by the idle scanner until
	// new activity (Touch) clears the flag. The flag is persisted in the
	// conversation file so it survives server restarts.
	Harvested bool `json:"harvested,omitempty"`
}

const defaultUserID = "web-user"

type registry struct {
	mu    sync.RWMutex
	items map[string]*SessionMeta
}

func newRegistry() *registry {
	r := &registry{items: make(map[string]*SessionMeta)}
	for _, m := range loadPersistedSessions() {
		r.items[m.ID] = m
	}
	return r
}

func (r *registry) New(squad string) *SessionMeta {
	now := time.Now()
	r.mu.Lock()
	m := &SessionMeta{
		ID:         r.uniqueName(),
		UserID:     defaultUserID,
		CreatedAt:  now,
		LastUsedAt: now,
		Squad:      squad,
	}
	r.items[m.ID] = m
	r.mu.Unlock()
	return m
}

// uniqueName generates a human-readable adjective-noun name that does not
// collide with any session already in the registry. Must be called with r.mu held.
func (r *registry) uniqueName() string {
	for {
		name := petname.Generate(2, "-")
		if _, exists := r.items[name]; !exists {
			return name
		}
	}
}

func (r *registry) Get(id string) (*SessionMeta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.items[id]
	return m, ok
}

// Touch marks a session as used and increments the turn counter.
// It also clears the Harvested flag so the idle harvester will re-evaluate
// the session after enough new activity accumulates. The on-disk flag is
// cleared by the next appendConversationTurn call.
func (r *registry) Touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.items[id]; ok {
		m.LastUsedAt = time.Now()
		m.Turns++
		m.Harvested = false
	}
}

// MarkHarvested flags a session so the idle harvester skips it until new
// activity arrives. The flag is persisted to disk asynchronously so it
// survives server restarts.
func (r *registry) MarkHarvested(id string) {
	r.mu.Lock()
	if m, ok := r.items[id]; ok {
		m.Harvested = true
	}
	r.mu.Unlock()
	go func() {
		if err := setConversationHarvested(id, true); err != nil {
			log.Printf("harvester: failed to persist harvested flag for session %s: %v", id, err)
		}
	}()
}

func (r *registry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return false
	}
	delete(r.items, id)
	deleteConversationFile(id)
	return true
}

func (r *registry) SetTitle(id, title string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.items[id]
	if !ok {
		return false
	}
	m.Title = title
	return true
}

func (r *registry) List() []*SessionMeta {
	r.mu.RLock()
	out := make([]*SessionMeta, 0, len(r.items))
	for _, m := range r.items {
		out = append(out, m)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}
