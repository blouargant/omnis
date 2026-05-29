// Package semindex is a thin, reusable persistence + query layer over a
// go-turbovec IdMapIndex. It pairs the ANN index with a JSON metadata sidecar
// (external id → opaque Meta) and a manifest (embedder model, dim, corpus
// hash) so callers can detect staleness and rebuild when the embedder changes.
//
// One Store backs each semantic feature (softskill recall, precedent recall,
// codebase search). All three share this code; only the corpus and the id
// scheme differ.
//
// Graceful degradation: Open with a nil embedder returns a usable handle whose
// Query/Upsert return ErrNoEmbedder, letting callers fall back to glob/grep
// paths without nil checks scattered everywhere.
package semindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	goturbovec "github.com/blouargant/go-turbovec"

	"github.com/blouargant/yoke/core/embed"
)

// Item is one record to index: a stable external id, the text to embed, and
// opaque metadata returned verbatim on a hit.
type Item struct {
	ID   uint64
	Text string
	Meta json.RawMessage
}

// Hit is one search result, ordered by descending Score (cosine similarity).
type Hit struct {
	ID    uint64
	Score float32
	Meta  json.RawMessage
}

// Manifest records what the index was built with so staleness (model/dim
// change) and corpus drift can be detected without re-reading the corpus.
type Manifest struct {
	Model      string `json:"model"`
	Dim        int    `json:"dim"`
	Count      int    `json:"count"`
	CorpusHash string `json:"corpus_hash"`
}

// metaFile is the on-disk shape of the <name>.meta.json sidecar.
type metaFile struct {
	Manifest Manifest                   `json:"manifest"`
	Meta     map[string]json.RawMessage `json:"meta"`
}

const bitWidth = 4

// Store is a persistent semantic index. It is safe for concurrent use.
type Store struct {
	base string // path without extension; .tvim + .meta.json derive from it
	emb  embed.Embedder

	mu       sync.Mutex
	idx      *goturbovec.IdMapIndex
	meta     map[uint64]json.RawMessage
	manifest Manifest
}

// Open loads an existing index (<path>.tvim + <path>.meta.json) if present and
// compatible with emb's model/dim; otherwise it returns an empty store ready
// to be populated. A nil emb yields a degraded handle (Query/Upsert error with
// ErrNoEmbedder), so callers can always Open and decide later whether to mount
// recall tools. `path` is the base path (no extension).
func Open(path string, emb embed.Embedder) (*Store, error) {
	s := &Store{
		base: path,
		emb:  emb,
		meta: map[uint64]json.RawMessage{},
	}
	if emb == nil {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("semindex: mkdir: %w", err)
	}

	mf, err := loadMetaFile(s.metaPath())
	if err != nil {
		return nil, err
	}
	// Manifest mismatch (model change) ⇒ rebuild from scratch: drop any
	// existing vectors + meta so the caller re-populates from the corpus.
	if mf != nil && mf.Manifest.Model == emb.Model() {
		idx, lerr := goturbovec.LoadIdMapFile(s.tvimPath())
		if lerr == nil {
			s.idx = idx
			s.manifest = mf.Manifest
			for k, v := range mf.Meta {
				if id, perr := parseUint(k); perr == nil {
					s.meta[id] = v
				}
			}
		}
	}
	return s, nil
}

func (s *Store) tvimPath() string { return s.base + ".tvim" }
func (s *Store) metaPath() string { return s.base + ".meta.json" }

// Len returns the number of live items in the index.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil {
		return 0
	}
	return s.idx.Len()
}

// Manifest returns a snapshot of the current manifest.
func (s *Store) Manifest() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.manifest
	m.Count = 0
	if s.idx != nil {
		m.Count = s.idx.Len()
	}
	return m
}

// SetCorpusHash records the corpus content hash on the manifest (persisted by
// the next Save). Callers use it to decide whether a rebuild is needed.
func (s *Store) SetCorpusHash(h string) {
	s.mu.Lock()
	s.manifest.CorpusHash = h
	s.mu.Unlock()
}

// Reset clears the index and metadata (used for a full corpus rebuild).
func (s *Store) Reset() {
	s.mu.Lock()
	s.idx = nil
	s.meta = map[uint64]json.RawMessage{}
	s.manifest.CorpusHash = ""
	s.mu.Unlock()
}

func (s *Store) ensureIndex(dim int) error {
	if s.idx != nil {
		return nil
	}
	idx, err := goturbovec.NewIdMap(goturbovec.Config{Dim: dim, BitWidth: bitWidth, UnitNorm: true})
	if err != nil {
		return fmt.Errorf("semindex: new index: %w", err)
	}
	s.idx = idx
	s.manifest.Model = s.emb.Model()
	s.manifest.Dim = dim
	return nil
}

// Upsert embeds each item's text and (re-)adds it under its external id,
// removing any stale vector for that id first. Metadata is replaced.
func (s *Store) Upsert(ctx context.Context, items []Item) error {
	if s.emb == nil {
		return embed.ErrNoEmbedder
	}
	if len(items) == 0 {
		return nil
	}
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.Text
	}
	vecs, err := s.emb.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(items) || len(vecs[0]) == 0 {
		return fmt.Errorf("semindex: embedder returned %d vectors of unexpected shape", len(vecs))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureIndex(len(vecs[0])); err != nil {
		return err
	}
	ids := make([]uint64, len(items))
	for i, it := range items {
		if s.idx.Contains(it.ID) {
			_ = s.idx.Remove(it.ID)
		}
		ids[i] = it.ID
	}
	if err := s.idx.AddWithIDs(vecs, ids); err != nil {
		return fmt.Errorf("semindex: add: %w", err)
	}
	for _, it := range items {
		s.meta[it.ID] = it.Meta
	}
	return nil
}

// Query embeds text and returns the top-k most similar items with their meta.
func (s *Store) Query(ctx context.Context, text string, k int) ([]Hit, error) {
	if s.emb == nil {
		return nil, embed.ErrNoEmbedder
	}
	if k <= 0 {
		k = 10
	}
	vecs, err := s.emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("semindex: empty query embedding")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil || s.idx.Len() == 0 {
		return nil, nil
	}
	scores, ids, err := s.idx.Search(vecs[0], k, nil)
	if err != nil {
		return nil, fmt.Errorf("semindex: search: %w", err)
	}
	hits := make([]Hit, 0, len(ids))
	for i, id := range ids {
		hits = append(hits, Hit{ID: id, Score: scores[i], Meta: s.meta[id]})
	}
	return hits, nil
}

// Remove deletes ids from the index and metadata.
func (s *Store) Remove(ids ...uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil {
		return nil
	}
	for _, id := range ids {
		if s.idx.Contains(id) {
			_ = s.idx.Remove(id)
		}
		delete(s.meta, id)
	}
	return nil
}

// Contains reports whether id is present in the index.
func (s *Store) Contains(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx != nil && s.idx.Contains(id)
}

// Save persists the index (.tvim) and metadata sidecar (.meta.json) atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.base), 0o755); err != nil {
		return fmt.Errorf("semindex: mkdir: %w", err)
	}
	if err := s.idx.WriteFile(s.tvimPath()); err != nil {
		return fmt.Errorf("semindex: write index: %w", err)
	}

	mf := metaFile{
		Manifest: s.manifest,
		Meta:     make(map[string]json.RawMessage, len(s.meta)),
	}
	mf.Manifest.Count = s.idx.Len()
	// Stable ordering for deterministic files (eases diffing / debugging).
	keys := make([]uint64, 0, len(s.meta))
	for id := range s.meta {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, id := range keys {
		mf.Meta[formatUint(id)] = s.meta[id]
	}
	b, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath())
}

func loadMetaFile(path string) (*metaFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("semindex: read meta: %w", err)
	}
	var mf metaFile
	if err := json.Unmarshal(b, &mf); err != nil {
		// A corrupt sidecar is treated as "no index" — rebuild from corpus.
		return nil, nil
	}
	if mf.Meta == nil {
		mf.Meta = map[string]json.RawMessage{}
	}
	return &mf, nil
}

func parseUint(s string) (uint64, error) {
	var v uint64
	_, err := fmt.Sscan(s, &v)
	return v, err
}

func formatUint(v uint64) string { return fmt.Sprintf("%d", v) }
