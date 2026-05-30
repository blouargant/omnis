// Package sessions owns the multi-session metadata registry and the
// per-session conversation files on disk. Both the HTTP server and the
// embedded TUI consume the same types here so chat history, squad
// routing, and the idle-curator harvest flag stay consistent across
// surfaces.
package sessions

import (
	"log"
	"sort"
	"sync"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
)

// DefaultUserID is the user ID used when a caller (web UI, TUI, A2A)
// does not supply one. The value is part of the on-disk session naming
// scheme, so do not change it without a migration.
const DefaultUserID = "web-user"

// SessionMeta is what we know about a chat session at the
// orchestrator layer. The actual conversation history lives in the
// per-session ConversationFile; this struct only tracks lifecycle
// metadata for listing in the UI.
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
	// Archived marks a session as set-aside-but-kept: not active, but still
	// present and viewable (read-only). Archived sessions stay in the registry
	// (so the GC retains their files and they keep feeding semantic-recall
	// indexes) and are surfaced in a separate panel by the UI surfaces. The flag
	// is persisted in the conversation file so it survives server restarts.
	Archived bool `json:"archived,omitempty"`
	// Indexed is set by the idle indexer (and on archive) once a session's
	// StateLog has been pushed into the cross-session precedent index. It is
	// in-memory only and cleared by Touch on new activity so a session that
	// keeps evolving gets re-indexed; it is deliberately not persisted, so a
	// server restart re-indexes idle sessions once (an idempotent upsert).
	Indexed bool `json:"-"`
}

// Registry is the in-memory session index. It is safe for concurrent
// use and is the single source of truth for "which sessions exist
// right now".
type Registry struct {
	mu    sync.RWMutex
	items map[string]*SessionMeta
}

// NewRegistry returns a Registry seeded from the on-disk conversation
// files so the sidebar repopulates after a process restart.
func NewRegistry() *Registry {
	r := &Registry{items: make(map[string]*SessionMeta)}
	for _, m := range LoadPersistedSessions() {
		r.items[m.ID] = m
	}
	return r
}

// NewEmptyRegistry returns a Registry with no entries and skips the
// disk scan. Intended for tests that want to seed the registry
// directly via Add.
func NewEmptyRegistry() *Registry {
	return &Registry{items: make(map[string]*SessionMeta)}
}

// Add inserts m into the registry, overwriting any existing entry
// with the same ID. Intended for tests and for restoring sessions
// during startup outside of NewRegistry.
func (r *Registry) Add(m *SessionMeta) {
	if m == nil || m.ID == "" {
		return
	}
	r.mu.Lock()
	r.items[m.ID] = m
	r.mu.Unlock()
}

// NewWithName creates a session with a caller-supplied name (rather than the
// auto-generated petname). Returns nil + false when the name collides with
// an existing session or fails sanitisation. Used by the A2A handler when
// `metadata.create:true` requests an explicitly-named session.
func (r *Registry) NewWithName(name, squad string) (*SessionMeta, bool) {
	if !ValidName(name) {
		return nil, false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; exists {
		return nil, false
	}
	m := &SessionMeta{
		ID:         name,
		UserID:     DefaultUserID,
		CreatedAt:  now,
		LastUsedAt: now,
		Squad:      squad,
	}
	r.items[m.ID] = m
	return m, true
}

// ValidName accepts the character set the petname generator uses
// (kebab-case lowercase). Constraining the surface here so a remote
// caller can't accidentally inject path separators or shell-special
// bytes into a filename downstream (session ID is used as the
// conversation file name).
func ValidName(name string) bool {
	if name == "" || len(name) > 80 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

// New creates a session with an auto-generated petname ID.
func (r *Registry) New(squad string) *SessionMeta {
	now := time.Now()
	r.mu.Lock()
	m := &SessionMeta{
		ID:         r.uniqueName(),
		UserID:     DefaultUserID,
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
func (r *Registry) uniqueName() string {
	for {
		name := petname.Generate(2, "-")
		if _, exists := r.items[name]; !exists {
			return name
		}
	}
}

// Get returns the metadata for sessionID, if present.
func (r *Registry) Get(id string) (*SessionMeta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.items[id]
	return m, ok
}

// Touch marks a session as used and increments the turn counter.
// It also clears the Harvested flag so the idle harvester will re-evaluate
// the session after enough new activity accumulates. The on-disk flag is
// cleared by the next AppendConversationTurn call.
func (r *Registry) Touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.items[id]; ok {
		m.LastUsedAt = time.Now()
		m.Turns++
		m.Harvested = false
		// New activity means the goal/decisions may have changed; let the
		// idle indexer re-index this session once it goes stale again.
		m.Indexed = false
	}
}

// MarkIndexed flags a session so the idle indexer skips it until new activity
// (Touch) clears the flag. In-memory only — see SessionMeta.Indexed.
func (r *Registry) MarkIndexed(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.items[id]; ok {
		m.Indexed = true
	}
}

// MarkHarvested flags a session so the idle harvester skips it until new
// activity arrives. The flag is persisted to disk asynchronously so it
// survives server restarts.
func (r *Registry) MarkHarvested(id string) {
	r.mu.Lock()
	if m, ok := r.items[id]; ok {
		m.Harvested = true
	}
	r.mu.Unlock()
	go func() {
		if err := SetConversationHarvested(id, true); err != nil {
			log.Printf("harvester: failed to persist harvested flag for session %s: %v", id, err)
		}
	}()
}

// SetArchived sets (or clears) the archived flag on a session. The flag is
// persisted to disk asynchronously so it survives server restarts. Returns
// true when a session was found. Archived sessions remain in the registry so
// the GC keeps their files (see server/gc.go activeFromRegistry).
func (r *Registry) SetArchived(id string, v bool) bool {
	r.mu.Lock()
	m, ok := r.items[id]
	if ok {
		m.Archived = v
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	go func() {
		if err := SetConversationArchived(id, v); err != nil {
			log.Printf("sessions: failed to persist archived flag for session %s: %v", id, err)
		}
	}()
	return true
}

// Delete removes the session and its conversation file. Returns true
// when a session was found and removed.
func (r *Registry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return false
	}
	delete(r.items, id)
	DeleteConversationFile(id)
	return true
}

// SetTitle updates the in-memory title. The caller is responsible for
// persisting the change via SetConversationTitle.
func (r *Registry) SetTitle(id, title string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.items[id]
	if !ok {
		return false
	}
	m.Title = title
	return true
}

// List returns all sessions sorted by creation time, newest first.
func (r *Registry) List() []*SessionMeta {
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
