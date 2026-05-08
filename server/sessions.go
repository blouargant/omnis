package main

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
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
}

const defaultUserID = "web-user"

type registry struct {
	mu    sync.Mutex
	items map[string]*SessionMeta
}

func newRegistry() *registry {
	r := &registry{items: make(map[string]*SessionMeta)}
	for _, m := range loadPersistedSessions() {
		r.items[m.ID] = m
	}
	return r
}

func (r *registry) New() *SessionMeta {
	now := time.Now()
	m := &SessionMeta{
		ID:         uuid.NewString(),
		UserID:     defaultUserID,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	r.mu.Lock()
	r.items[m.ID] = m
	r.mu.Unlock()
	return m
}

func (r *registry) Get(id string) (*SessionMeta, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.items[id]
	return m, ok
}

// Touch marks a session as used and increments the turn counter.
func (r *registry) Touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.items[id]; ok {
		m.LastUsedAt = time.Now()
		m.Turns++
	}
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
	r.mu.Lock()
	out := make([]*SessionMeta, 0, len(r.items))
	for _, m := range r.items {
		out = append(out, m)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}
