// Package sessindex builds and queries a semantic index over past chat sessions
// so a user (or the session_search agent) can find the conversation where
// something was discussed — including archived ones.
//
// It mirrors internal/docindex: the corpus is chunked, re-indexing is
// content-hash gated per session (only changed sessions are re-embedded, chunks
// of deleted sessions are removed), and the index is a single global store under
// $OMNIS_HOME/index/sessions. Three things differ:
//
//   - The unit is a TURN, not a line window. A turn (user request + assistant
//     reply) is the smallest thing a user would want to jump back to, and it is
//     exactly "what was presented to the user" — tool calls are not indexed.
//   - Results are folded BY SESSION (best-scoring turn wins), because the user
//     is looking for a conversation, not a paragraph.
//   - The index is UNLOADED after a period of search inactivity (see
//     StartIdleSweeper). Sessions accumulate without bound, and their text lives
//     in the metadata map, so unlike the docs/registry indexes this one is only
//     worth holding in memory while it is actually being used.
//
// Without an embedder there is no index at all and every caller falls back to
// Scan (a direct, warned-about walk of the conversation files) — the same
// additive contract the other semantic features keep.
package sessindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/omnis/core/embed"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/semindex"
)

const (
	// maxChunkChars caps one indexed chunk. A turn longer than this is split into
	// overlapping windows so a long conversation's tail is still reachable and no
	// single embed call exceeds the model's context.
	maxChunkChars = 2400
	chunkOverlap  = 240

	// defaultIdleTTL is how long the index may sit unused before it is dropped
	// from memory (override with OMNIS_SESSION_INDEX_IDLE).
	defaultIdleTTL = 10 * time.Minute
	sweepInterval  = time.Minute
)

// chunkMeta is the per-chunk metadata persisted in the index sidecar.
//
// It deliberately carries NO title / collection / archived flag: those are
// mutable session properties (a rename, a move, an archive) and freezing them
// here would leave the search results showing stale ones. They are resolved live
// from the session registry when a hit is rendered; only the immutable
// coordinates (which session, which turn, when) and the text are stored.
type chunkMeta struct {
	SessionID string    `json:"session_id"`
	TurnIndex int       `json:"turn_index"`
	At        time.Time `json:"at"`
	Text      string    `json:"text"`
}

// sessionRecord tracks a session's indexed-content hash and the chunk ids
// derived from it, so stale chunks are removed when the session grows or is
// deleted. The hash covers ONLY the indexed text (the turns), not the whole
// file — otherwise flipping an unrelated flag (harvested, archived, cwd) would
// re-embed the entire conversation.
type sessionRecord struct {
	Hash string   `json:"hash"`
	IDs  []uint64 `json:"ids"`
}

// Index is the global past-session search index.
type Index struct {
	store     *semindex.Store
	emb       embed.Embedder
	filesPath string
	idleTTL   time.Duration

	mu       sync.Mutex
	sessions map[string]sessionRecord // session id → record
	lastUsed time.Time
}

// Open opens (or creates) the session index backed by emb. Returns (nil, nil)
// when emb is nil, so callers fall back to Scan.
func Open(emb embed.Embedder) (*Index, error) {
	if emb == nil {
		return nil, nil
	}
	dir := paths.IndexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	store, err := semindex.Open(filepath.Join(dir, "sessions"), emb)
	if err != nil {
		return nil, err
	}
	idx := &Index{
		store:     store,
		emb:       emb,
		filesPath: filepath.Join(dir, "sessions.files.json"),
		idleTTL:   resolveIdleTTL(),
		sessions:  map[string]sessionRecord{},
		lastUsed:  time.Now(),
	}
	idx.loadRecords()
	// Opening reads the metadata sidecar eagerly; drop it again until the index
	// is actually used, so merely wiring the index up costs no memory.
	_ = store.Unload()
	return idx, nil
}

// resolveIdleTTL reads OMNIS_SESSION_INDEX_IDLE (a Go duration). 0 disables the
// idle unload (the index stays resident once loaded).
func resolveIdleTTL() time.Duration {
	v := strings.TrimSpace(os.Getenv("OMNIS_SESSION_INDEX_IDLE"))
	if v == "" {
		return defaultIdleTTL
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return defaultIdleTTL
	}
	return d
}

func (i *Index) loadRecords() {
	b, err := os.ReadFile(i.filesPath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &i.sessions)
}

func (i *Index) saveRecords() error {
	b, err := json.MarshalIndent(i.sessions, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.filesPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, i.filesPath)
}

// Len reports the number of indexed chunks.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return i.store.Len()
}

// touch records index activity, deferring the idle unload.
func (i *Index) touch() {
	i.mu.Lock()
	i.lastUsed = time.Now()
	i.mu.Unlock()
}

func chunkID(sessionID string, turnIndex, offset int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%d:%d", turnIndex, offset)
	return h.Sum64()
}

// turnText renders one turn as the text that gets embedded: the user's request
// and the assistant's reply, labelled. This is exactly what was on screen.
func turnText(t turn) string {
	var b strings.Builder
	if u := strings.TrimSpace(t.UserText); u != "" {
		b.WriteString("User: ")
		b.WriteString(u)
	}
	if a := strings.TrimSpace(t.AssistantText); a != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Assistant: ")
		b.WriteString(a)
	}
	return b.String()
}

// contentHash hashes only the indexed text of a session, so a metadata-only
// change to the conversation file does not force a re-embed.
func contentHash(c *conv) string {
	h := sha256.New()
	for _, t := range c.Turns {
		_, _ = h.Write([]byte(turnText(t)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// chunkConv splits a session into per-turn items (windowing any turn longer
// than maxChunkChars) and returns the items plus their chunk ids.
func chunkConv(c *conv) ([]semindex.Item, []uint64) {
	var items []semindex.Item
	var ids []uint64
	for ti, t := range c.Turns {
		body := turnText(t)
		if strings.TrimSpace(body) == "" {
			continue
		}
		runes := []rune(body)
		for off := 0; off < len(runes); off += maxChunkChars - chunkOverlap {
			end := off + maxChunkChars
			if end > len(runes) {
				end = len(runes)
			}
			text := strings.TrimSpace(string(runes[off:end]))
			if text == "" {
				break
			}
			id := chunkID(c.ID, ti, off)
			meta, _ := json.Marshal(chunkMeta{
				SessionID: c.ID,
				TurnIndex: ti,
				At:        t.At,
				Text:      text,
			})
			// Prefix the session title so a query matching the topic of the
			// conversation ranks its turns up, mirroring docindex's path prefix.
			embedText := text
			if title := strings.TrimSpace(c.Title); title != "" {
				embedText = title + "\n" + text
			}
			items = append(items, semindex.Item{ID: id, Text: embedText, Meta: meta})
			ids = append(ids, id)
			if end >= len(runes) {
				break
			}
		}
	}
	return items, ids
}

// Reindex brings the index up to date with every persisted conversation. Only
// changed/new sessions are re-embedded; chunks of changed, hidden, or deleted
// sessions are removed first. Cheap no-op when nothing changed, so it is safe to
// call on every search. Returns the count of sessions (re)indexed and removed.
func (i *Index) Reindex(ctx context.Context) (indexed, removed int, err error) {
	if i == nil {
		return 0, 0, embed.ErrNoEmbedder
	}
	i.touch()
	ids := listSessionIDs()

	i.mu.Lock()
	defer i.mu.Unlock()

	// The vector store was invalidated (embedder model/dim changed, so
	// semindex.Open dropped the persisted index): the per-session hash cache no
	// longer matches any stored chunks. Drop it so everything is re-embedded
	// rather than skipped as "unchanged" against an empty store.
	if i.store.Len() == 0 && len(i.sessions) > 0 {
		i.sessions = map[string]sessionRecord{}
	}

	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		c, _, lerr := loadConv(id)
		if lerr != nil || !c.searchable() {
			continue // unreadable, empty, or hidden ⇒ not in the corpus
		}
		seen[id] = true
		changed, uerr := i.upsertConvLocked(ctx, c)
		if uerr != nil {
			return indexed, removed, uerr
		}
		if changed {
			indexed++
		}
	}
	// Drop chunks for sessions that disappeared (deleted) or left the corpus.
	for id, rec := range i.sessions {
		if seen[id] {
			continue
		}
		_ = i.store.Remove(rec.IDs...)
		delete(i.sessions, id)
		removed++
	}

	if indexed == 0 && removed == 0 {
		return 0, 0, nil
	}
	if err := i.store.Save(); err != nil {
		return indexed, removed, err
	}
	return indexed, removed, i.saveRecords()
}

// IndexSession indexes (or refreshes) a single session and persists. This is the
// live path, driven by the idle-indexer rail. Hash-gated: an unchanged session
// costs one file read.
func (i *Index) IndexSession(ctx context.Context, sessionID string) error {
	if i == nil {
		return embed.ErrNoEmbedder
	}
	i.touch()
	c, _, err := loadConv(sessionID)
	if err != nil {
		return err
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if !c.searchable() {
		// Hidden / emptied / deleted since: make sure it is not left behind.
		if rec, ok := i.sessions[sessionID]; ok {
			_ = i.store.Remove(rec.IDs...)
			delete(i.sessions, sessionID)
			if serr := i.store.Save(); serr != nil {
				return serr
			}
			return i.saveRecords()
		}
		return nil
	}
	changed, err := i.upsertConvLocked(ctx, c)
	if err != nil || !changed {
		return err
	}
	if err := i.store.Save(); err != nil {
		return err
	}
	return i.saveRecords()
}

// upsertConvLocked re-embeds a session when its indexed content changed.
// Reports whether any work was done. Caller holds i.mu.
func (i *Index) upsertConvLocked(ctx context.Context, c *conv) (bool, error) {
	hash := contentHash(c)
	if rec, ok := i.sessions[c.ID]; ok && rec.Hash == hash {
		return false, nil // unchanged
	}
	if rec, ok := i.sessions[c.ID]; ok && len(rec.IDs) > 0 {
		_ = i.store.Remove(rec.IDs...)
	}
	items, ids := chunkConv(c)
	if len(items) == 0 {
		delete(i.sessions, c.ID)
		return false, nil
	}
	if err := i.store.Upsert(ctx, items); err != nil {
		return false, err
	}
	i.sessions[c.ID] = sessionRecord{Hash: hash, IDs: ids}
	return true, nil
}

// Hit is one search result: the best-matching turn of one session.
type Hit struct {
	SessionID string    `json:"session_id"`
	TurnIndex int       `json:"turn_index"`
	At        time.Time `json:"at"`
	Snippet   string    `json:"snippet"`
	Score     float32   `json:"score"`
}

// Search returns the top-k SESSIONS most relevant to the query, each represented
// by its best-scoring turn. Chunks are over-fetched (several may belong to one
// session) and folded by session id.
//
// It does NOT build the index implicitly: a cold index is answered by the caller
// via Scan (which is what the "index is still building" path shows the user),
// while the build runs in the background. Callers that want a fresh index call
// Reindex first.
func (i *Index) Search(ctx context.Context, query string, k int) ([]Hit, error) {
	if i == nil {
		return nil, embed.ErrNoEmbedder
	}
	i.touch()
	if k <= 0 {
		k = 10
	}
	// Over-fetch: a single session can own many of the top chunks.
	hits, err := i.store.Query(ctx, query, k*4)
	if err != nil {
		return nil, err
	}
	terms := queryTerms(query)
	best := map[string]Hit{}
	for _, h := range hits {
		var m chunkMeta
		if err := json.Unmarshal(h.Meta, &m); err != nil || m.SessionID == "" {
			continue
		}
		// Hybrid ranking: semantic similarity, nudged by how many of the user's
		// actual words the passage contains (see lexicalWeight). A search box gets
		// keywords, and pure cosine buries the session that literally says them.
		score := h.Score + lexicalWeight*lexicalOverlap(m.Text, terms)
		if cur, ok := best[m.SessionID]; ok && cur.Score >= score {
			continue
		}
		best[m.SessionID] = Hit{
			SessionID: m.SessionID,
			TurnIndex: m.TurnIndex,
			At:        m.At,
			Snippet:   snippet(m.Text, terms),
			Score:     score,
		}
	}
	out := make([]Hit, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// Unload drops the index from memory (see semindex.Store.Unload).
func (i *Index) Unload() {
	if i == nil {
		return
	}
	if err := i.store.Unload(); err != nil {
		log.Printf("sessindex: unload: %v", err)
	}
}

// StartIdleSweeper drops the index from memory once it has gone idleTTL without
// a search or an indexing pass. The next use transparently re-reads it from
// disk. A zero TTL disables the sweeper (the index stays resident once loaded).
func (i *Index) StartIdleSweeper(ctx context.Context) {
	if i == nil || i.idleTTL <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(sweepInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				i.mu.Lock()
				idle := time.Since(i.lastUsed)
				i.mu.Unlock()
				if idle < i.idleTTL || !i.store.Loaded() {
					continue
				}
				i.Unload()
				log.Printf("sessindex: unloaded after %s idle", idle.Round(time.Second))
			}
		}
	}()
}
