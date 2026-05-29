// Package precedents indexes finalised sessions' goals and decisions into a
// semantic store so the reflector/curator (and optionally the leader) can
// recall how similar past sessions were resolved before deciding.
//
// One item is indexed per (session_key, kind, index): the session goal
// (kind="goal", index=0) and each Decisions[i] (kind="decision", index=i).
// Ids are derived deterministically from that triple so re-indexing the same
// session is idempotent — the backfill command and the live session-end hook
// converge on the same store.
package precedents

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/yoke/core/embed"
	"github.com/blouargant/yoke/internal/compress"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/semindex"
)

// RecallToolName is the tool the reflector/curator call to find precedents.
const RecallToolName = "recall_precedents"

// Meta is the per-item metadata persisted in the index sidecar and returned on
// a hit.
type Meta struct {
	SessionKey string `json:"session_key"`
	Kind       string `json:"kind"` // "goal" | "decision"
	Text       string `json:"text"`
	Timestamp  string `json:"timestamp"`
}

// Store wraps a semindex.Store specialised for session precedents.
type Store struct {
	s *semindex.Store
}

// Open opens (or creates) the precedents store at $YOKE_HOME/index/precedents.
// A nil embedder yields a degraded handle whose Query returns no results and
// whose Tool reports recall is unavailable.
func Open(emb embed.Embedder) (*Store, error) {
	s, err := semindex.Open(filepath.Join(paths.IndexDir(), "precedents"), emb)
	if err != nil {
		return nil, err
	}
	return &Store{s: s}, nil
}

func itemID(sessionKey, kind string, index int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionKey))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%d", index)
	return h.Sum64()
}

// Add upserts a session's goal + decisions into the index WITHOUT persisting.
// Callers must call Save() afterwards. ts is the session timestamp (file mtime
// for backfill, time.Now for live). A missing/empty StateLog is a no-op.
func (p *Store) Add(ctx context.Context, sessionKey string, sl *compress.StateLog, ts time.Time) error {
	if p == nil || sl == nil {
		return nil
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	stamp := ts.UTC().Format(time.RFC3339)
	var items []semindex.Item
	add := func(kind, text string, idx int) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		meta, _ := json.Marshal(Meta{SessionKey: sessionKey, Kind: kind, Text: text, Timestamp: stamp})
		items = append(items, semindex.Item{ID: itemID(sessionKey, kind, idx), Text: text, Meta: meta})
	}
	if g := strings.TrimSpace(sl.Goal); g != "" {
		add("goal", g, 0)
	}
	for i, d := range sl.Decisions {
		add("decision", d, i)
	}
	if len(items) == 0 {
		return nil
	}
	return p.s.Upsert(ctx, items)
}

// IndexStateLog adds a session's goal + decisions and persists immediately —
// the per-session live path (one session per EventSessionReflected).
func (p *Store) IndexStateLog(ctx context.Context, sessionKey string, sl *compress.StateLog, ts time.Time) error {
	if err := p.Add(ctx, sessionKey, sl, ts); err != nil {
		return err
	}
	return p.Save()
}

// Save persists the index to disk.
func (p *Store) Save() error {
	if p == nil {
		return nil
	}
	return p.s.Save()
}

// Hit is one recalled precedent.
type Hit struct {
	SessionKey string  `json:"session_key"`
	Kind       string  `json:"kind"`
	Text       string  `json:"text"`
	Timestamp  string  `json:"timestamp"`
	Score      float32 `json:"score"`
}

// Query returns the top-k most similar precedents to text.
func (p *Store) Query(ctx context.Context, text string, k int) ([]Hit, error) {
	if p == nil {
		return nil, nil
	}
	hits, err := p.s.Query(ctx, text, k)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		var m Meta
		_ = json.Unmarshal(h.Meta, &m)
		out = append(out, Hit{SessionKey: m.SessionKey, Kind: m.Kind, Text: m.Text, Timestamp: m.Timestamp, Score: h.Score})
	}
	return out, nil
}

// Len reports how many precedent items are indexed.
func (p *Store) Len() int {
	if p == nil {
		return 0
	}
	return p.s.Len()
}

type recallIn struct {
	Query string `json:"query"`
	K     int    `json:"k"`
}

type recallOut struct {
	Precedents []Hit `json:"precedents"`
}

// Tool builds the recall_precedents tool. Returns (nil, nil) when the store has
// no embedder so callers can skip mounting it.
func (p *Store) Tool() (tool.Tool, error) {
	if p == nil {
		return nil, nil
	}
	desc := "Recall goals and decisions from past sessions semantically similar to a query. " +
		"Use it to check how comparable problems were resolved before before deciding. " +
		"Arguments: `query` (string, required); `k` (int, optional, default 5)."
	return functiontool.New(functiontool.Config{Name: RecallToolName, Description: desc},
		func(_ tool.Context, in recallIn) (recallOut, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return recallOut{}, fmt.Errorf("query is required")
			}
			k := in.K
			if k <= 0 {
				k = 5
			}
			hits, err := p.Query(context.Background(), query, k)
			if err != nil {
				return recallOut{}, err
			}
			return recallOut{Precedents: hits}, nil
		})
}
